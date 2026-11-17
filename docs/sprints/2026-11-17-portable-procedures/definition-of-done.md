# Portable procedures — definition of done

Every chunk cites `decision.md` and this file. Tests are synthetic and run in CI on Linux and
macOS (WDR 004, Decision 3).

## The seven skills (chunk 01)

- `agents/skills/NAME/SKILL.md` for each of the seven. Each differs from the draft that the
  maintainer saw only in its procedure-source line, which names its Koinon record. The pull
  request lists each draft SHA-256, each final SHA-256 and that one-line diff; the observer
  checks them against the drafts. A contributor test compares each final file's SHA-256 with
  a constant; a later change of a skill updates its constant in the same pull request.
- Each repository-facts block parses (`library.Embedded`), its fields and defaults are the
  drafts' fields, and each item is within the catalog bounds (PDR 008, Decision 4): 26 items
  of at most 32, each at most 64 KiB.
- `TestTeamSkills` and `TestOpenCodeCommandSkills` list the seven new names; `koinon install`
  in its existing synthetic tests writes the seven entries; the generated OpenCode commands
  include them.
- `TestRepositoryFactsMatchCanonicalSchemasAndPolicy`: the seven are offered, not adopted, so a
  missing fact file is allowed for them by name; every fact file that is present is still
  checked, and the existing skills keep their requirement.
- A contributor scan finds no name of the source project in the seven files, with a positive
  control that it finds a planted one.
- `agents/skills/README.md` has a row for each; PDR 008 Decisions 1 and 5 and the Document
  History; LICENSE; CHANGELOG Unreleased.
- Full `go test -race ./...`, `go vet ./...` and `go vet -tags native ./internal/nativetest/`,
  `gofmt -l .` empty, `git diff --check`, the four `CGO_ENABLED=0` builds, and exact-head CI
  on Linux, macOS and the native lifecycle jobs: the binary embeds the new skills.

## The install script (chunk 02, after its verdict)

- `scripts/install-skills.sh VERSION`, POSIX `sh`, checked with `sh -n`.
- The release workflow adds `koinon-skills.tar.gz` (every `agents/skills/*/SKILL.md` of the
  tag) and its line in SHA256SUMS.
- Tests against a synthetic release folder: a good hash writes each skill as
  `koinon-NAME/SKILL.md` with only its `name:` line changed; a wrong hash writes nothing; a
  symbolic link entry is kept; `koinon-guide` is skipped; the OpenCode and agy roots only when
  their folder exists; a second run reports unchanged. A test shows that `koinon install`
  afterwards replaces the copies with its entries.
- PDR 008 Decision 2, docs/INSTALL.md and CHANGELOG; the release workflow change runs on its
  pull request path.
