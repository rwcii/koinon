# Synthetic legacy import fixtures

These committed trees are independent inputs to the Go import and native upgrade tests.
They were generated once using the pinned previous main release
`3d60c73c88f3110ca4bc91a9a0cc7e16f7daf827`, obtained through Git history, so the database
schemas and recorded state come from that release rather than hand-written approximations.
All peers, messages, repositories and sessions are synthetic.

The fixture generator was retired with chunk 12. Its source remains in Git at
`f65ad6e27c00efe20bc714c7c9adee575a8be896:scripts/make-import-fixtures.py`; reproducing fixture
provenance requires an isolated historical checkout. No current test or native runner calls
that generator or imports the deleted checkout package. `fixture.json` describes expected
records, cursors and retained claims. Tests copy these trees to temporary directories and
adapt synthetic repository paths there, leaving committed inputs unchanged.

The native upgrade job obtains the previous release's installer/uninstaller separately with
`git archive` and uses Python only for that baseline. It keeps full Git history and reads
these committed trees; its fresh Go installation and lifecycle need no interpreter.
