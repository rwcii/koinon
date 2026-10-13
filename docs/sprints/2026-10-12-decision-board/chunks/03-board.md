# Chunk 03 — the board

Depends on chunk 02. Cites `decision.md` (criteria 1, 5, 6, 7, 12) and
`definition-of-done.md` (Content, Board and agent rows).

## Markdown

A fixed Markdown subset renderer in Go with no new dependency: paragraphs, line breaks,
ordered and unordered lists, emphasis, inline code and fenced code blocks. It produces
escaped HTML for `html/template`. Raw HTML, links and images render as their literal text.
It is used for context, recommendation, agent text and verdict notes.

## Board view

The Decisions view becomes the designed board:

- Counts (waiting, open fyi, decided), then cards: `ACTION FOR YOU` fyi items first, then
  item order and reference. Each card shows the kind, status, where, Markdown text, the
  recipients with any resolution flag, terminal-copy commands in separate boxes with a copy
  action, the optional note field and the actions for its kind. A hidden command (one that
  fails the ASCII rule on read) is named, and Approve is disabled.
- The archive section and a read-only lookup by reference show verdict history and
  snapshots.
- Actions are disabled while a request runs, and errors show inline. The existing fragment
  refresh pauses while a field has focus, as on the other views.

## Agent rows

A table above the cards, from daemon data only: public name, activity with its reason,
current claimed work (native and participant claims, on the current holder), a context meter
or "not reported", and the waiting references that name the agent. No native IDs, paths or
wake targets appear.

## Style

Maximum width 1180 pixels, system fonts, system light and dark colors, and horizontal scroll
for the agent table on narrow screens. Only embedded assets under the existing CSP.

## Docs

USAGE (the board), the guide's Decisions section, CHANGELOG. #206 closes after this chunk
and its evidence.
