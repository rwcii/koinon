"""Strict selected membership for the immutable global release receipt."""
import unittest

import upgrade_release


class ReleaseEvidenceTests(unittest.TestCase):
    def setUp(self):
        self.loaded = dict(sha256='a' * 64, documents=dict(components=dict(items=[
            dict(kind='session', running=True), dict(kind='memory', running=True),
            dict(kind='memory', running=False)])))
        self.members = [dict(component=0, kind='bridge', generation='b' * 32),
                        dict(component=0, kind='notifier', generation='c' * 32),
                        dict(component=1, kind='memory', generation='d' * 32)]
        self.evidence = dict(version=1, plan='a' * 64, members=self.members)

    def test_running_pair_and_memory_are_required_and_inactive_memory_stays_closed(self):
        self.assertEqual(upgrade_release.validate(self.loaded, self.evidence), self.evidence)
        with self.assertRaises(upgrade_release.ReleaseError):
            upgrade_release.validate(self.loaded, dict(self.evidence, members=self.members + [
                dict(component=2, kind='memory', generation='e' * 32)]))
        for index in range(3):
            with self.subTest(missing=index), self.assertRaises(upgrade_release.ReleaseError):
                upgrade_release.validate(self.loaded, dict(self.evidence, members=self.members[:index] + self.members[index + 1:]))

    def test_duplicate_foreign_kind_and_wrong_plan_refuse(self):
        variants = [dict(self.evidence, plan='f' * 64),
                    dict(self.evidence, members=self.members + [self.members[0]]),
                    dict(self.evidence, members=self.members + [dict(component=2, kind='bridge', generation='e' * 32)]),
                    dict(self.evidence, members=self.members + [dict(component=99, kind='memory', generation='e' * 32)])]
        for value in variants:
            with self.subTest(value=value), self.assertRaises(upgrade_release.ReleaseError):
                upgrade_release.validate(self.loaded, value)
