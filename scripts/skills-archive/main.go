// skills-archive is repository tooling, not an installed Koinon command. It writes
// koinon-skills.tar.gz for a release: every agents/skills/NAME/SKILL.md, as NAME/SKILL.md, with
// fixed headers, so the same source gives the same bytes on every computer (#732; PDR 008,
// Decision 2). scripts/install-skills.sh installs it on a computer with no Koinon.
package main

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"time"
)

func main() {
	if len(os.Args) != 2 {
		fmt.Fprintln(os.Stderr, "usage: go run ./scripts/skills-archive OUT")
		os.Exit(2)
	}
	data, err := archive(".")
	if err == nil {
		err = os.WriteFile(os.Args[1], data, 0o644)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

// archive returns the gzip-compressed tar of root's agents/skills/*/SKILL.md, in name order.
func archive(root string) ([]byte, error) {
	paths, err := filepath.Glob(filepath.Join(root, "agents", "skills", "*", "SKILL.md"))
	if err != nil {
		return nil, err
	}
	if len(paths) == 0 {
		return nil, fmt.Errorf("no agents/skills/*/SKILL.md under %s", root)
	}
	sort.Strings(paths)
	var out bytes.Buffer
	zw, err := gzip.NewWriterLevel(&out, gzip.BestCompression)
	if err != nil {
		return nil, err
	}
	// No file name and no time in the gzip header.
	zw.ModTime = time.Time{}
	tw := tar.NewWriter(zw)
	for _, path := range paths {
		info, err := os.Lstat(path)
		if err != nil {
			return nil, err
		}
		if !info.Mode().IsRegular() {
			return nil, fmt.Errorf("%s is not a regular file", path)
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return nil, err
		}
		header := &tar.Header{Typeflag: tar.TypeReg, Name: filepath.Base(filepath.Dir(path)) + "/SKILL.md",
			Mode: 0o644, Size: int64(len(data)), ModTime: time.Unix(0, 0), Format: tar.FormatUSTAR}
		if err := tw.WriteHeader(header); err != nil {
			return nil, err
		}
		if _, err := io.Copy(tw, bytes.NewReader(data)); err != nil {
			return nil, err
		}
	}
	if err := tw.Close(); err != nil {
		return nil, err
	}
	if err := zw.Close(); err != nil {
		return nil, err
	}
	return out.Bytes(), nil
}
