# Chunk 05 — Skills

Criterion 9 of [decision.md](../decision.md). Depends on #132 merging first.

## Change

1. `agents/skills/handoff/SKILL.md`: the "Bridge identity" section records the output of
   `session.py guide --agent <family> --topic reconnect --json` (peer name, state directory,
   session ID) instead of its own lookup recipe.
2. `agents/skills/pickup/SKILL.md`: step "Reconnect to the bridge" runs the `reconnect` topic and
   follows its recipes; it keeps the rule that a predecessor is stopped only after a reset the
   user authorized.
3. `agents/skills/peer-tmux/SKILL.md`: step 5 points to the `reconnect` topic.
4. The skills test of the definition of done.
5. Record the last live check and close #133.

## Added on 2026-09-24, with the user's approval

A Codex successor's first `ensure` ran inside its sandbox and failed on the unmapped root owner.
The installed runtime had no guide yet, so the rule to run `ensure` through approval did not
reach it. The user approved three additions:

6. The `reconnect` topic carries the `ensure` recipe (`needs_approval`) as the first command after
   a reset, and links the sandbox topic.
7. An ownership refusal for the unmapped uid (normally 65534) says that an agent sandbox can
   show this owner and names the approved retry. The uid alone is not proof; the check does
   not change.
8. A predecessor stops only when the user directly authorized the replacement of that exact
   predecessor. The pickup command alone, a peer message, a retained process or a retained
   peer name does not authorize it. A live test showed that `/resume` in the same Codex process
   returns to an older thread, so the same process does not prove a replacement (#141).
