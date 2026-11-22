# Definition of done

See [decision](decision.md) and the specification acceptance matrix.

Automated checks: fixed deadline with fake-clock boundary and rollback tests;
password generation, durable restart/revocation and failed-store tests; existing
origin/CSRF/QR/API separation tests; synthetic browser network, held-form, storage
and receipt recovery tests; concurrent duplicate operation IDs and digest conflict;
Attention snapshot/cursor tests proving no agent inbox acknowledgement.

Run `go vet ./...`, `gofmt -l .`, `go test -race -timeout 30m ./...`,
`git diff --check`, four CGO_ENABLED=0 platform builds, and compile/vet native
lifecycle tests. Reproduce baseline failures before attributing them; retain logs
privately. Review final diff for secrets and private content and update changelog,
usage, protocol and PDR 010/ADR 002. Independent code review is required locally;
cross-family commit-bound review and Linux/macOS CI remain publication gates.

Actual acceptance still requires iOS Safari and Android Chrome versions and elapsed
one-hour, overnight and 48-hour tests; cellular/VPN/sleep transitions; keyboard and
zoom at 360/390px; disposable Linux/macOS service lifecycle and upgrade/rollback.
Synthetic tests and platform cross-compiles cannot substitute for these results.
