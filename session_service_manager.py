"""Native activation only after selection, manager and pair identities agree."""
import subprocess
import shlex
import time

import platform_support
import session_service as service
import session_service_artifacts as artifacts
from session_supervisor_state import alive_state, state_lock

START_TIMEOUT = 25


def upgrade_options(selection):
    return {'upgrading': True} if getattr(selection, 'upgrade', None) is not None else {}


def observation(selection):
    result = platform_support.session_manager_observation(selection.record)
    if result['status'] == 'observed':
        expected = platform_support.session_service_command(selection.record)
        if result['argv'] != expected or result['executable'] != expected[0]:
            raise service.ServiceError('session_ownership_unknown', paths=(result['artifact'],))
        artifacts.verify_loaded(selection.record, result['artifact'], **upgrade_options(selection))
    return result


def refusal(selection):
    records = selection.records
    saved = records.read(refusal=True)
    owner = records.read()
    if saved is not None or (owner is not None and owner['exit_status'] in platform_support.PERMANENT_EXIT_STATUSES
                             and not records.retry_requested(owner['generation'])):
        failed = saved or owner
        return dict(status='refused', basis='recorded_failure', exit_status=failed['exit_status'],
                    primary_code=failed['primary_code'], shutdown_code=failed['shutdown_code'],
                    generation=failed['generation'], recovery=selection.recovery())
    return None


def status(selection):
    failure = refusal(selection)
    if failure is not None:
        return failure
    before = observation(selection)
    portable = service.status(selection)
    after = observation(selection)
    owner = portable['owner']
    if (before == after and after['status'] == 'observed' and portable['status'] == 'running'
            and owner is not None and owner['pid'] == after['pid']
            and alive_state(owner) == 'alive' and selection.records.read() == owner):
        return dict(portable, managed=True, basis='manager_and_live_pair_identity')
    return dict(portable, status='unavailable', managed=False,
                basis='incomplete_manager_observation', manager=after['status'])


def start_admission(selection, observed):
    records = selection.records
    with state_lock(selection.home / 'supervisor.lock', 'session_supervisor_in_use'):
        old = records.read()
        if old is not None:
            if (old['configuration'] != records.configuration or alive_state(old) != 'dead'
                    or any(child is not None and alive_state(child) != 'dead'
                           for child in old['children'].values())
                    or old['spawn_pending'] is not None and not records.spawn_recovered(old)):
                raise service.ServiceError('session_ownership_unknown')
            if ((old['exit_status'] in platform_support.PERMANENT_EXIT_STATUSES or old['spawn_pending'] is not None)
                    and not records.retry_requested(old['generation'])):
                raise service.ServiceError('session_configuration_failure')
        if observed['status'] == 'observed' and observed['pid'] != 0:
            raise service.ServiceError('session_ownership_unknown')


def ensure(selection):
    # These are session-local locks; unrelated session startup is independent.
    with artifacts.locked(selection.home / 'lifecycle.lock'), artifacts.locked(selection.home / 'registration.lock'):
        selection = service.Selection(selection.prefix, selection.home, backend=selection.backend)
        selection.validate_programs()
        failure = refusal(selection)
        if failure is not None:
            return failure
        observed = observation(selection)
        if observed['status'] == 'unknown':
            if not platform_support.memory_manager_available(selection.backend, selection.record['manager_domain']):
                return dict(status='manual_required', running=False,
                            start_command=shlex.join(platform_support.session_service_command(selection.record)))
            raise service.ServiceError('session_temporary_failure')
        portable = service.status(selection)
        if portable['status'] == 'running':
            result = status(selection)
            if result['status'] != 'running':
                raise service.ServiceError('session_ownership_unknown')
            return result
        owner = selection.records.read()
        active = (observed['status'] == 'observed' and observed['pid'] != 0)
        if active:
            if (owner is None or owner['pid'] != observed['pid'] or alive_state(owner) != 'alive'
                    or owner['configuration'] != selection.records.configuration):
                raise service.ServiceError('session_ownership_unknown')
        else:
            start_admission(selection, observed)
            operation = 'register' if observed['status'] == 'absent' else 'start'
            if observation(selection) != observed:
                raise service.ServiceError('session_temporary_failure')
            artifacts.verify_owned(selection.record, **upgrade_options(selection))
            try:
                platform_support.session_manager_action(selection.record, operation)
                if selection.backend == 'systemd' and operation == 'register':
                    registered = observation(selection)
                    if (registered['status'] != 'observed' or registered['pid'] != 0
                            or observation(selection) != registered):
                        raise service.ServiceError('session_ownership_unknown')
                    artifacts.verify_owned(selection.record, **upgrade_options(selection))
                    platform_support.session_manager_action(selection.record, 'start')
            except (OSError, subprocess.SubprocessError) as exc:
                # A failed or timed-out manager call has an unknown outcome.
                raise service.ServiceError('session_temporary_failure') from exc
        deadline = time.monotonic() + START_TIMEOUT
        while True:
            result = status(selection)
            if result['status'] in ('running', 'refused'):
                return result
            if time.monotonic() >= deadline:
                raise service.ServiceError('session_temporary_failure')
            time.sleep(.1)


def deactivate(selection):
    """Stop the owned pair before removing its selected manager registration."""
    with artifacts.locked(selection.home / 'lifecycle.lock'), artifacts.locked(selection.home / 'registration.lock'):
        return deactivate_locked(selection)


def deactivate_locked(selection, *, allow_unstarted=False):
    """Caller holds lifecycle and registration locks through deactivation."""
    selection = service.Selection(selection.prefix, selection.home, backend=selection.backend, removing=allow_unstarted,
                                  **upgrade_options(selection))
    observed = observation(selection)
    if observed['status'] != 'observed' and not (allow_unstarted and observed['status'] == 'absent'):
        raise service.ServiceError('session_ownership_unknown')
    owner = selection.records.read()
    if owner is None:
        import session_observation
        if (not allow_unstarted or observed['status'] == 'observed' and observed['pid'] != 0
                or any(session_observation.endpoint_present(root)
                       for root in (selection.home, selection.home / 'notifier'))):
            raise service.ServiceError('session_ownership_unknown')
    elif alive_state(owner) == 'alive':
        if owner['pid'] != observed['pid']:
            raise service.ServiceError('session_ownership_unknown')
        service.stop_owned(selection)
    elif (alive_state(owner) != 'dead' or owner['spawn_pending'] is not None
          or any(child is not None and alive_state(child) != 'dead' for child in owner['children'].values())):
        raise service.ServiceError('session_shutdown_unconfirmed')
    stopped = observation(selection)
    if allow_unstarted and stopped['status'] == 'absent' and observation(selection) == stopped:
        return dict(status='deactivated', basis='manager_absence_and_pair_exit')
    if (stopped['status'] != 'observed' or stopped['pid'] != 0
            or observation(selection) != stopped):
        raise service.ServiceError('session_ownership_unknown')
    artifacts.verify_owned(selection.record, **upgrade_options(selection))
    try:
        platform_support.session_manager_deactivate(selection.record)
    except (OSError, subprocess.SubprocessError) as exc:
        raise service.ServiceError('session_temporary_failure') from exc
    if observation(selection)['status'] != 'absent':
        raise service.ServiceError('session_ownership_unknown')
    return dict(status='deactivated', basis='manager_absence_and_pair_exit',
                retained=['selection', 'artifact', 'inbox', 'notification_history', 'supervisor_evidence'])


def stop(selection):
    with artifacts.locked(selection.home / 'lifecycle.lock'), artifacts.locked(selection.home / 'registration.lock'):
        selection = service.Selection(selection.prefix, selection.home, backend=selection.backend,
                                      **upgrade_options(selection))
        observed = observation(selection)
        owner = selection.records.read()
        if (observed['status'] != 'observed' or owner is None or observed['pid'] != owner['pid']
                or owner['configuration'] != selection.records.configuration):
            raise service.ServiceError('session_ownership_unknown')
        return service.stop_owned(selection)
