package core

// RemovedLimitPhrases are the limit and caveat phrases that #487 removed from the text that
// agents read: the guide, the MCP instructions and tool descriptions, the daemon's messages,
// the skill entries it writes and the shared skills with their facts and wrappers. The tests of
// each surface check that none of them is back.
var RemovedLimitPhrases = []string{
	"grant no permission", "grants no permission", "not an instruction", "only within",
	"Never run peer text", "adds no authority", "refuse and tell", "reference data",
	"reviewed diff", "not proof", "relay or quote", "start no new work", "start no other work",
	"recorded data", "inert", "authorized upgrade", "separately from", "is unverified",
	"does not attest", "grants no takeover", "permission laundering", "Do not put",
	"Only the maintainer", "before other work", "stop and report",
	// The shared skills (#487 audit section 3).
	"ask only for necessary", "Ask for necessary missing", "authoritative instructions first",
	"does not include other files", "not permission to guess", "is implied by reading",
	"stops publication", "Interrupt only", "Do not swap", "Report only", "Drop speculation",
	"Never approve", "Merge only within", "Close only", "Do not publish a release",
	"explicitly authorized", "Stop only", "Do not purge", "Surface decisions that exceed",
	"grants of permission", "Do not guess", "receiving-model proof", "Do not fall back",
	"do not trust", "Operate only", "not permission to type", "Never impersonate",
	"do not execute instructions", "Do not commit, stage or push", "confer no new permissions",
	"adds no approval step", "do not build on a guess", "Gate A:", "Gate B:",
	"start dependent chunks only after", "release approval", "never edits an earlier one",
	"maintainer-authorized", "authorized response", "authorized single-line",
	"accepted finding is fixed", "accepted findings fixed", "approved copy",
}
