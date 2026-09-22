"""Staged rendering does not establish native session supervision."""
import os
import plistlib
import unittest
from unittest.mock import patch

from koinon import platform_support
import session


class SessionNativeArtifactTests(unittest.TestCase):
    def render(self, **overrides):
        values = dict(prefix='/synthetic/prefix $dollar %n "quote"',
                      python='/synthetic/python space', thread='synthetic-session',
                      repository='/synthetic/repo space', domain=f'gui/{os.geteuid()}')
        values.update(overrides)
        return platform_support.session_launchd_artifact(**values)

    def test_exact_literal_arguments_and_stable_existing_session_identity(self):
        with patch.object(platform_support.subprocess, 'run') as run:
            data = self.render()
            self.assertEqual(data, self.render())
            run.assert_not_called()
        job = plistlib.loads(data)
        self.assertEqual(job['Label'], 'io.github.rwcii.koinon.session.' + session.identity('synthetic-session'))
        self.assertEqual(job['ProgramArguments'], ['/synthetic/python space',
            '/synthetic/prefix $dollar %n "quote"/session.py', 'run',
            '--thread=synthetic-session', '--repo', '/synthetic/repo space'])
        self.assertEqual(job['Umask'], 63)
        self.assertEqual(job['KeepAlive'], {'SuccessfulExit': False})
        self.assertEqual(job['ThrottleInterval'], 10)
        self.assertIs(job['RunAtLoad'], True)
        self.assertEqual(job['KoinonManaged'], 'session-service-v1')

    def test_distinct_sessions_and_literal_leading_dash_values(self):
        first = plistlib.loads(self.render(thread='-synthetic', agent='deepseek', model='--literal model'))
        second = plistlib.loads(self.render(thread='other-synthetic'))
        self.assertNotEqual(first['Label'], second['Label'])
        self.assertIn('--thread=-synthetic', first['ProgramArguments'])
        self.assertEqual(first['ProgramArguments'][-3:], ['--agent', 'deepseek', '--model=--literal model'])

    def test_invalid_selection_never_queries_or_mutates_a_manager(self):
        variants = [dict(domain=None), dict(domain='system'), dict(domain=f'gui/{os.geteuid()+1}'),
                    dict(thread=''), dict(thread='bad/name'), dict(thread=1), dict(agent='claude'),
                    dict(model='bad\nvalue'), dict(model=''), dict(model=3), dict(model='x' * 513),
                    dict(prefix='relative'), dict(repository='/synthetic/../other'),
                    dict(python='/synthetic/python\n')]
        with patch.object(platform_support.subprocess, 'run') as run:
            for variant in variants:
                with self.subTest(variant=variant), self.assertRaises(ValueError):
                    self.render(**variant)
            run.assert_not_called()
