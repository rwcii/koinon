"""Declared runtime layout migrations between releases.

A release that moves a shipped file records the move here. Replacement refuses an
undeclared removal, because a path that disappears without a declaration cannot be
explained to whoever reads the upgrade evidence, and cannot be told apart from an
operator's own change to the installation.

A declaration is not permission to delete anything else. Each entry must name a
destination that the new release actually publishes, and the old path is removed
only after its replacement is published and confirmed.
"""
from koinon import upgrade_manifest as manifest


class LayoutError(ValueError):
    pass


# Old prefix-relative path -> new prefix-relative path. The 2026-09 restructure
# moved the implementation modules into the `koinon` package and left the
# executable entrypoints at the prefix, because installed service definitions
# name their paths and are compared byte for byte.
MOVES = {
    'claims.py': 'koinon/claims.py',
    'codex_instructions.py': 'koinon/codex_instructions.py',
    'component_install.py': 'koinon/component_install.py',
    'component_remove.py': 'koinon/component_remove.py',
    'database_worker.py': 'koinon/database_worker.py',
    'delivery_ledger.py': 'koinon/delivery_ledger.py',
    'dsh_delivery.py': 'koinon/dsh_delivery.py',
    'durable_state.py': 'koinon/durable_state.py',
    'generation_stop.py': 'koinon/generation_stop.py',
    'inbox_schema.py': 'koinon/inbox_schema.py',
    'install_state.py': 'koinon/install_state.py',
    'memory_bindings.py': 'koinon/memory_bindings.py',
    'memory_service_artifacts.py': 'koinon/memory_service_artifacts.py',
    'memory_service_config.py': 'koinon/memory_service_config.py',
    'notification_delivery.py': 'koinon/notification_delivery.py',
    'notification_health.py': 'koinon/notification_health.py',
    'notification_journal.py': 'koinon/notification_journal.py',
    'notification_legacy.py': 'koinon/notification_legacy.py',
    'notification_memory.py': 'koinon/notification_memory.py',
    'notification_migration.py': 'koinon/notification_migration.py',
    'notification_notices.py': 'koinon/notification_notices.py',
    'notification_provider.py': 'koinon/notification_provider.py',
    'notification_runtime.py': 'koinon/notification_runtime.py',
    'notification_source.py': 'koinon/notification_source.py',
    'notification_state.py': 'koinon/notification_state.py',
    'participant_instructions.py': 'koinon/participant_instructions.py',
    'participant_lock.py': 'koinon/participant_lock.py',
    'participant_presence.py': 'koinon/participant_presence.py',
    'peer_guidance.py': 'koinon/peer_guidance.py',
    'peer_transport.py': 'koinon/peer_transport.py',
    'platform_support.py': 'koinon/platform_support.py',
    'runtime_names.py': 'koinon/runtime_names.py',
    'service_runtime.py': 'koinon/service_runtime.py',
    'session_endpoints.py': 'koinon/session_endpoints.py',
    'session_install.py': 'koinon/session_install.py',
    'session_observation.py': 'koinon/session_observation.py',
    'session_service_artifacts.py': 'koinon/session_service_artifacts.py',
    'session_service_config.py': 'koinon/session_service_config.py',
    'session_service_manager.py': 'koinon/session_service_manager.py',
    'session_socket_handoff.py': 'koinon/session_socket_handoff.py',
    'session_supervisor.py': 'koinon/session_supervisor.py',
    'session_supervisor_state.py': 'koinon/session_supervisor_state.py',
    'subscriptions.py': 'koinon/subscriptions.py',
    'uninstall_finalize.py': 'koinon/uninstall_finalize.py',
    'upgrade_backup.py': 'koinon/upgrade_backup.py',
    'upgrade_backup_inventory.py': 'koinon/upgrade_backup_inventory.py',
    'upgrade_bundle.py': 'koinon/upgrade_bundle.py',
    'upgrade_capture.py': 'koinon/upgrade_capture.py',
    'upgrade_command.py': 'koinon/upgrade_command.py',
    'upgrade_complete.py': 'koinon/upgrade_complete.py',
    'upgrade_coordinator.py': 'koinon/upgrade_coordinator.py',
    'upgrade_discovery.py': 'koinon/upgrade_discovery.py',
    'upgrade_documents.py': 'koinon/upgrade_documents.py',
    'upgrade_exclusion.py': 'koinon/upgrade_exclusion.py',
    'upgrade_gate.py': 'koinon/upgrade_gate.py',
    'upgrade_inventory.py': 'koinon/upgrade_inventory.py',
    'upgrade_journal.py': 'koinon/upgrade_journal.py',
    'upgrade_manifest.py': 'koinon/upgrade_manifest.py',
    'upgrade_manual.py': 'koinon/upgrade_manual.py',
    'upgrade_migration.py': 'koinon/upgrade_migration.py',
    'upgrade_observation.py': 'koinon/upgrade_observation.py',
    'upgrade_plan.py': 'koinon/upgrade_plan.py',
    'upgrade_preflight.py': 'koinon/upgrade_preflight.py',
    'upgrade_probe.py': 'koinon/upgrade_probe.py',
    'upgrade_quiescence.py': 'koinon/upgrade_quiescence.py',
    'upgrade_release.py': 'koinon/upgrade_release.py',
    'upgrade_replace.py': 'koinon/upgrade_replace.py',
    'upgrade_reservation.py': 'koinon/upgrade_reservation.py',
    'upgrade_start.py': 'koinon/upgrade_start.py',
    'usage_selection.py': 'koinon/usage_selection.py',
    'usage_sources.py': 'koinon/usage_sources.py',
    'work_guidance.py': 'koinon/work_guidance.py',
    'work_items.py': 'koinon/work_items.py',
    'work_maintenance.py': 'koinon/work_maintenance.py',
    'work_policy.py': 'koinon/work_policy.py',
    'work_schema.py': 'koinon/work_schema.py',
    'work_storage.py': 'koinon/work_storage.py',
}


def declared(removed, published):
    """Pair each disappearing runtime path with the published path that replaces it.

    `removed` is the set of paths the old runtime holds and the new release does
    not. Every one must be declared, and its destination must be published, or the
    upgrade refuses before it shuts anything down.
    """
    moves = []
    for old in sorted(removed):
        new = MOVES.get(old)
        if new is None:
            raise LayoutError('undeclared runtime file removal: ' + old)
        if new not in published:
            raise LayoutError('declared move has no published destination: ' + old)
        moves.append((old, new))
    if moves:
        manifest.names_checked([old for old, _ in moves])
        manifest.names_checked([new for _, new in moves])
    return tuple(moves)
