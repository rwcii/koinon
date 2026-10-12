package library

import (
	"path/filepath"
	"unicode/utf8"
)

// NativeRoot takes only the native client's environment reader. No tool argument or
// daemon HOME supplies this value. Only verified catalog discovery is supported.
func NativeRoot(family string, getenv func(string) string) (string, string) {
	if family != "codex" || getenv == nil {
		return "", "native_root_unverified"
	}
	root := getenv("CODEX_HOME")
	if root == "" {
		home := getenv("HOME")
		if home == "" {
			return "", "native_root_unavailable"
		}
		root = filepath.Join(home, ".codex")
	}
	// Relative native homes depend on the process working directory; they are not
	// resolved against a model-selected project directory.
	root, err := filepath.Abs(root)
	if err != nil || !utf8.ValidString(root) {
		return "", "native_root_unavailable"
	}
	root, err = filepath.EvalSymlinks(root)
	if err != nil {
		return "", "native_root_unavailable"
	}
	return root, ""
}
