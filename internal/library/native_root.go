package library

import (
	"os"
	"path/filepath"
	"unicode/utf8"
)

// NativeRoot takes only the native client's environment reader. No tool argument or
// daemon HOME supplies this value. Only verified catalog discovery is supported.
func NativeRoot(family string, getenv func(string) string) (string, string) {
	return NativeRootAt(family, getenv, "")
}

// NativeRootAt anchors a relative CODEX_HOME to the registered native directory,
// never an optionally selected worktree. The isolated 0.162.0 probe verifies this.
func NativeRootAt(family string, getenv func(string) string, nativeDirectory string) (string, string) {
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
	if !filepath.IsAbs(root) && nativeDirectory != "" {
		root = filepath.Join(nativeDirectory, root)
	}
	root, err := filepath.Abs(root)
	if err != nil || !utf8.ValidString(root) {
		return "", "native_root_unavailable"
	}
	root, err = filepath.EvalSymlinks(root)
	if err != nil {
		return "", "native_root_unavailable"
	}
	info, err := os.Stat(root)
	if err != nil || !info.IsDir() {
		return "", "native_root_unavailable"
	}
	return root, ""
}

// NativeUserRoot supports the shared HOME/.agents root verified for Codex 0.162.0.
// It is additional collision context, not a different selected adoption destination.
func NativeUserRoot(family string, getenv func(string) string) string {
	return NativeUserRootAt(family, getenv, "")
}

func NativeUserRootAt(family string, getenv func(string) string, nativeDirectory string) string {
	if family != "codex" || getenv == nil || getenv("HOME") == "" {
		return ""
	}
	root := getenv("HOME")
	if !filepath.IsAbs(root) && nativeDirectory != "" {
		root = filepath.Join(nativeDirectory, root)
	}
	root, err := filepath.Abs(root)
	if err != nil || !utf8.ValidString(root) {
		return ""
	}
	root, err = filepath.EvalSymlinks(root)
	if err != nil {
		return ""
	}
	info, err := os.Stat(root)
	if err != nil || !info.IsDir() {
		return ""
	}
	return root
}
