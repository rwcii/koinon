package core

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"regexp"
	"strings"
	"syscall"
	"time"
	"unicode/utf8"

	"github.com/rwcii/koinon/internal/library"
)

// handoffName is a canonical handoff file name: a UTC timestamp, an optional label and .md.
var handoffName = regexp.MustCompile(`^[0-9]{4}-[0-9]{2}-[0-9]{2}-[0-9]{6}(-[A-Za-z0-9._-]{1,96})?\.md$`)

type artifactFile struct{ dev, ino, size, mtime int64 }

// verifyArtifact checks a cycle's handoff artifact in the main checkout of the requester's
// repository: <main>/<handoff directory>/<name>, a regular file of 1 byte to 1 MiB, modified
// after the current request (after a repeat, the repeated one, #431), ignored by Git, in a
// directory where Git tracks nothing. The handoff directory is the repository's Handoff path
// fact, else _handoff/<agent>/ (handoffDirectory, #468). Each refusal names the check that
// failed.
// It reads metadata through a root opened at the main checkout, and of the files there only
// the bounded handoff facts, so no symlink or `..` leaves the checkout.
func (s *Store) verifyArtifact(ctx context.Context, c cycleRow, name string) (artifactFile, error) {
	if !validArtifactName(name) {
		return artifactFile{}, artifactRefusal("name %q is not a handoff file name, YYYY-MM-DD-HHMMSS[-LABEL].md", name)
	}
	var repository, directory, role string
	if err := s.db.QueryRowContext(ctx, `SELECT repository,directory,role FROM sessions WHERE family=? AND id=?`, c.Family, c.SessionID).
		Scan(&repository, &directory, &role); err != nil {
		return artifactFile{}, artifactRefusal("has no registered session for this cycle")
	}
	main, err := mainCheckout(ctx, directory, repository)
	if err != nil {
		return artifactFile{}, artifactRefusal("has no main checkout for this session's repository")
	}
	root, err := os.OpenRoot(main)
	if err != nil {
		return artifactFile{}, artifactRefusal("has no readable main checkout")
	}
	defer root.Close()
	catalog, err := s.libraryCatalog()
	if err != nil {
		return artifactFile{}, artifactRefusal("cannot be checked: the catalog is unavailable")
	}
	facts, err := library.OpenReadRoot(main)
	if err != nil {
		return artifactFile{}, artifactRefusal("has no readable main checkout")
	}
	dir, err := handoffDirectory(facts, catalog, cycleAgent(c.Family, role))
	facts.Close()
	if err != nil {
		return artifactFile{}, artifactRefusal("directory cannot be read from %s: %v", handoffFacts, err)
	}
	for _, d := range parentDirectories(dir) {
		info, err := root.Lstat(d)
		if err != nil || !info.IsDir() {
			return artifactFile{}, artifactRefusal("must be in %s/ of the main checkout, and %s/ is not a directory there", dir, d)
		}
	}
	relative := filepath.Join(dir, name)
	requested, err := cycleRequestedAt(ctx, s.db, c)
	if err != nil {
		return artifactFile{}, artifactRefusal("cannot be checked: the request time is unavailable")
	}
	info, err := root.Lstat(relative)
	switch {
	case err != nil || !info.Mode().IsRegular():
		return artifactFile{}, artifactRefusal("%s is not a regular file in the main checkout", relative)
	case info.Size() < 1 || info.Size() > maxArtifactBytes:
		return artifactFile{}, artifactRefusal("%s must hold 1 byte to 1 MiB", relative)
	case info.ModTime().UnixMilli() < requested:
		return artifactFile{}, artifactRefusal("%s was last modified before this cycle's request; write a new handoff", relative)
	}
	file := artifactFile{size: info.Size(), mtime: info.ModTime().UnixMilli()}
	if st, ok := info.Sys().(*syscall.Stat_t); ok {
		file.dev, file.ino = int64(st.Dev), int64(st.Ino)
	}
	// The recorded identity is the device, inode, size and modification time: a file
	// replaced under a reused inode, or rewritten in place, changes it.
	if c.Artifact == name && c.ArtifactIno != 0 && (file.dev != c.ArtifactDev || file.ino != c.ArtifactIno ||
		file.size != c.ArtifactSize || file.mtime != c.ArtifactMtime) {
		return artifactFile{}, artifactRefusal("%s changed after the ready report; report ready again", relative)
	}
	if !gitIgnored(ctx, main, relative) {
		return artifactFile{}, artifactRefusal("%s is not ignored by Git", relative)
	}
	if gitTracks(ctx, main, dir) {
		return artifactFile{}, artifactRefusal("directory %s/ holds files that Git tracks", dir)
	}
	return file, nil
}

// artifactRefusal is ErrCycleArtifact with the check that failed (#468). Each argument is cut
// to maxRefusalArgument bytes, so that the text, with the check first, stays within
// MaxRefusalMessage, which a client passes on.
func artifactRefusal(format string, args ...any) error {
	for i, arg := range args {
		args[i] = shortRefusalArgument(fmt.Sprint(arg))
	}
	return Refusal{ErrCycleArtifact.Code, "the handoff artifact " + fmt.Sprintf(format, args...)}
}

const maxRefusalArgument = 240

// shortRefusalArgument cuts text at a character boundary to maxRefusalArgument bytes, marking a
// cut with "...".
func shortRefusalArgument(text string) string {
	text = strings.ToValidUTF8(text, "\uFFFD")
	if len(text) <= maxRefusalArgument {
		return text
	}
	cut := maxRefusalArgument - 3
	for cut > 0 && !utf8.RuneStart(text[cut]) {
		cut--
	}
	return text[:cut] + "..."
}

// handoffFacts is the repository's facts file of the handoff skill.
const handoffFacts = ".koinon/skills/handoff.md"

// cycleAgent is the handoff skill's agent ID of a session: its family, with -ROLE for a
// session with a role.
func cycleAgent(family, role string) string {
	if role != "" {
		return family + "-" + role
	}
	return family
}

// handoffDirectory is the session's handoff directory, relative to the main checkout: the
// first word of the Handoff path fact in handoffFacts, parsed with the catalog's schema for
// skill.handoff, whose default _handoff/<agent>/ applies when the file or the fact is absent.
// <agent> is replaced by agent. The path must be relative and clean, stay in the checkout and
// name <agent>, so each session has its own directory.
func handoffDirectory(root library.ReadRoot, catalog library.Catalog, agent string) (string, error) {
	var schema *library.Facts
	for _, item := range catalog.Items {
		if item.Kind == "skill" && item.Name == "handoff" {
			schema = item.Facts
		}
	}
	if schema == nil {
		return "", errors.New("the catalog declares no handoff facts")
	}
	template := ""
	for _, field := range schema.Fields {
		if field.Heading == "Handoff path" && field.Default != nil {
			template = *field.Default
		}
	}
	// A bounded read of the opened regular file: a link out of the checkout, a swapped FIFO
	// or an oversized file is refused, and none blocks the ready reply.
	data, err := root.ReadRegular(handoffFacts, library.MaxItemBytes)
	switch {
	case errors.Is(err, os.ErrNotExist):
	case err != nil:
		return "", errors.New("not a readable regular file of at most 64 KiB")
	default:
		values, err := library.ParseFactValues(data, "handoff", schema)
		if err != nil {
			return "", err
		}
		template = values.Fields["Handoff path"]
	}
	words := strings.Fields(template)
	if len(words) == 0 || !strings.Contains(words[0], "<agent>") {
		return "", errors.New("Handoff path must start with a directory that names <agent>")
	}
	dir := strings.TrimSuffix(strings.ReplaceAll(words[0], "<agent>", agent), "/")
	if dir == "" || path.IsAbs(dir) || path.Clean(dir) != dir || dir == "." || dir == ".." || strings.HasPrefix(dir, "../") ||
		strings.ContainsAny(dir, "\\<>") {
		return "", fmt.Errorf("Handoff path %q is not a clean directory inside the main checkout", words[0])
	}
	return filepath.FromSlash(dir), nil
}

// parentDirectories lists dir and each directory above it, top first: a/b/c gives a, a/b,
// a/b/c.
func parentDirectories(dir string) []string {
	var dirs []string
	for d := dir; d != "." && d != string(filepath.Separator); d = filepath.Dir(d) {
		dirs = append([]string{d}, dirs...)
	}
	return dirs
}

// mainCheckout returns the main working tree of the registered repository: the first
// worktree that Git lists, with the same common directory. The registered directory must
// itself be a listed worktree, so a copied Git pointer in an unlisted directory is refused.
func mainCheckout(ctx context.Context, directory, repository string) (string, error) {
	base, err := CheckoutResource(ctx, directory)
	if err != nil || repository != "" && base.Repository != repository {
		return "", ErrInvalid
	}
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "git", "-C", base.Directory, "worktree", "list", "--porcelain", "-z")
	cmd.Env = CleanGitEnvironment()
	data, err := cmd.Output()
	if err != nil {
		return "", ErrInvalid
	}
	main, listed := "", false
	for _, record := range bytes.Split(data, []byte{0}) {
		root, ok := bytes.CutPrefix(record, []byte("worktree "))
		if !ok {
			continue
		}
		resolved, err := filepath.EvalSymlinks(string(root))
		if err != nil {
			if main == "" {
				return "", ErrInvalid
			}
			continue
		}
		if main == "" {
			checkout, err := CheckoutResource(ctx, resolved)
			if err != nil || checkout.Repository != base.Repository || checkout.Directory != resolved {
				return "", ErrInvalid
			}
			main = resolved
		}
		listed = listed || resolved == base.Directory
	}
	if main == "" || !listed {
		return "", ErrInvalid
	}
	return main, nil
}

func gitIgnored(ctx context.Context, root, relative string) bool {
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "git", "-C", root, "check-ignore", "-q", "--no-index", "--", relative)
	cmd.Env = CleanGitEnvironment()
	return cmd.Run() == nil
}

func gitTracks(ctx context.Context, root, relative string) bool {
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "git", "-C", root, "ls-files", "-z", "--", relative)
	cmd.Env = CleanGitEnvironment()
	data, err := cmd.Output()
	return err != nil || len(data) > 0
}
