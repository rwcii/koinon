import copy
import hashlib
import json
import os
import plistlib
import unittest
from unittest.mock import patch

from koinon import platform_support
from koinon import session_service_config as configuration


class NativeSessionSelectionTests(unittest.TestCase):
    def setUp(self):
        self.registration = dict(thread='synthetic-session', name='synthetic-peer', repo='/synthetic/repo',
                                 agent='codex', model=None)
        self.config = dict(state_root='/synthetic/state', codex='/synthetic/bin/codex')
        key = hashlib.sha256(self.registration['thread'].encode()).hexdigest()[:16]
        self.home = '/synthetic/state/sessions/' + key

    def candidate(self, backend='systemd', **kwargs):
        return configuration.selection('/synthetic/prefix $literal %n "quote"', '/synthetic/python space',
                                       self.home, self.config, self.registration, backend, **kwargs)

    def test_selection_and_rendering_are_pure_exact_and_stable(self):
        with patch.object(platform_support.subprocess, 'run') as command:
            selected = self.candidate()
            self.assertEqual(selected, self.candidate())
            configuration.verify(selected, self.config, self.registration)
            command.assert_not_called()
        content = platform_support.session_service_artifact(selected)
        self.assertEqual(hashlib.sha256(content).hexdigest(), selected['artifact_digest'])
        self.assertIn(b'ExecStart=:', content)
        self.assertIn(b'$literal %%n', content)
        self.assertIn(b'RestartSec=5', content)
        self.assertNotIn(b'WantedBy=', content)
        self.assertNotIn(self.registration['thread'].encode(), content)
        self.assertLess(len(json.dumps(selected).encode()), 4096)

    def test_registration_and_participant_command_changes_require_reconciliation(self):
        selected = self.candidate()
        changes = [dict(self.registration, name='renamed'), dict(self.registration, repo='/different'),
                   dict(self.registration, model='different-model'), dict(self.registration, thread='other')]
        for registration in changes:
            with self.subTest(registration=registration), self.assertRaises(ValueError):
                configuration.verify(selected, self.config, registration)
        for config in (dict(self.config, state_root='/different'), dict(self.config, codex='/other/codex')):
            with self.subTest(config=config), self.assertRaises(ValueError):
                configuration.verify(selected, config, self.registration)
        configuration.verify(selected, dict(self.config, unrelated='preserved'), self.registration)

    def test_explicit_launchd_domain_and_literal_wrapper_arguments(self):
        selected = self.candidate('launchd', domain=f'gui/{os.geteuid()}')
        job = plistlib.loads(platform_support.session_service_artifact(selected))
        self.assertEqual(job['ProgramArguments'], [selected['python'], selected['prefix'] + '/session_service.py',
                         'run', '--prefix', selected['prefix'], '--state-dir', self.home, '--backend', 'launchd'])
        self.assertEqual(job['KeepAlive'], {'SuccessfulExit': False})
        self.assertEqual(job['ThrottleInterval'], 10)
        self.assertEqual(job['Umask'], 63)
        for domain in (None, 'system', f'gui/{os.geteuid()+1}'):
            with self.subTest(domain=domain), self.assertRaises(ValueError):
                self.candidate('launchd', domain=domain)

    def test_saved_selection_rejects_path_and_digest_tampering(self):
        selected = self.candidate()
        for field, value in (('artifact', '/foreign.service'), ('session_key', 'f' * 16),
                             ('prefix', '/synthetic/../other'), ('manager_domain', 'gui/0'),
                             ('participant_digest', 'invalid'), ('version', True)):
            with self.subTest(field=field), self.assertRaises(ValueError):
                configuration.validate(dict(selected, **{field: value}))
        with self.assertRaises(ValueError):
            configuration.verify(dict(selected, artifact_digest='f' * 64), self.config, self.registration)

    def test_pending_publication_cannot_adopt_existing_artifact_preimage(self):
        selected = self.candidate()
        pending = dict(selected, state='pending', before_digest=None, after_digest=selected['artifact_digest'])
        configuration.validate(pending)
        configuration.verify(pending, self.config, self.registration)
        with self.assertRaises(ValueError):
            configuration.validate(dict(pending, before_digest=selected['artifact_digest']))

    def test_native_deepseek_selection_binds_explicit_harness_and_credentials(self):
        self.registration['agent'] = 'deepseek'
        with self.assertRaises(ValueError):
            self.candidate()
        self.config.update(dsh_url='http://synthetic.invalid', dsh_credentials='/synthetic/private/credentials')
        selected = self.candidate()
        argv = configuration.commands(selected['prefix'], selected['python'], self.home,
                                      self.config, self.registration)['notifier']
        self.assertIn('--dsh-url=http://synthetic.invalid', argv)
        self.assertIn('/synthetic/private/credentials', argv)
        self.assertNotIn('--codex', argv)
        changed = dict(self.config, dsh_url='http://other.invalid')
        with self.assertRaises(ValueError):
            configuration.verify(selected, changed, self.registration)
