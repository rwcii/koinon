# Chunk 01 — rooms, membership and awareness

Delivers the matching criteria in decision.md; use definition-of-done.md's membership,
identity, audit, migration, quota and contributor checks. No post/reply tool yet.

Add room IDs as opaque 32-character IDs; names are bounded UTF-8 display data (128 bytes),
not addresses. Store rooms and exact native memberships in the existing DB. Bound rooms
at 64 and active members at 32 per room; at most 128 current/former membership rows per
room. A cap refuses visibly without evicting a room/member/history. Names may duplicate;
all APIs/actions use the stable room ID. Store membership revision and current active bit.
Reserve a membership-start sequence field: an initial add or explicit re-add records the
current room post head as its exclusive lower bound; a duplicate add leaves it unchanged.
Native expiry/re-registration does not change that bound. Chunk02 uses it for transcript
catch-up; chunk01 has no posts and therefore initializes it to zero.
Removed membership rows provide removal awareness but grant no roster/transcript access.
Native expiration does not erase membership; show inactive and skip delivery until that
same native identity is active again. Retirement remains inactive. A new native session or
participant holder receives nothing automatically.

Add authenticated dashboard create and add/remove actions. Resolve selected peer names
inside the write transaction and require an active native peer; refuse participant aliases
as membership targets. Changes increment the roster revision and audit in the same ordinary
transaction. The first room page can name a room, list members and change its roster.
Do not add decision-board actions or claim a room post as an approval.

Add room_list/room_members and equivalent `koinon room list/members` commands. Derive callers
from current native admission. List current and former membership status for that caller;
room_members requires current membership. Unknown/removed callers cannot enumerate another
room's roster. No argument supplies caller family/native ID. Bound pages to 100 and response
bytes to the existing one-MiB inbox envelope; return explicit next cursor/more.

An add/remove transaction sends the affected exact native session one bounded typed inbox
notice (room ID, revision, added/removed kind), through the existing message/wake lifecycle.
Extend inbox metadata with those trusted fields, never parse peer text as a room event. A
remove targets its currently active recipient; if already inactive, the retained membership
status supplies catch-up on its own subsequent active room_list. Capacity refusal rolls back
membership plus notice/audit, rather than promising a notification not stored. Native reset
or a missing wake never resets an inbox acknowledgement cursor.

Account both tables/indexes and notice metadata in ordinary page/logical accounting. Add
schema migration and native-tool-purpose coverage. Use existing CSRF, audit, private state,
provider stubs, native guides and Unreleased docs; do not change live instructions/config.
