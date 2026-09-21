"""Publish native selection for a newly registered, explicitly targeted session."""
import hashlib
import json
import os
from pathlib import Path
import sys

import durable_state
import install_state
from peer_transport import private_dir
import runtime_names
import session_service_artifacts as artifacts
import session_service_config as configuration


def stage(prefix, config, state, thread, repo, agent, model, register):
    if config.get('installation_state') == 'removing':
        raise ValueError('installation removal is incomplete; resume uninstall')
    backend = config.get('session_backend')
    if backend not in ('systemd', 'launchd'):
        raise ValueError('native session backend was not selected')
    state = Path(state)
    intent_path = state / 'native-install-intent.json'
    selection = {key: config.get(key) for key in ('state_root', 'codex', 'dsh_url', 'dsh_credentials')}
    expected = dict(version=1, thread=thread, prefix=str(prefix), backend=backend,
                    configuration=hashlib.sha256(json.dumps(selection, sort_keys=True).encode()).hexdigest())
    # Refuse existing legacy registrations before creating locks or selections.
    if (runtime_names.present(state / 'session.json')
            and not runtime_names.present(state / 'native-service.json')
            and not runtime_names.present(intent_path)):
        saved = durable_state.read(state / 'session.json')
        archived = artifacts.archived_removal(prefix, state, config, saved)
        if archived is None or archived['backend'] != backend:
            raise ValueError('existing legacy session requires explicit upgrade: ' + str(state / 'session.json'))
    with install_state.locked(prefix) as installed:
        if installed.config != config:
            raise ValueError('installation selection changed during session registration')
        private_dir(state)
        with artifacts.locked(state / 'lifecycle.lock'), artifacts.locked(state / 'registration.lock'):
            saved = durable_state.read(state / 'session.json')
            native = artifacts.load(state)
            intent = durable_state.read(intent_path)
            if native is None and intent is not None and intent != expected:
                raise ValueError('native installation intent changed; preserve ' + str(intent_path))
            if saved is not None and native is None and intent is None:
                archived = artifacts.archived_removal(prefix, state, config, saved)
                if archived is None or archived['backend'] != backend:
                    raise ValueError('existing legacy session requires explicit upgrade: ' + str(state / 'session.json'))
            if native is None and intent is None:
                durable_state.publish(intent_path, expected)
            if saved is None:
                saved = register(state, Path(config['state_root']), thread, repo, agent=agent, model=model)
            if saved.get('thread') != thread:
                raise ValueError('session registration identity differs from explicit target')
            private_dir(state / 'native-service')
            desired = configuration.selection(prefix, sys.executable, state, config, saved, backend,
                                               domain=f'gui/{os.geteuid()}' if backend == 'launchd' else None)
    # Artifact publication reacquires the installation and session locks and
    # validates registration/configuration again. Never recursively acquire them.
    return artifacts.publish(desired)
