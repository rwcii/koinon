"""Actual isolated memory entrypoint under a retained upgrade marker."""
import waiting
import asyncio
import shutil
import subprocess
import sys
import time
import unittest

import memory
import memory_service
from koinon.peer_transport import control_exchange
from scripts.install import FILES
import test_upgrade_capture as fixtures
from koinon import upgrade_inventory


class LiveGatedMemoryTests(unittest.TestCase):
    def setUp(self):
        fixtures.MemoryCaptureTests.setUp(self)
        for name in FILES:
            target = self.prefix / name
            target.parent.mkdir(parents=True, exist_ok=True)
            shutil.copyfile(name, target)
            target.chmod(0o600)

    def owner(self):
        return fixtures.MemoryCaptureTests.owner(self)

    def test_real_entrypoint_preserves_store_and_serves_repository_bound_inventory(self):
        store = memory.Store(self.home / 'memory.sqlite3', self.key, fts=False)
        before = upgrade_inventory.capture(store.db)
        store.close()
        with self.owner() as owner:
            fixtures.MemoryCaptureTests.advance(self, owner, 10)
            selected = memory_service.Selection(self.prefix, self.repo, upgrading=True)
            process = subprocess.Popen(selected.command(), stdout=subprocess.PIPE, stderr=subprocess.PIPE, text=True)
            try:
                last = None
                def reachable():
                    nonlocal last
                    try:
                        last = asyncio.run(memory.verify_running(self.home, self.key))
                        return last
                    except (OSError, ValueError) as exc:
                        last = str(exc)
                        return False
                hello = waiting.wait_until_sync(reachable, 'gated child readiness', process=process,
                                                observe=lambda: last)
                self.assertFalse(hello['upgrade']['released'])
                request = dict(op='upgrade-inventory', plan=self.prepared['sha256'],
                               generation=hello['generation'], repo=self.key)
                reply, pid = asyncio.run(control_exchange(self.home, request))
                self.assertEqual(pid, process.pid)
                self.assertTrue(reply['ok'], reply)
                self.assertEqual(reply['result'], before)
                wrong, _ = asyncio.run(control_exchange(self.home, dict(request, repo='f' * 16)))
                self.assertFalse(wrong['ok'])
                denied, _ = asyncio.run(control_exchange(self.home,
                    dict(op='note', consumer='synthetic', type='finding', body='must remain gated')))
                self.assertFalse(denied['ok'])
                from koinon.upgrade_documents import Documents
                from koinon import platform_support
                from koinon import session_supervisor
                fixtures.MemoryCaptureTests.advance(self, owner, 16)
                receipt = Documents(self.operation).put('release', dict(
                    version=1, plan=self.prepared['sha256'], members=[]))
                owner.journal.advance(owner.journal.read(), evidence=receipt)
                _, error = process.communicate(timeout=waiting.timeout())
                self.assertIn('not verified for upgrade release', error)
                self.assertNotEqual(process.returncode, 0)
                self.assertNotIn(process.returncode, platform_support.PERMANENT_EXIT_STATUSES)
                self.assertEqual(memory_service.child_failure(process.returncode).exit_status, 75)
                self.assertEqual(session_supervisor.child_failure(process.returncode).code,
                                 'session_temporary_failure')
            finally:
                if process.poll() is None:
                    process.terminate()
                try:
                    process.communicate(timeout=waiting.timeout())
                except subprocess.TimeoutExpired:
                    process.kill()
                    process.communicate()
                    raise
