package platform

import (
	"os"
	"path/filepath"
	"strings"
)

// HomebrewOpt returns the formula's stable opt path when source is a koinon binary in a
// Homebrew keg, <P>/Cellar/koinon/<version>/bin/koinon with the keg's INSTALL_RECEIPT.json,
// and <P>/opt/koinon resolves into <P>/Cellar/koinon; otherwise "". Each brew upgrade moves
// the opt link to the new keg (#397).
func HomebrewOpt(source string) string {
	file, err := filepath.EvalSymlinks(source)
	if err != nil {
		return ""
	}
	bin := filepath.Dir(file)
	keg := filepath.Dir(bin)
	kegs := filepath.Dir(keg)
	cellar := filepath.Dir(kegs)
	if filepath.Base(file) != "koinon" || filepath.Base(bin) != "bin" || filepath.Base(kegs) != "koinon" || filepath.Base(cellar) != "Cellar" {
		return ""
	}
	if info, err := os.Stat(filepath.Join(keg, "INSTALL_RECEIPT.json")); err != nil || !info.Mode().IsRegular() {
		return ""
	}
	opt := filepath.Join(filepath.Dir(cellar), "opt", "koinon", "bin", "koinon")
	resolved, err := filepath.EvalSymlinks(opt)
	if err != nil || !strings.HasPrefix(resolved, kegs+string(filepath.Separator)) {
		return ""
	}
	if info, err := os.Stat(resolved); err != nil || !info.Mode().IsRegular() {
		return ""
	}
	return opt
}
