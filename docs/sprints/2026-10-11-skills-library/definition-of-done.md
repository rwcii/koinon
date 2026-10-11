# Skills library and adoption — definition of done

## Required tests

- Every actual canonical `agents/skills/NAME/SKILL.md` has exactly one skill catalog item
  with identical bytes/digest. No independent skill-body tree exists in `internal/library`.
  Add a valid skill to a temporary source tree, rebuild, and observe it in the catalog through
  the same enumeration; changing canonical content changes the embedded read result.
- `.agents/skills`, `.codex/skills` and `.claude/skills` remain relative symlinks to the shared
  tree. The wrapper generator/check covers every actual skill, including the new team skills.
  A policy-extraction review verifies that Koinon's existing gates still live in root instructions;
  contributor tests still catch drift between the actual Go workflow and project check policy.
- The raw embedding whitelist excludes root `AGENTS.md`/pairing authorization, handoffs,
  sprint plans and private state. Shared skill authoring instructions are reference data.
- Catalog IDs are unique, metadata is complete, content digests match, supported families
  cover the actual family registry, and catalog bytes come from the installed binary.
  Invalid IDs, unsupported families and oversized items are refused. Build the binary,
  remove the checkout from its search path and still list/read its catalog.
- MCP list/read/state use native identity; guessed caller fields and arbitrary target paths
  are refused. State defaults to the registered directory; an optional directory must resolve
  into a worktree listed for the same Git common directory. Exercise each MCP family with synthetic native metadata and DeepSeek's
  command path with its existing identity/admission rules. Native identity is never returned
  in catalog content.
- Initialize and guide always offer the catalog, including repeated initialize/reconnect.
  Offers are informational; these calls neither inspect nor change adopter files nor
  record a decision. Actual tool-purpose and family coverage guards from #253 remain current.
- Temporary repositories, linked worktrees and temporary native homes exercise present,
  missing, outdated, edited, unreadable and symlink/escape cases. Outside files stay
  unread. An inspection failure returns a reason, not a guessed state. Inspect the real
  `setup.claude-user-skill` item under a temporary HOME for all four content states; no synthetic
  catalog item substitutes for the required agent-home proof. Test a session registered in the
  main checkout selecting its linked worktree, and refuse foreign repository/plain directories.
- Test the actual discovery-link item: correct link bytes are present; a custom link is edited;
  an escaping link is refused without following it or changing it. The native-home guide item
  has the same canonical body as its repository counterpart.
- Collection items such as generated wrappers retain bounded per-target descriptor IDs/digests
  in receipts. Adding a canonical skill offers its new file and wrapper through the same process.
  An unchanged prior wrapper collection is outdated after a new wrapper is added; a locally
  edited wrapper is edited and retained. Report per-target states for mixed collections.
- State/list/read cause no filesystem writes and no adoption/memory acknowledgement writes.
  Existing MCP registration/renewal effects remain documented separately.
- Adopt one approved item by applying its reviewed change in a fixture, record the decision,
  query present, and repeat without changing files or adding another identical receipt.
  Record a decline, reconnect and upgrade: the decline still suppresses a new-item offer.
- Old catalog + recorded digest + unchanged old bytes yields outdated after upgrade;
  modified bytes yield edited. Missing bytes with a prior receipt yields missing. Correct
  current bytes yield present even without a receipt. A receipt alone never proves content
  or approval.
- Conflicting decisions are reported explicitly; a concurrent replacement is not silently
  chosen as approval. Page all receipt results, preserve the existing memory idempotency
  deadline and conflict rules, and test capacity refusals without touching adopter files.
- Test managed instruction fragments without replacing surrounding user text. Check exact
  proposed file bytes before an adoption; if bytes changed since review, rebuild the proposal
  and get approval for the changed diff. Native pointers/config recommendations are shown
  as separate changes and never bundled into a skill adoption implicitly.
- Team skill scenario tests cover a dirty checkout, first-run prompt, unavailable launcher,
  lost registration, uncertain wake, resumed session and unverified terminal target. The
  written procedure requires authorization and verified interfaces before action and stops
  when evidence is missing. No test runs a real agent, provider, terminal or service.
- Scan embedded catalog bytes for repository-specific authority, private IDs, credentials,
  messages and absolute home paths. Review every canonical skill text as product content.

## Integration and gate

Use the existing HTTP client/server, MCP identity guard, command path and repository memory
store; no parallel persistence or identity mechanism. Document any new tool in the actual
tool-purpose table. Existing installation and upgrade proof must demonstrate that catalog
content requires no file beside the binary and an upgrade applies no catalog item.

Because canonical skill content becomes an installed-runtime input, update the native-CI
classifier and its contributor tests: `agents/skills/` source/metadata/content changes require
native lifecycle evidence and cannot use the former agent-only skip. Before query tools
exist, the version report's `catalog_digest` references and proves the embedded catalog.
Test that fingerprint without a runtime checkout; after chunk02, exercise actual list/read.

Before each publication: the check skill, latest develop ancestry, full Go vet and race suite,
whitespace/privacy checks and four CGO-free builds for runtime changes. Linux/macOS Go and
native lifecycle CI must pass on the final head. The observer records an exact-head verdict;
each PR has squash auto-merge enabled. Real services run only on disposable CI runners.

## Maintainer gate

No product chunk begins before the maintainer approves the reviewed plan under sprint Gate B.
Later adopters approve their own exact changes; approval of this product plan is not their
adoption approval.
