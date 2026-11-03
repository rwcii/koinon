package core

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"
	"unicode/utf8"

	"github.com/rwcii/koinon/internal/library"
)

// DecisionRecordFile is the repository's record of the maintainer's verdicts: one file at
// the root of each repository, to which entries are only appended (#536).
const DecisionRecordFile = "DECISIONS.md"

// maxDecisionRecord bounds the record file that RecordDecision reads to find the entries
// that it already holds.
const maxDecisionRecord = 64 << 20

const decisionRecordHeader = `# Decisions

The maintainer's verdicts on the Koinon decision board, appended by Koinon's ` + "`decision_record`" + `
tool. Each entry holds the card text that the maintainer saw when giving the verdict, and the
maintainer's note or feedback, with spelling and typing errors corrected. Entries are only
appended, never changed. Each entry is named by its reference; a link's target is the
reference in lower case.
`

// ErrDecisionRecord refuses a record file that is not a regular file, or that is too large to
// read.
var ErrDecisionRecord = Refusal{"decision_record_invalid", "DECISIONS.md at the checkout root must be a regular file of at most 64 MiB"}

// The record modes of the Decision record fact in the ship skill's facts (#589): none writes
// nothing, short writes the verdict without the card text, and full writes the card text that
// the maintainer saw.
const (
	RecordNone  = "none"
	RecordShort = "short"
	RecordFull  = "full"
)

// shipFacts is the repository's facts file of the ship skill, which holds the Decision record
// fact.
const shipFacts = ".koinon/skills/ship.md"

// ErrDecisionRecordMode refuses a Decision record fact that cannot be read or is not a mode.
var ErrDecisionRecordMode = Refusal{"decision_record_mode_invalid", "the Decision record fact in " + shipFacts + " must start with none, short or full, in a valid facts file; nothing was written"}

// ErrDecisionOtherRepository refuses to record an item in a repository other than the one it
// was created in (#536).
var ErrDecisionOtherRepository = Refusal{"decision_other_repository", "the decision item was created in another repository; record it in a checkout of its author_repository. Nothing was written"}

// ErrDecisionRepositoryUnknown refuses to record an item whose author held no repository
// participant, so its repository is not known (#536).
var ErrDecisionRepositoryUnknown = Refusal{"decision_repository_unknown", "the decision item has no recorded repository: its author held no repository participant, so the tool cannot tell which repository the decision belongs to. Nothing was written"}

// CheckRecordRepository refuses an item that was created in a repository other than
// repository, the common Git directory of the checkout that would record it.
func CheckRecordRepository(item DecisionItem, repository string) error {
	switch item.AuthorRepository {
	case "":
		return ErrDecisionRepositoryUnknown
	case repository:
		return nil
	}
	return ErrDecisionOtherRepository
}

// DecisionRecord tells the reader of a verdict where and how to record it (#536). Entry is the
// current verdict's entry, on a decision_get.
type DecisionRecord struct {
	Path        string `json:"path"`
	Instruction string `json:"instruction"`
	Entry       string `json:"entry,omitempty"`
}

// decisionRecordInstruction asks an agent that reads a verdict to record it as its
// repository's Decision record fact says (#536, #589).
const decisionRecordInstruction = "Call decision_record with this reference in a checkout of the item's repository. It applies the repository's " +
	"Decision record fact (none, short or full; the default is none) and says what goes into the repository."

// decisionRecordNone is what an agent does with a verdict in a repository whose record mode
// is none (#589): the rule goes into the document that holds it.
const decisionRecordNone = "This repository's Decision record fact is none: nothing was written. Put the changed rule into the document that holds it, " +
	"in the pull request that does the work. Give the question, the chosen option, the reason and the maintainer's words with their date, " +
	"with spelling and typing errors corrected and the meaning kept, and name the decision by its card reference. A verdict ID exists only in " +
	"this machine's store and does not go into repository text."

// decisionRecordCommit is what an agent does with the appended entries before it commits them.
const decisionRecordCommit = "Correct the spelling and typing errors in the maintainer's words in each new entry, and keep their meaning and wording; then commit DECISIONS.md with the work. Name a decision by its reference: a verdict ID exists only in this machine's store and does not go into repository text."

// DecisionRecordResult is the outcome of one RecordDecision: the file, and the verdicts that it
// appended, that it already held, and that are not recorded because they are fyi
// acknowledgements or dismissals.
type DecisionRecordResult struct {
	Mode      string  `json:"mode"`
	Path      string  `json:"path,omitempty"`
	Reference string  `json:"reference"`
	Appended  []int64 `json:"appended"`
	Present   []int64 `json:"present"`
	Skipped   []int64 `json:"skipped,omitempty"`
	// Instruction says what to do with the appended entries before the commit.
	Instruction string `json:"instruction,omitempty"`
}

// repoRecordMarker is the line that identifies one verdict's entry. It holds no local verdict
// ID: the reference, revision, verdict and time identify the entry in any clone. Card text is
// quoted line by line, so a marker in it is never a whole line.
func repoRecordMarker(reference string, v DecisionVerdict) string {
	return fmt.Sprintf("<!-- koinon-decision %s revision %d %s %d -->", reference, v.Revision, v.Verdict, v.At)
}

// repoRecordAnchor is the anchor that a link to DECISIONS.md#reference resolves to: the first
// entry of the reference carries it, in lower case, as GitHub writes every anchor.
func repoRecordAnchor(reference string) string {
	return `<a id="` + strings.ToLower(reference) + `"></a>`
}

// DecisionRecordEntry renders one verdict's entry. Card text is Markdown, so each field is a
// block quote, which keeps its headings and markers out of the file's structure. where is the
// item's current text, used only when the verdict's snapshot has none, and marked as such.
func DecisionRecordEntry(reference, where string, v DecisionVerdict) string {
	var b strings.Builder
	fmt.Fprintf(&b, "\n## %s\n\n%s\n\n", reference, repoRecordMarker(reference, v))
	fmt.Fprintf(&b, "**%s**, revision %d, %s.\n\n", verdictWord(v.Verdict), v.Revision,
		time.UnixMilli(v.At).UTC().Format("2006-01-02 15:04:05 UTC"))
	fmt.Fprintf(&b, "- Title: %s\n", oneLine(v.Snapshot.Title))
	switch {
	case v.Snapshot.Where != "":
		fmt.Fprintf(&b, "- Where: %s\n", oneLine(v.Snapshot.Where))
	case where != "":
		fmt.Fprintf(&b, "- Where (the item's text when recorded; the verdict's snapshot has none): %s\n", oneLine(where))
	}
	quoted := func(label, text string) {
		if strings.TrimSpace(text) == "" {
			return
		}
		fmt.Fprintf(&b, "\n%s:\n\n", label)
		for _, line := range strings.Split(strings.TrimRight(text, "\n"), "\n") {
			line = strings.TrimRight(line, "\r")
			if line == "" {
				b.WriteString(">\n")
			} else {
				b.WriteString("> " + line + "\n")
			}
		}
	}
	quoted("Context", v.Snapshot.Context)
	quoted("Recommendation", v.Snapshot.Recommendation)
	if len(v.Snapshot.Commands) > 0 {
		b.WriteString("\nCommands:\n\n")
		for _, c := range v.Snapshot.Commands {
			if !printableCommand(c) {
				c = strconv.Quote(c)
			}
			b.WriteString("    " + c + "\n")
		}
	}
	// Feedback's note is the maintainer's guidance (#537).
	if v.Verdict == "feedback" {
		quoted("Feedback", v.Note)
	} else {
		quoted("Note", v.Note)
	}
	return b.String()
}

// DecisionRecordShortEntry renders one verdict's short entry (#589): the verdict, the title,
// the recommendation's heading and the maintainer's note or feedback, without the card text. The
// heading is the card's recommendation, which the verdict approved, declined or answered; the
// note says what the maintainer chose when it differs.
func DecisionRecordShortEntry(reference string, v DecisionVerdict) string {
	var b strings.Builder
	fmt.Fprintf(&b, "\n## %s\n\n%s\n\n", reference, repoRecordMarker(reference, v))
	fmt.Fprintf(&b, "**%s**, revision %d, %s.\n\n", verdictWord(v.Verdict), v.Revision,
		time.UnixMilli(v.At).UTC().Format("2006-01-02 15:04:05 UTC"))
	fmt.Fprintf(&b, "- Title: %s\n", oneLine(v.Snapshot.Title))
	if heading := recommendationHeading(v.Snapshot.Recommendation); heading != "" {
		fmt.Fprintf(&b, "- Recommendation: %s\n", heading)
	}
	label := "Note"
	if v.Verdict == "feedback" {
		label = "Feedback"
	}
	if strings.TrimSpace(v.Note) != "" {
		fmt.Fprintf(&b, "\n%s:\n\n", label)
		for _, line := range strings.Split(strings.TrimRight(v.Note, "\n"), "\n") {
			line = strings.TrimRight(line, "\r")
			if line == "" {
				b.WriteString(">\n")
			} else {
				b.WriteString("> " + line + "\n")
			}
		}
	}
	return b.String()
}

// recommendationHeading is the recommendation's first non-empty line, without the card form's
// "## Recommendation:" prefix, on one line of at most 300 bytes.
func recommendationHeading(text string) string {
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		line = strings.TrimSpace(strings.TrimPrefix(strings.TrimLeft(line, "# "), "Recommendation:"))
		line = oneLine(line)
		if len(line) > 300 {
			cut := 300
			for cut > 0 && !utf8.RuneStart(line[cut]) {
				cut--
			}
			line = line[:cut] + "..."
		}
		return line
	}
	return ""
}

// DecisionRecordMode reads the Decision record fact of the checkout at root: the first word of
// the fact in shipFacts, parsed with the catalog's schema for skill.ship. The default none
// applies when the file or the fact is absent (#589).
func DecisionRecordMode(root string) (string, error) {
	catalog, err := library.Embedded("")
	if err != nil {
		return "", err
	}
	var schema *library.Facts
	for _, item := range catalog.Items {
		if item.Kind == "skill" && item.Name == "ship" {
			schema = item.Facts
		}
	}
	if schema == nil {
		return "", errors.New("the catalog declares no ship facts")
	}
	mode := ""
	for _, field := range schema.Fields {
		if field.Heading == "Decision record" && field.Default != nil {
			mode = *field.Default
		}
	}
	dir, err := library.OpenReadRoot(root)
	if err != nil {
		return "", err
	}
	defer dir.Close()
	data, err := dir.ReadRegular(shipFacts, library.MaxItemBytes)
	switch {
	case errors.Is(err, os.ErrNotExist):
	case err != nil:
		return "", ErrDecisionRecordMode
	default:
		values, err := library.ParseFactValues(data, "ship", schema)
		if err != nil {
			return "", Refusal{ErrDecisionRecordMode.Code, ErrDecisionRecordMode.Message + ": " + err.Error()}
		}
		mode = values.Fields["Decision record"]
	}
	// The first word names the mode; text after it explains the choice.
	if words := strings.Fields(mode); len(words) > 0 {
		switch word := strings.TrimRight(words[0], ",.;:"); word {
		case RecordNone, RecordShort, RecordFull:
			return word, nil
		}
	}
	return "", ErrDecisionRecordMode
}

func verdictWord(verdict string) string {
	if verdict == "" {
		return verdict
	}
	return strings.ToUpper(verdict[:1]) + verdict[1:]
}

func oneLine(text string) string {
	return strings.Join(strings.Fields(text), " ")
}

// RecordDecision records the item's verdicts in the checkout at root as mode says (#589). In
// mode none it opens no file and says where the rule goes. In modes short and full it appends
// to DECISIONS.md the entries of the verdicts that the file does not hold yet, in verdict
// order; an fyi acknowledgement or dismissal holds no decision and is not recorded.
// It creates the file with its header when it is absent, appends after the text of a file in
// another format, refuses a symbolic link or a file that is not a regular file, and holds an
// exclusive lock on the file while it reads and appends, so that concurrent records add each
// entry once. It writes nothing else and commits nothing.
func RecordDecision(root, mode, reference, where string, verdicts []DecisionVerdict) (DecisionRecordResult, error) {
	if err := checkReference(reference); err != nil {
		return DecisionRecordResult{}, err
	}
	result := DecisionRecordResult{Mode: mode, Reference: reference, Appended: []int64{}, Present: []int64{}}
	switch mode {
	case RecordNone:
		result.Instruction = decisionRecordNone
		return result, nil
	case RecordShort, RecordFull:
	default:
		return DecisionRecordResult{}, ErrDecisionRecordMode
	}
	path := filepath.Join(root, DecisionRecordFile)
	result.Path = path
	flags := os.O_RDWR | os.O_APPEND | syscall.O_NOFOLLOW | syscall.O_NONBLOCK
	f, err := os.OpenFile(path, flags, 0)
	if errors.Is(err, os.ErrNotExist) {
		// An item with no recordable verdict creates no file.
		recordable := false
		for _, v := range verdicts {
			recordable = recordable || recordedVerdict(v.Verdict)
		}
		if !recordable {
			for _, v := range verdicts {
				result.Skipped = append(result.Skipped, v.ID)
			}
			return result, nil
		}
		f, err = os.OpenFile(path, flags|os.O_CREATE, 0o644)
	}
	if err != nil {
		if info, lerr := os.Lstat(path); lerr == nil && !info.Mode().IsRegular() {
			return DecisionRecordResult{}, ErrDecisionRecord
		}
		return DecisionRecordResult{}, err
	}
	defer f.Close()
	if info, err := f.Stat(); err != nil || !info.Mode().IsRegular() {
		return DecisionRecordResult{}, ErrDecisionRecord
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX); err != nil {
		return DecisionRecordResult{}, err
	}
	defer syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
	existing, err := io.ReadAll(io.LimitReader(f, maxDecisionRecord+1))
	if err != nil {
		return DecisionRecordResult{}, err
	}
	if len(existing) > maxDecisionRecord {
		return DecisionRecordResult{}, ErrDecisionRecord
	}
	held := map[string]bool{}
	for _, line := range strings.Split(string(existing), "\n") {
		held[strings.TrimRight(line, "\r")] = true
	}
	var add bytes.Buffer
	if len(existing) == 0 {
		add.WriteString(decisionRecordHeader)
	} else if existing[len(existing)-1] != '\n' {
		add.WriteString("\n")
	}
	for _, v := range verdicts {
		marker := repoRecordMarker(reference, v)
		switch {
		case !recordedVerdict(v.Verdict):
			result.Skipped = append(result.Skipped, v.ID)
		case held[marker]:
			result.Present = append(result.Present, v.ID)
		default:
			if anchor := repoRecordAnchor(reference); !held[anchor] {
				add.WriteString("\n" + anchor + "\n")
				held[anchor] = true
			}
			if mode == RecordShort {
				add.WriteString(DecisionRecordShortEntry(reference, v))
			} else {
				add.WriteString(DecisionRecordEntry(reference, where, v))
			}
			held[marker] = true
			result.Appended = append(result.Appended, v.ID)
		}
	}
	if len(result.Appended) == 0 {
		return result, nil
	}
	if _, err := f.Write(add.Bytes()); err != nil {
		return DecisionRecordResult{}, err
	}
	result.Instruction = decisionRecordCommit
	return result, f.Sync()
}

// CollectDecisionRecord reads an item and every verdict in its history through call, which
// sends one daemon request with the caller already in its body.
func CollectDecisionRecord(ctx context.Context, call func(ctx context.Context, path string, body map[string]any) ([]byte, error), reference string) (DecisionItem, []DecisionVerdict, error) {
	data, err := call(ctx, "/v1/decisions/get", map[string]any{"reference": reference})
	if err != nil {
		return DecisionItem{}, nil, err
	}
	var reply struct {
		Result DecisionItem `json:"result"`
	}
	if err := json.Unmarshal(data, &reply); err != nil {
		return DecisionItem{}, nil, err
	}
	var verdicts []DecisionVerdict
	after := int64(0)
	for {
		data, err := call(ctx, "/v1/decisions/history", map[string]any{"reference": reference, "after": after})
		if err != nil {
			return DecisionItem{}, nil, err
		}
		var page struct {
			Result DecisionHistoryPage `json:"result"`
		}
		if err := json.Unmarshal(data, &page); err != nil {
			return DecisionItem{}, nil, err
		}
		verdicts = append(verdicts, page.Result.Verdicts...)
		if !page.Result.More || page.Result.NextAfter == nil || *page.Result.NextAfter <= after {
			break
		}
		after = *page.Result.NextAfter
	}
	return reply.Result, verdicts, nil
}
