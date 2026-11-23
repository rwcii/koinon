# Chunk 04 — Merge gates

Cites `../decision.md` (Merge gates; acceptance 4) and `../definition-of-done.md` (Merge
gates).

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
5. Records and docs: WDR 004 (the gates, with the maintainer's words); WDR 002, Decision 6;
   CONTRIBUTING; the skills README; CHANGELOG.

## Tests

Shell syntax checks for the new scripts; tests with a fake `gh` for the publish and verdict
scripts (head mismatch, missing approval, declined card); a contributor test that no skill
text tells an agent to merge into `main`.
