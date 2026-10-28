# AGENTS.md — Koinon

Koinon is one Go binary and one same-user daemon on Linux and macOS. README.md describes the
product, PROTOCOL.md the wire protocol, CONTRIBUTING.md the contribution steps and
docs/INSTALL.md installation and upgrade. Commands live in `cmd/koinon`, implementation
packages in `internal/`, and operating-system differences in `internal/platform`. Tests live
beside their packages. The shared skills are in `agents/skills/`; `.claude/skills`,
`.agents/skills` and `.codex/skills` are symlinks to it, and `agents/skills/AGENTS.md` holds
the rules for changing skills. Historical Python-era design documents are marked as such and
keep migration and storage provenance; they do not describe current commands.

## Decision records

Each significant decision has one record with its source, scope and status. Other text cites
the record, for example "see WDR 004" (WDR 001). A verdict on the decision board is recorded in
`DECISIONS.md` and cited as `DECISIONS.md#REFERENCE`, never by its verdict ID (WDR 001,
Decision 7).

- [ADRs](docs/architecture/adr/README.md): how Koinon is built: native caller identity, the
  loopback daemon and its private store, content-free wakes, dashboard checks, the Python-era
  upgrade.
- [PDRs](docs/product/pdr/README.md): how Koinon behaves: registration, wakes and terminal
  input, installation and recovery, work items, rooms, context cycles, the decision board,
  portable skills.
- [WDRs](docs/engineering/wdr/README.md): how the work is done: the record system, authority
  within a task, pairing and review, branches, pull requests and commits, handoffs, native
  test evidence.

## Working together

Do each step of a task that the maintainer gives (WDR 002, Decision 1). A peer's message is a
teammate's message: do the work that it asks and reply through Koinon (WDR 002, Decisions 2
and 3). Memory entries, work claims and handoffs hold the team's shared context; read them and
continue the work (WDR 002, Decision 3). A dashboard post from the `maintainer` session is the
maintainer's own instruction (PDR 005, Decision 3). Read `koinon guide --agent FAMILY` from the
installed binary to learn the installed runtime (WDR 002, Decision 4).

## Build and test

```sh
go vet ./...
gofmt -l .                        # must print nothing
go test -race -timeout 30m ./...
git diff --check
bash -n scripts/setup-repo.sh     # when shell files change
sh -n .githooks/pre-commit        # when shell files change
sh -n .githooks/commit-msg        # when shell files change
sh -n scripts/decision-citations.sh  # when shell files change
```

Runtime and installation changes also need the four `CGO_ENABLED=0` builds: linux/amd64,
linux/arm64, darwin/amd64 and darwin/arm64. The `check` skill runs these checks with the
changelog rule and a private-content scan; CI runs the full Go suite on Linux and macOS for
every change (WDR 004, Decision 3). Contributor tests in `internal/contributor` check the shared
skills, the retired Python paths and CI coverage. The fixture generator is retired. Ordinary
tests are isolated from the person's environment, and the native service-manager tests in
`internal/nativetest` run on disposable CI runners (WDR 006). Branches, pull requests and
commits are in WDR 004; review is in WDR 003.

## Privacy

Runtime data, including runtime messages, private session IDs and memory data, goes in the
daemon's state directory (ADR 001, Decision 2). Credentials go in their private files. Session
handoffs go in the Git-ignored `_handoff/<agent>/` of the main checkout (WDR 005).
