# AGENTS.md — Architecture Decision Records

## What This Directory Contains

Architecture Decision Records (ADRs) that capture **how a system is built** —
structure, patterns, boundaries, infrastructure. ADRs are the architectural
counterpart to Product Decision Records (PDRs, `docs/product/pdr/`) and Workflow
Decision Records (WDRs, `docs/engineering/wdr/`).

## When You Should Read These

- Before changing how subsystems interact (service boundaries, data access, auth flow)
- Before introducing a new pattern, technology, or cross-cutting mechanism
- Before adding a new variant to an extensible system (provider, adapter, object type)
- When the `peer-review` skill checks a change against accepted ADRs

## Key Constraints

- **ADRs capture architecture, not product behavior.** If the decision survives a
  complete product pivot (new features, new flows) it belongs here. If it survives a
  complete rewrite (new language, new framework) it belongs in `docs/product/pdr/`. If
  it survives **both** — because it governs how the work is done — it belongs in a
  Workflow Decision Record (`docs/engineering/wdr/`).
- **No line numbers, no AI attribution, no volatile implementation details.** See
  `ADR_PROCESS.md` § "What ADRs Must NOT Contain."

## Numbering

Sequential from 001; point versions `NNN.N`. Upstream engineering-standards records are
cited by link and are not Koinon decisions until a Koinon record adopts them.

## File Locations

| File             | Purpose                                                      |
| ---------------- | ----------------------------------------------------------- |
| `ADR_PROCESS.md` | Process rules, template, lifecycle, ADR-vs-PDR scoping      |
| `README.md`      | Index of all ADRs                                           |
| `NNN-*.md`       | Individual ADR files (`NNN.N-*.md` for point versions)      |

## Conventions

- Sequential numbering; check `README.md` for the next number
- Kebab-case filenames, 3–6 words after the number
- YAML frontmatter with `adr`, `title`, `status`, `created`, `updated`, `authors`,
  `tags` (all required); `parent_adr` required on point versions
- Status lifecycle: `draft → under_review → accepted → modified → deprecated/superseded`
- Commit titles name the record, for example `Add ADR 001: [title]`

## Checklist Before Modifying

- [ ] Read `ADR_PROCESS.md` for the full template and scoping rules
- [ ] Check `README.md` for the current highest number
- [ ] Verify the decision belongs in an ADR, not a PDR or an issue
- [ ] Update `README.md` index when adding or changing status
