package core

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"syscall"
	"time"
)

// handoffName is a canonical handoff file name: a UTC timestamp, an optional label and .md.
var handoffName = regexp.MustCompile(`^[0-9]{4}-[0-9]{2}-[0-9]{2}-[0-9]{6}(-[A-Za-z0-9._-]{1,96})?\.md$`)

type artifactFile struct{ dev, ino, size, mtime int64 }

// verifyArtifact checks a cycle's handoff artifact in the main checkout of the requester's
// repository: <main>/_handoff/<family[-role]>/<name>, a regular file of 1 byte to 1 MiB,
// modified after the request, ignored by Git, in a directory where Git tracks nothing.
// It reads metadata through a root opened at the main checkout, never the file's content,
// so no symlink or `..` leaves the checkout.
func (s *Store) verifyArtifact(ctx context.Context, c cycleRow, name string) (artifactFile, error) {
	if !validArtifactName(name) {
		return artifactFile{}, ErrCycleArtifact
	}
	var repository, directory, role string
	if err := s.db.QueryRowContext(ctx, `SELECT repository,directory,role FROM sessions WHERE family=? AND id=?`, c.Family, c.SessionID).
		Scan(&repository, &directory, &role); err != nil {
		return artifactFile{}, ErrCycleArtifact
	}
	main, err := mainCheckout(ctx, directory, repository)
	if err != nil {
		return artifactFile{}, ErrCycleArtifact
	}
	agent := c.Family
	if role != "" {
		agent += "-" + role
	}
	relative := filepath.Join("_handoff", agent, name)
	root, err := os.OpenRoot(main)
	if err != nil {
		return artifactFile{}, ErrCycleArtifact
	}
	defer root.Close()
	for _, dir := range []string{"_handoff", filepath.Join("_handoff", agent)} {
		info, err := root.Lstat(dir)
		if err != nil || !info.IsDir() {
			return artifactFile{}, ErrCycleArtifact
		}
	}
	info, err := root.Lstat(relative)
	if err != nil || !info.Mode().IsRegular() || info.Size() < 1 || info.Size() > maxArtifactBytes ||
		info.ModTime().UnixMilli() < c.CreatedAt {
		return artifactFile{}, ErrCycleArtifact
	}
	file := artifactFile{size: info.Size(), mtime: info.ModTime().UnixMilli()}
	if st, ok := info.Sys().(*syscall.Stat_t); ok {
		file.dev, file.ino = int64(st.Dev), int64(st.Ino)
	}
	if c.Artifact == name && c.ArtifactIno != 0 && (file.dev != c.ArtifactDev || file.ino != c.ArtifactIno) {
		return artifactFile{}, ErrCycleArtifact // replaced since ready: a new ready check is needed
	}
	if !gitIgnored(ctx, main, relative) || gitTracks(ctx, main, filepath.Join("_handoff", agent)) {
		return artifactFile{}, ErrCycleArtifact
	}
	return file, nil
}

// mainCheckout returns the main working tree of the registered repository: the first
// worktree that Git lists, with the same common directory. A copied Git pointer cannot
// name an unlisted root.
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
	for _, record := range bytes.Split(data, []byte{0}) {
		root, ok := bytes.CutPrefix(record, []byte("worktree "))
		if !ok {
			continue
		}
		resolved, err := filepath.EvalSymlinks(string(root))
		if err != nil {
			return "", ErrInvalid
		}
		main, err := CheckoutResource(ctx, resolved)
		if err != nil || main.Repository != base.Repository || main.Directory != resolved {
			return "", ErrInvalid
		}
		return resolved, nil
	}
	return "", ErrInvalid
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
