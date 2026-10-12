---
name: peer-review
description: Review another agent's frozen commit for Koinon - standards, security and correctness - report only findings you have reproduced or can show in the code, and record the verdict on the pull request bound to that commit. Use when a peer asks for review of a hash, before any merge, and for a sprint plan at its review gate.
---

# Peer review

Read `agents/skills/AGENTS.md` and the root `AGENTS.md` first.

The reviewer is a session of another agent family than the author's (WDR 003, Decision 3).
Review what is committed, not what the author says about it.

## 1. Bind the target

Review one commit hash. Read it without disturbing the author's worktree:

```sh
git fetch origin
git diff origin/develop...<sha>
git show <sha>:<path>
```

Every verdict is bound to the current hash. For a fix-only commit, review the listed fixes,
their affected behavior and required checks, carrying forward the earlier review of unchanged
content. A new hash does not restart a general audit. A substantive scope change needs review
of the changed scope.

## 2. Read with three lenses

- **Standards**: the root `AGENTS.md`, `CONTRIBUTING.md`, the decision records and the documents
  the change touches.
  Documentation matches the code; `internal/platform` holds operating-system differences;
  peer addresses and socket paths stay unresolved; the changelog rule is followed.
- **Security**: the same-user boundary, inert peer controls, explicit thread targeting,
  content-free notifications, no execution of incoming peer text, and no private content in
  the diff.
- **Correctness**: the change does what it claims on Linux and macOS, error paths included, and
  its tests would fail without it.
- **Records**: the change agrees with the accepted ADRs, PDRs and WDRs under `docs/`. Judge the
  actual effect of new text, tests and code conditions: a new restriction, gate or refusal with
  no record that covers its case is a finding (WDR 001).

## 3. Verify each finding

Report a finding only when you have reproduced it (a test, a command, a synthetic repository) or
can point to the lines that show it (WDR 003, Decision 4). Give each one the scenario that fails and a severity. Drop
a hazard that no reachable input triggers. Do not report style preferences as findings.

Run only the checks that the change needs. Reuse the author's reported results for the same
commit; rerun a check only when you doubt it.

For a paired increment, follow `pair`: send one batch of findings against its written criteria
and the repository's mandatory boundaries. Fix verification stays limited to the accepted
blockers and affected behavior. A newly exposed blocker needs a demonstrated, reachable
failure of those criteria or a concrete security or data-loss defect; otherwise finish the review.

## 4. Record the verdict

Send the findings to the author. When no accepted blocker is open, post the sign-off on the pull
request (WDR 003, Decisions 3 and 4):

```text
Peer review (<agent family>): approved at <sha>. <checks run and their results>.
```

The comment is the durable verdict. Then record it as the `peer-review` commit status on the
same full hash, linked to that comment. The `develop` ruleset requires the status, so the pull
request cannot merge before the sign-off or on another head:

```sh
gh api "repos/{owner}/{repo}/statuses/<sha>" -f context=peer-review -f state=success \
  -f description="approved by <agent family>" -f target_url=<sign-off comment URL>
```

Read it back, and check its state and URL:

```sh
gh api "repos/{owner}/{repo}/commits/<sha>/status" \
  --jq '.statuses[] | select(.context == "peer-review") | [.state, .target_url]'
```

When the call fails or the read-back differs, the verdict is not recorded; report it as not
recorded, not as approved. When findings are open, post them on the pull request and record
`-f state=failure` with `-f description="changes requested by <agent family>"` and the URL of
that comment. Record a status only on the hash that you reviewed; a new commit has none until
its own review. The reviewer records `peer-review`; the author does not set it on its own pull
request (WDR 003, Decision 3). One account holds every agent, so the status identifies the
GitHub user, not the agent family that set it. It holds the merge until the review when every
agent follows this practice; it does not authenticate the reviewer.

A sign-off records a review; it grants no permission beyond what the maintainer already gave
(WDR 003, Decision 4).
