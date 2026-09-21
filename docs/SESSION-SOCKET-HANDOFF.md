# Parent-owned session control sockets

The owned runner binds and records control sockets before spawning bridge and
notifier children. The implementation retains uncertainty if the wrapper itself dies
between binding and durable inode publication.

A child can bind its control socket and die before a verified status exchange. The
runner then has no captured inode and correctly refuses to remove it. Seeing a new
path while holding `supervisor.lock` cannot prove ownership: direct bridge and notifier
commands do not acquire that lock.

The runner binds each control socket before spawning that child, records the inode
durably, and passes the bound descriptor explicitly through `Popen(pass_fds=...)`.
The child validates and uses the inherited descriptor instead of binding the path.
Normal direct bridge/notifier commands retain exclusive bind behavior. Manager
job arguments and saved notifier command selection remain stable; descriptor numbers
are per-attempt arguments added only by the owner.

Version 2 ownership records add a parent-endpoint map and endpoint-creation intent
independent of `spawn_pending`: an endpoint exists before its child PID exists.
The order is durable endpoint intent, exclusive bind, durable path/device/inode capture,
durable spawn intent, spawn with descriptor, durable child PID/start identity, then
verified control generation and readiness. A failure to publish any required intent
must prevent the following side effect.

A child receiving an inherited descriptor must check that it is an AF_UNIX stream
socket bound to the selected control path, that current path metadata matches the
recorded inode, and that the exact parent generation/PID/start identity is still the
owner. It refuses the flag on non-serving commands and never infers a descriptor from
an environment variable. Missing, invalid or misassigned inherited descriptors cause
permanent configuration refusal; they never fall back to a new bind. The exact parent
and child assignment checks also refuse an accidentally inheriting grandchild. Inherited sockets do not bypass same-user control credentials,
request admission, notifier ownership or database readiness.

After a confirmed direct-child exit, the parent may close and remove only its unchanged
captured socket, even if the child never answered status. It must still refuse a
replacement inode, an unknown process, conflicting owner evidence or an unconfirmed
shutdown. Ordinary startup must not remove an operator-created endpoint.

There remains a different crash window between the parent's bind and durable inode
publication. The endpoint intent preserves that uncertainty; a dead wrapper is not
proof that an unrecorded path belongs to it. That case must remain an explicit recovery
condition rather than silently becoming automatic adoption. Parent handoff closes the
child's pre-handshake crash gap; it does not make filesystem bind and record publication
atomic.

Validation must inject failures before bind, after bind, before spawn, immediately
after child exec, before first status, and after readiness. Verify recorded intent,
absence of unauthorized unlink, no inherited descriptor leaks, exact child shutdown,
retained refusal/provenance, and native restart on both Linux and macOS. Existing direct
CLI startup and same-user control tests must continue to pass.

Version 1 records remain readable and retain the older conservative captured-endpoint
rules. Earlier runtimes refuse version 2 rather than silently ignoring its intents.
A failed endpoint-creation intent remains visible and blocks automatic retry. No
operator assertion is inferred from path absence or a timeout.

Validation includes real Linux native jobs where a notifier exits before accepting its
inherited socket and the manager restarts the complete pair. The seven-check native
fixture also retains the runtime crash, independent second session, permanent failure,
explicit retry and exact deregistration cases. The parent bind/publication crash window
remains a named recovery condition and is not claimed solved by these tests.
