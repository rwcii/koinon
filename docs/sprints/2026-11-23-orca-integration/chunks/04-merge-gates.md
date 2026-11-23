# Chunk 04 — Merge gates

Cites `../decision.md` (Merge gates, recorded in WDR 004, Decision 8; acceptance 4) and
`../definition-of-done.md` (Merge gates).

## Outcome

A pull request into `develop` needs a passing `preflight` with the security, code, standards
and refusal lenses, and a `board-verdict` on its exact head. The `release` skill stops before
the merge to `main`; the maintainer merges.

## Work

1. Preflight for Koinon: `.koinon/skills/preflight.md` with the Review run command
   (`scripts/preflight-run.sh`: one fresh worker for each lens, on the frozen head, no forge
   credential, reports in an ignored folder), the Review publish command
   (`scripts/preflight-publish.sh`: one comment and the `preflight` status on the head, refused
   when the PR head differs), and the lens policy with the four lens briefs. PDR 008, Decision
   5: Koinon now adopts `preflight`.
2. `ship`: after the reviews pass, put a card on the board that names the PR and its exact
   head; after the approval, post `board-verdict` on that head with
   `scripts/board-verdict.sh PR HEAD REFERENCE`, which reads the card's verdict, checks that it
   is an approval of that head, and refuses otherwise. A new head needs a new card.
3. `release`: stop when the promotion PR and its evidence are ready; report; after the
   maintainer's merge, push the tag and check the publish steps. `.koinon/skills/release.md`
   changes its promotion path to match.
4. `scripts/setup-repo.sh` declares `preflight` and `board-verdict` as required statuses on
   `develop`. The live ruleset changes only after the maintainer's go in this PR.
5. Portable scope: the gates are project policy. `ship` gets the optional fact "Merge
   approval" (default "none") and `release` the optional fact "Promotion merge" (default "the
   promotion policy"); the board card, the `board-verdict` status and the stop before `main`
   apply only when the facts say so. Koinon's `.koinon/skills/ship.md` and `release.md` opt in.
6. Rollout: this chunk's own pull request runs the new preflight and posts `preflight` with
   the scripts on its branch, and gets a board card and `board-verdict` on its head, as the
   first use. The ruleset change comes after the merge, with the maintainer's go: the
   maintainer runs `scripts/setup-repo.sh` or approves its run, and the result is read back
   with `gh api repos/rwcii/koinon/rulesets`. Until then, the statuses are posted but not
   required, and the skills still follow the rule.
7. Records and docs: WDR 004 (the gates, with the maintainer's words); CONTRIBUTING; the skills
   README; CHANGELOG.

## Tests

- Shell syntax checks for the new scripts.
- Tests with a fake `gh` and a fake card store: the verdict script posts `board-verdict` for an
  approval of the exact head, and refuses a different head, a missing approval and a declined
  card; the publish script posts for the exact head and refuses another head.
- A contributor test that no skill text tells an agent to merge into `main`, and that a facts
  file without "Merge approval" and "Promotion merge" keeps today's ship and release steps.
- The release path: the release steps are skill text, with no script, so the contributor
  test checks the text (the stop for the maintainer's merge when "Promotion merge" names the
  maintainer, and no merge into `main`); a dry run against a fake `gh` has no script to run.
  The first real release (v0.13.4) is the live check, in `verification.md`. (Corrected while
  the chunk was built.)
