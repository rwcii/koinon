# Increment 2: Reconnect and optional draft recovery

See [decision](../decision.md), [definition of done](../definition-of-done.md)
and the original `docs/REMOTE-CONTROL-V2-SPEC.md`.

Keep reads single-flight and stop polling while hidden. Foreground/network return
checks immediately. Separate explicit authentication rejection from network loss;
retain the 15-second applied-data freshness, 1/2/5/10/30s jittered retry and no write replay.
Fresh CSRF loads without replacing or submitting forms. Sign-in in a separate tab
keeps the original in-memory draft even when browser saving is off.

Browser draft saving is opt-in, off initially, host/session/route/target scoped.
Save only message/feedback text for seven days. Recovery is explicit. Clear on
success/logout/observed revocation/explicit clear; preserve newer edits. Storage
failures must leave forms usable and visibly disable recovery. Never save secrets,
QR tickets, cookies or complete private responses.

Checks: FORM-1, PHONE-2 and synthetic read/write contract tests, quota/access and
malformed storage, background/foreground, revoked 401, new CSRF, separate room
identities and client clock rollback. Actual discarded-tab, keyboard and browser
cookie retention require the PHONE/UI acceptance matrix.
