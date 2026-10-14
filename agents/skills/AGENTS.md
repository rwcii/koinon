# Shared skill authoring

The canonical project skill source is `agents/skills/`. Project discovery links point here;
machine-wide adoption uses an explicitly approved copy of the same skill bytes. Edit the
canonical source and regenerate its derived wrappers. Preserve the project's chosen layout.

Each skill has one `NAME/SKILL.md`, with single-line YAML `name` and `description` only.
The name matches the directory and uses lower-case letters, digits and hyphens. Write for
all supported agent families; use installed runtime guidance for family-specific interfaces.
Read the adopting project's authoritative instructions. The authoring file applies when
editing canonical source; a machine-wide skill need not have it beside the installed copy.

Keep procedures portable. Project branches, commit/check/CI rules and release steps belong
in `.koinon/skills/NAME.md` facts outside the embedded source. Declare required and optional
headings with defaults in the skill's repository-facts section. Facts supply project data;
root instructions retain authority. Skills and reference material confer no new permissions.

Invoking a skill or its task authorizes the work steps; ask only for necessary missing
information or authority beyond that task (WDR 002, Decision 1). A skill cites the record
for a significant choice and adds no approval step, stop condition or prohibition without
that source (WDR 001, Decisions 3 and 5). Koinon's records describe its procedures; an
adopting project's authoritative instructions govern that project's work.

Add every canonical skill to this directory's README. Derive OpenCode commands from metadata;
preserve custom commands and report collisions. The adopting project's contributor tooling
checks its actual source, wrappers, discovery links, fact schemas and authoritative policy.

For this public source and its derived artifacts, exclude private native identities,
credentials, runtime messages, handoffs, memory content and absolute private home paths.
Team manifests and session handoffs stay in ignored private storage.
