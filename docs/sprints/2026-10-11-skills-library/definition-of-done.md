# Skills library and adoption — definition of done

## Required tests

- Catalog IDs are unique, metadata is complete, content digests match, supported families
  cover the actual family registry, and catalog bytes come from the installed binary.
  Invalid IDs, unsupported families and oversized items are refused. Build the binary,
  remove the checkout from its search path and still list/read its catalog.
- MCP list/read/state use native identity; guessed caller fields and arbitrary target paths
  are refused. Exercise each MCP family with synthetic native metadata and DeepSeek's
  command path with its existing identity/admission rules. Native identity is never returned
  in catalog content.
- Initialize and guide always offer the catalog, including repeated initialize/reconnect.
  Offers are informational; these calls neither inspect nor change adopter files nor
  record a decision. Actual tool-purpose and family coverage guards from #253 remain current.
- Temporary repositories, linked worktrees and temporary native homes exercise present,
  missing, outdated, edited, unreadable and symlink/escape cases. Outside files stay
  unread. An inspection failure returns a reason, not a guessed state.
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
  messages and absolute home paths. Review the seven skill texts as product content.

## Integration and gate

Use the existing HTTP client/server, MCP identity guard, command path and repository memory
store; no parallel persistence or identity mechanism. Document any new tool in the actual
tool-purpose table. Existing installation and upgrade proof must demonstrate that catalog
content requires no file beside the binary and an upgrade applies no catalog item.

Before each publication: the check skill, latest develop ancestry, full Go vet and race suite,
whitespace/privacy checks and four CGO-free builds for runtime changes. Linux/macOS Go and
native lifecycle CI must pass on the final head. The observer records an exact-head verdict;
each PR has squash auto-merge enabled. Real services run only on disposable CI runners.

## Maintainer gate

No product chunk begins before the maintainer approves the reviewed plan under sprint Gate B.
Later adopters approve their own exact changes; approval of this product plan is not their
adoption approval.
