# Shared skill authoring

The canonical project skill source is `agents/skills/`. Project discovery links point here;
machine-wide adoption copies the same skill bytes. Edit the canonical source
and regenerate its derived wrappers. Keep the project's chosen layout.

Each skill has one `NAME/SKILL.md`, with single-line YAML `name` and `description` only.
The name matches the directory and uses lower-case letters, digits and hyphens. Write for
all supported agent families; use installed runtime guidance for family-specific interfaces.
Read the adopting project's instructions. This authoring file is for editing the canonical
source; a machine-wide skill works without it.

Keep procedures portable. Project branches, commit/check/CI rules and release steps go in the
`.koinon/skills/NAME.md` facts, outside the embedded source. Declare required and optional
headings with defaults in the skill's repository-facts section. Facts supply project data, and
the root instructions supply the project's policy.

Invoking a skill or its task authorizes its steps (WDR 002, Decision 1). A skill says what to do,
step by step, and cites the record for a significant choice (WDR 001, Decisions 3 and 5).
Koinon's records describe its procedures; an adopting project's instructions describe that
project's work.

Add every canonical skill to this directory's README. Derive OpenCode commands from metadata;
keep custom commands and report collisions. The adopting project's contributor tooling checks
its actual source, wrappers, discovery links, fact schemas and policy.

This source and its derived artifacts are public: write native identities, credentials,
runtime messages, handoffs, memory content and home paths as placeholders. Team manifests and
session handoffs stay in ignored private storage.
