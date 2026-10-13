package library

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestRootedReaderRejectsStaticAndSwappedLeafLinks(t *testing.T) {
	for _, sameFile := range []bool{false, true} {
		t.Run(map[bool]string{false: "different inode", true: "original inode"}[sameFile], func(t *testing.T) {
			root := t.TempDir()
			if err := os.WriteFile(filepath.Join(root, "leaf"), []byte("synthetic declared bytes"), 0600); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(root, "other"), []byte("synthetic other bytes"), 0600); err != nil {
				t.Fatal(err)
			}
			opened, err := os.OpenRoot(root)
			if err != nil {
				t.Fatal(err)
			}
			defer opened.Close()
			r := diskRoot{opened}
			data, err := r.readRegular("leaf", MaxItemBytes, func() {
				if err := os.Rename(filepath.Join(root, "leaf"), filepath.Join(root, "original")); err != nil {
					t.Fatal(err)
				}
				target := "other"
				if sameFile {
					target = "original"
				}
				if err := os.Symlink(target, filepath.Join(root, "leaf")); err != nil {
					t.Fatal(err)
				}
			})
			if !errors.Is(err, errTargetChanged) || len(data) != 0 {
				t.Fatal("swapped leaf link returned file bytes", string(data), err)
			}
			if data, err := r.ReadRegular("leaf", MaxItemBytes); !errors.Is(err, errNotRegular) || len(data) != 0 {
				t.Fatal("static leaf link returned file bytes", string(data), err)
			}
		})
	}
}
