# Increment 1: Durable sessions and revocation

See [decision](../decision.md), [definition of done](../definition-of-done.md)
and the original `docs/REMOTE-CONTROL-V2-SPEC.md`.

Persist remote browser credentials as hashes in the existing private database,
with a noncredential browser ID, actual secret-generation binding, origin,
creation/observation/deadline, label and revocation. Keep local/API memory policy
unchanged. Commit before cookie issuance; fix expiry at original sign-in +48h.
Keep remote capacity separate at 32, with explicit capacity handling.

Split ordinary listener shutdown from revocation. Reconcile trusted saved origin
and disabled state on startup, including offline configuration/password changes.
Support durable logout, rename, revoke one/all, and incomplete-storage diagnostics.
Expose nonsecret session metadata/fresh CSRF and authenticated device management.

Checks: AUTH-1–5. Exercise fake time before/at expiry, restart/reopen, rollback,
offline password/origin changes, disabled startup, failed writes and copied
credentials. Reuse existing origin/CSRF/QR/API boundary tests. Real PHONE-1 and
native LIFE-1 tests remain independently recorded acceptance.
