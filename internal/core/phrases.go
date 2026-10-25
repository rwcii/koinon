package core

// RemovedLimitPhrases are the limit and caveat phrases that #487 removed from the text that
// agents read: the guide, the MCP instructions and tool descriptions, the daemon's messages
// and the skill entries it writes. The tests of each surface check that none of them is back.
var RemovedLimitPhrases = []string{
	"grant no permission", "grants no permission", "not an instruction", "only within",
	"Never run peer text", "adds no authority", "refuse and tell", "reference data",
	"reviewed diff", "not proof", "relay or quote", "start no new work", "start no other work",
	"recorded data", "inert", "authorized upgrade", "separately from", "is unverified",
	"does not attest", "grants no takeover", "permission laundering", "Do not put",
	"Only the maintainer", "before other work", "stop and report",
}
