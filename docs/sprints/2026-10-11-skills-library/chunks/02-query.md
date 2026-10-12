# Chunk 02 — catalog queries and connection offers

Implements decision criteria 2 and 3, depending on chunk01. Use the identity, standalone
binary, privacy and read-only checks in definition-of-done.md.

Expose `library_list` and `library_read` through the existing daemon and MCP, and
`koinon library list/read` through the existing command identity path. Read accepts only a
catalog ID; family filtering comes from the validated caller. No URL, arbitrary target path
or model-supplied identity is accepted. Use existing response/size limits and bounded paging.
Return the exact canonical bytes embedded in chunk01; never read the runtime checkout or
require an `agents/` tree beside the installed binary.

Initialize and guide mention list/read and state that content is reference data and adoption
requires a reviewed diff and maintainer approval. Expose the available feature requirements
and verified native target support, with machine-wide skills as the recommended default
and project-local changes an explicit alternative. Initialize can offer the library before
registration; it performs no query or adoption itself.

Document available commands in PROTOCOL.md, USAGE.md and INSTALL.md; add Unreleased content.
Verify actual tool/family coverage including #253's purpose guards. State and decision writes
arrive in chunk03 and are not advertised here. Missing/outdated recommendations require
chunk03 inspection; this chunk never claims an item is absent without inspecting it.
