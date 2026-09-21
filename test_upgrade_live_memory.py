"""Actual isolated memory entrypoint under a retained upgrade marker."""
import asyncio
import shutil
import subprocess
import sys
import time
import unittest

import memory
import memory_service
from peer_transport import control_exchange
from scripts.install import FILES
import test_upgrade_capture as fixtures
import upgrade_inventory


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
                deadline = time.monotonic() + 10
                hello = None
                while time.monotonic() < deadline:
                    if process.poll() is not None:
                        self.fail('gated child exited: ' + process.communicate()[1])
                    try:
                        hello = asyncio.run(memory.verify_running(self.home, self.key))
                    except (OSError, ValueError):
                        hello = None
                    if hello:
                        break
                    time.sleep(.02)
                self.assertIsNotNone(hello)
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
            finally:
                if process.poll() is None:
                    process.terminate()
                try:
                    process.communicate(timeout=10)
                except subprocess.TimeoutExpired:
                    process.kill()
                    process.communicate()
                    raise
