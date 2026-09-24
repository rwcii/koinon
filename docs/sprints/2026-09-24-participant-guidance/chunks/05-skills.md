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
