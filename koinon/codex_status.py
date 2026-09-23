"""Read only the selected Codex rollout; retain metadata, never conversation content."""
import datetime
import json
import os
from pathlib import Path
import stat
import subprocess
import time

from koinon import participant_status as status
from koinon import platform_support as platform

SOURCE = 'codex_session_log'
BATCH_BYTES = 4 * 1024 * 1024
LINE_BYTES = 1024 * 1024


def unknown(reason):
    return {name: dict(source=SOURCE, recorded_at_ms=0, reason=reason) for name in status.FIELDS}


def stamp(value):
    parsed = datetime.datetime.fromisoformat(value.replace('Z', '+00:00'))
    if parsed.tzinfo is None:
        raise ValueError('timestamp needs timezone')
    result = int(parsed.timestamp() * 1000)
    if not 0 <= result <= status.MAX_INT:
        raise ValueError('invalid timestamp')
    return result


def open_log(path):
    fd = os.open(path, os.O_RDONLY | os.O_NOFOLLOW | os.O_NONBLOCK | os.O_CLOEXEC)
    try:
        info = os.fstat(fd)
        if not stat.S_ISREG(info.st_mode) or info.st_uid != os.geteuid():
            raise ValueError('unowned rollout')
        return os.fdopen(fd, 'rb'), info
    except BaseException:
        os.close(fd)
        raise


class Reader:
    def __init__(self, thread, codex, home=None):
        self.thread, self.codex = thread, codex
        self.home = Path(home or os.environ.get('CODEX_HOME') or Path.home() / '.codex')
        self.path = self.owner = self.inode = None
        self.offset = 0
        self.groups = unknown('no_token_usage')
        self.turn = None
        self.next_search = 0

    def locate(self):
        if not isinstance(self.thread, str) or not status._KEY.fullmatch(self.thread):
            raise ValueError('invalid selected thread')
        paths = list((self.home / 'sessions').rglob('*' + self.thread + '*.jsonl'))
        if len(paths) != 1:
            return None
        stream, _ = open_log(paths[0])
        with stream:
            first = json.loads(stream.readline(LINE_BYTES))
        if first.get('type') != 'session_meta' or first.get('payload', {}).get('id') != self.thread:
            raise ValueError('rollout identity mismatch')
        return paths[0]

    def event(self, record):
        kind = record.get('type')
        payload = record.get('payload')
        if kind not in ('turn_context', 'event_msg'):
            return
        if not isinstance(payload, dict):
            raise ValueError('invalid event')
        event = payload.get('type') if kind == 'event_msg' else 'model'
        if event not in ('model', 'task_started', 'task_complete', 'turn_aborted', 'token_count'):
            return
        when = stamp(record['timestamp'])
        group = dict(source=SOURCE, recorded_at_ms=when, reason=None)
        if event == 'model':
            group['id'] = payload['model']
            self.groups['model'] = status._group('model', group)
        elif event == 'token_count':
            info = payload.get('info')
            if not isinstance(info, dict) or not isinstance(info.get('last_token_usage'), dict):
                self.groups['context'] = dict(group, reason='no_token_usage')
                return
            group.update(limit_tokens=info.get('model_context_window'),
                         used_tokens=info['last_token_usage'].get('input_tokens'), usage_available=True)
            self.groups['context'] = status._group('context', group)
        else:
            turn = payload.get('turn_id')
            if not isinstance(turn, str) or not turn:
                raise ValueError('missing turn identity')
            if event == 'task_started':
                self.turn = turn
                self.groups['activity'] = dict(group, state='busy')
            elif turn == self.turn:
                self.groups['activity'] = dict(group, state='idle')

    def consume(self):
        stream, info = open_log(self.path)
        with stream:
            inode = (info.st_dev, info.st_ino)
            if inode != self.inode or info.st_size < self.offset:
                first = json.loads(stream.readline(LINE_BYTES))
                if first.get('type') != 'session_meta' or first.get('payload', {}).get('id') != self.thread:
                    raise ValueError('rollout identity mismatch')
                self.offset = 0
                self.turn = None
                self.groups = unknown('no_token_usage')
                self.inode = inode
            stream.seek(self.offset)
            consumed = 0
            while consumed < BATCH_BYTES:
                line = stream.readline(LINE_BYTES + 1)
                if not line:
                    break
                consumed += len(line)
                if len(line) > LINE_BYTES:
                    # Skip an oversized record incrementally without retaining its text.
                    while line and not line.endswith(b'\n') and consumed < BATCH_BYTES:
                        line = stream.readline(LINE_BYTES)
                        consumed += len(line)
                    if not line.endswith(b'\n'):
                        raise ValueError('oversized partial record')
                    self.groups = unknown('source_unrecognized')
                    self.offset = stream.tell()
                    continue
                if not line.endswith(b'\n'):
                    return False
                self.offset = stream.tell()
                self.event(json.loads(line))
            return self.offset >= os.fstat(stream.fileno()).st_size

    def sample(self):
        try:
            if self.path is None:
                if time.monotonic() < self.next_search:
                    return None, unknown('no_status_record')
                self.next_search = time.monotonic() + 15
                self.path = self.locate()
                if self.path is None:
                    return None, unknown('no_status_record')
            if self.owner is not None:
                pid = self.owner['pid']
                if not (platform.same_process(self.owner['proc_start'], platform.proc_start(pid))
                        and platform.holds_open(pid, self.path)):
                    self.owner = None
            if self.owner is None:
                holders = platform.open_file_holders(self.path)
                if len(holders) != 1 or not platform.codex_process(holders[0], self.codex):
                    return None, unknown('participant_not_associated')
                self.owner = dict(pid=holders[0], proc_start=platform.proc_start(holders[0]))
            if not self.consume():
                return self.owner, unknown('source_unrecognized')
            return self.owner, dict(self.groups)
        except (OSError, ValueError, KeyError, TypeError, AttributeError, OverflowError, RecursionError, subprocess.SubprocessError):
            self.groups = unknown('source_unrecognized')
            self.turn = None
            return None, unknown('source_unrecognized')
