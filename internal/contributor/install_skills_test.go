package contributor

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/rwcii/koinon/internal/core"
)

// skillsEntry is one archive entry for a synthetic release.
type skillsEntry struct {
	name, body, link string
	dir              bool
}

func skillsArchive(t *testing.T, entries []skillsEntry) []byte {
	t.Helper()
	var out bytes.Buffer
	zw := gzip.NewWriter(&out)
	tw := tar.NewWriter(zw)
	for _, e := range entries {
		h := &tar.Header{Name: e.name, Mode: 0o644, Typeflag: tar.TypeReg, Size: int64(len(e.body))}
		switch {
		case e.link != "":
			h.Typeflag, h.Linkname, h.Size = tar.TypeSymlink, e.link, 0
		case e.dir:
			h.Typeflag, h.Mode, h.Size = tar.TypeDir, 0o755, 0
		}
		if err := tw.WriteHeader(h); err != nil {
			t.Fatal(err)
		}
		if h.Size > 0 {
			if _, err := tw.Write([]byte(e.body)); err != nil {
				t.Fatal(err)
			}
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return out.Bytes()
}

func skillBody(name string) string {
	return "---\nname: " + name + "\ndescription: \"A synthetic skill.\"\n---\n\n# " + name + "\n\nSteps.\n"
}

// release writes a synthetic release folder with archive and its SHA256SUMS line (or sums,
// when given) and returns it.
func release(t *testing.T, archive []byte, sums string) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "koinon-skills.tar.gz"), archive, 0o644); err != nil {
		t.Fatal(err)
	}
	if sums == "" {
		sum := sha256.Sum256(archive)
		sums = "0000000000000000000000000000000000000000000000000000000000000000  koinon-linux-amd64\n" +
			hex.EncodeToString(sum[:]) + "  koinon-skills.tar.gz\n"
	}
	if err := os.WriteFile(filepath.Join(dir, "SHA256SUMS"), []byte(sums), 0o644); err != nil {
		t.Fatal(err)
	}
	return dir
}

// installSkills runs scripts/install-skills.sh against the release folder with home as HOME
// and the extra environment; it returns the output and whether the script succeeded.
func installSkills(t *testing.T, rel, home string, env ...string) (string, bool) {
	t.Helper()
	cmd := exec.Command("sh", filepath.Join(root(t), "scripts/install-skills.sh"), "v9.9.9")
	cmd.Env = append([]string{"PATH=" + os.Getenv("PATH"), "HOME=" + home, "KOINON_SKILLS_BASE=file://" + rel}, env...)
	out, err := cmd.CombinedOutput()
	return string(out), err == nil
}

// files lists every regular file under dir, relative to it.
func files(t *testing.T, dir string) []string {
	t.Helper()
	var found []string
	filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
		if err == nil && !info.IsDir() {
			rel, _ := filepath.Rel(dir, path)
			found = append(found, rel)
		}
		return nil
	})
	sort.Strings(found)
	return found
}

func needTools(t *testing.T) {
	for _, tool := range []string{"curl", "tar"} {
		if _, err := exec.LookPath(tool); err != nil {
			t.Skip(tool + " is not installed")
		}
	}
}

// The release archive and its install on a computer with no Koinon (#732; PDR 008, Decision 2).
func TestInstallSkillsFromRelease(t *testing.T) {
	needTools(t)
	// The real archive: every canonical skill, reproducible.
	build := func(out string) []byte {
		cmd := exec.Command("go", "run", "./scripts/skills-archive", out)
		cmd.Dir = root(t)
		if b, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("skills-archive: %v\n%s", err, b)
		}
		data, err := os.ReadFile(out)
		if err != nil {
			t.Fatal(err)
		}
		return data
	}
	tmp := t.TempDir()
	archive := build(filepath.Join(tmp, "a.tar.gz"))
	if again := build(filepath.Join(tmp, "b.tar.gz")); !bytes.Equal(archive, again) {
		t.Fatal("the skills archive is not reproducible")
	}
	canonical, err := filepath.Glob(filepath.Join(root(t), "agents/skills/*/SKILL.md"))
	if err != nil || len(canonical) < 20 {
		t.Fatalf("canonical skills: %v %v", canonical, err)
	}
	rel := release(t, archive, "")
	home := t.TempDir()
	for _, dir := range []string{".config/opencode", "grok"} {
		if err := os.MkdirAll(filepath.Join(home, dir), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	env := []string{"GROK_HOME=" + filepath.Join(home, "grok")}
	out, ok := installSkills(t, rel, home, env...)
	if !ok {
		t.Fatalf("install failed:\n%s", out)
	}
	roots := map[string]string{"claude": ".claude/skills", "codex": ".agents/skills", "opencode": ".config/opencode/skills", "grok": "grok/skills"}
	for family, sub := range roots {
		for _, path := range canonical {
			name := filepath.Base(filepath.Dir(path))
			entry := "koinon-" + strings.TrimPrefix(name, "koinon-")
			target := filepath.Join(home, sub, entry, "SKILL.md")
			got, err := os.ReadFile(target)
			if name == "koinon-guide" {
				if err == nil {
					t.Errorf("%s: koinon-guide was installed", family)
				}
				continue
			}
			src, _ := os.ReadFile(path)
			want := strings.Replace(string(src), "\nname: "+name+"\n", "\nname: "+entry+"\n", 1)
			if err != nil || string(got) != want {
				t.Errorf("%s %s: %v; not the canonical text with only its name line changed", family, entry, err)
			}
			if !strings.Contains(out, "created   "+family+" "+target) {
				t.Errorf("no created line for %s", target)
			}
		}
	}
	if _, err := os.Stat(filepath.Join(home, ".gemini")); !os.IsNotExist(err) {
		t.Errorf("agy's folder did not exist, but the script made one: %v", err)
	}
	out, ok = installSkills(t, rel, home, env...)
	if !ok || strings.Contains(out, "created") || strings.Contains(out, "replaced") || strings.Count(out, "unchanged") != 4*(len(canonical)-1) {
		t.Fatalf("second run:\n%s", out)
	}
	// koinon install then replaces each copy with its own entry.
	skills, err := core.TeamSkills()
	if err != nil {
		t.Fatal(err)
	}
	for _, skill := range skills {
		path := filepath.Join(home, ".claude/skills", skill.Name, "SKILL.md")
		if skill.Name == "koinon-guide" {
			continue
		}
		result, err := core.InstallTeamSkill(path, skill)
		got, _ := os.ReadFile(path)
		if err != nil || result != "replaced" || !bytes.Equal(got, skill.Content) {
			t.Errorf("%s: koinon install gave %q, %v", skill.Name, result, err)
		}
	}
	// The formula ignores the archive's line in SHA256SUMS.
	sums, err := os.ReadFile(filepath.Join(rel, "SHA256SUMS"))
	if err != nil {
		t.Fatal(err)
	}
	binaries := "1111111111111111111111111111111111111111111111111111111111111111  koinon-darwin-arm64\n" +
		"2222222222222222222222222222222222222222222222222222222222222222  koinon-darwin-amd64\n" +
		"3333333333333333333333333333333333333333333333333333333333333333  koinon-linux-arm64\n" +
		"4444444444444444444444444444444444444444444444444444444444444444  koinon-linux-amd64\n"
	formula := func(sums string) string {
		path := filepath.Join(t.TempDir(), "SHA256SUMS")
		if err := os.WriteFile(path, []byte(sums), 0o644); err != nil {
			t.Fatal(err)
		}
		b, err := exec.Command("bash", filepath.Join(root(t), "scripts/homebrew-formula.sh"), "v9.9.9", path).CombinedOutput()
		if err != nil {
			t.Fatalf("formula: %v\n%s", err, b)
		}
		return string(b)
	}
	archiveLine := string(sums[strings.Index(string(sums), "\n")+1:])
	if formula(binaries) != formula(binaries+archiveLine) {
		t.Error("the formula changes with the archive's line in SHA256SUMS")
	}
}

// A release whose archive or checksum is wrong writes nothing.
func TestInstallSkillsRefusesABadRelease(t *testing.T) {
	needTools(t)
	good := []skillsEntry{{name: "alpha/SKILL.md", body: skillBody("alpha")}, {name: "koinon-extra/SKILL.md", body: skillBody("koinon-extra")}}
	archive := skillsArchive(t, good)
	sum := sha256.Sum256(archive)
	hash := hex.EncodeToString(sum[:])
	cases := map[string]string{}
	for name, entries := range map[string][]skillsEntry{
		"absolute path":       append(append([]skillsEntry{}, good...), skillsEntry{name: "/abs/SKILL.md", body: skillBody("abs")}),
		"parent path":         append(append([]skillsEntry{}, good...), skillsEntry{name: "../up/SKILL.md", body: skillBody("up")}),
		"symbolic link":       append(append([]skillsEntry{}, good...), skillsEntry{name: "lnk/SKILL.md", link: "/etc/hosts"}),
		"other file":          append(append([]skillsEntry{}, good...), skillsEntry{name: "alpha/run.sh", body: "echo\n"}),
		"upper-case name":     append(append([]skillsEntry{}, good...), skillsEntry{name: "Beta/SKILL.md", body: skillBody("Beta")}),
		"nested folder":       append(append([]skillsEntry{}, good...), skillsEntry{name: "alpha/deep/", dir: true}),
		"name line elsewhere": append(append([]skillsEntry{}, good...), skillsEntry{name: "gamma/SKILL.md", body: skillBody("delta")}),
	} {
		a := skillsArchive(t, entries)
		s := sha256.Sum256(a)
		cases[name] = release(t, a, hex.EncodeToString(s[:])+"  koinon-skills.tar.gz\n")
	}
	cases["wrong hash"] = release(t, archive, strings.Repeat("0", 64)+"  koinon-skills.tar.gz\n")
	cases["no archive line"] = release(t, archive, hash+"  koinon-linux-amd64\n")
	cases["two archive lines"] = release(t, archive, hash+"  koinon-skills.tar.gz\n"+hash+"  *koinon-skills.tar.gz\n")
	cases["malformed hash"] = release(t, archive, strings.ToUpper(hash)+"  koinon-skills.tar.gz\n")
	for name, rel := range cases {
		t.Run(name, func(t *testing.T) {
			home := t.TempDir()
			out, ok := installSkills(t, rel, home)
			if ok || len(files(t, home)) != 0 {
				t.Fatalf("succeeded %v, wrote %q:\n%s", ok, files(t, home), out)
			}
		})
	}
	// The good archive installs, a koinon- name keeps its name, and a bad version is refused.
	rel := release(t, archive, "")
	home := t.TempDir()
	if out, ok := installSkills(t, rel, home); !ok {
		t.Fatalf("good release failed:\n%s", out)
	}
	if got := files(t, filepath.Join(home, ".claude/skills")); strings.Join(got, " ") != filepath.Join("koinon-alpha", "SKILL.md")+" "+filepath.Join("koinon-extra", "SKILL.md") {
		t.Fatalf("installed %q", got)
	}
	extra, _ := os.ReadFile(filepath.Join(home, ".agents/skills/koinon-extra/SKILL.md"))
	if string(extra) != skillBody("koinon-extra") {
		t.Fatalf("koinon-extra changed: %q", extra)
	}
	cmd := exec.Command("sh", filepath.Join(root(t), "scripts/install-skills.sh"), "latest")
	cmd.Env = []string{"PATH=" + os.Getenv("PATH"), "HOME=" + t.TempDir(), "KOINON_SKILLS_BASE=file://" + rel}
	if out, err := cmd.CombinedOutput(); err == nil {
		t.Fatalf("version latest was accepted:\n%s", out)
	}
}

// A symbolic link at the entry file, the entry folder, the skills folder or the configuration
// folder is kept; a linked folder above the configuration folder is written through.
func TestInstallSkillsKeepsLinks(t *testing.T) {
	needTools(t)
	archive := skillsArchive(t, []skillsEntry{{name: "alpha/SKILL.md", body: skillBody("alpha")}})
	rel := release(t, archive, "")
	link := func(t *testing.T, target, path string) {
		t.Helper()
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(target, path); err != nil {
			t.Fatal(err)
		}
	}
	for _, c := range []struct {
		name  string
		setup func(t *testing.T, home, elsewhere string) []string
		kept  bool
	}{
		{"entry file", func(t *testing.T, home, elsewhere string) []string {
			link(t, filepath.Join(elsewhere, "mine.md"), filepath.Join(home, ".claude/skills/koinon-alpha/SKILL.md"))
			return nil
		}, true},
		{"entry folder", func(t *testing.T, home, elsewhere string) []string {
			link(t, elsewhere, filepath.Join(home, ".claude/skills/koinon-alpha"))
			return nil
		}, true},
		{"skills folder", func(t *testing.T, home, elsewhere string) []string {
			link(t, elsewhere, filepath.Join(home, ".claude/skills"))
			return nil
		}, true},
		{"configuration folder", func(t *testing.T, home, elsewhere string) []string {
			link(t, elsewhere, filepath.Join(home, "linked-claude"))
			return []string{"CLAUDE_CONFIG_DIR=" + filepath.Join(home, "linked-claude")}
		}, true},
		{"folder above the configuration folder", func(t *testing.T, home, elsewhere string) []string {
			link(t, elsewhere, filepath.Join(home, "dots"))
			return []string{"CLAUDE_CONFIG_DIR=" + filepath.Join(home, "dots", "claude")}
		}, false},
	} {
		t.Run(c.name, func(t *testing.T) {
			home, elsewhere := t.TempDir(), t.TempDir()
			mine := filepath.Join(elsewhere, "mine.md")
			if err := os.WriteFile(mine, []byte("mine\n"), 0o644); err != nil {
				t.Fatal(err)
			}
			env := c.setup(t, home, elsewhere)
			out, ok := installSkills(t, rel, home, env...)
			if !ok {
				t.Fatalf("install failed:\n%s", out)
			}
			kept := strings.Contains(out, "kept      claude ")
			if kept != c.kept {
				t.Fatalf("kept %v, want %v:\n%s", kept, c.kept, out)
			}
			if c.kept {
				if got := files(t, elsewhere); strings.Join(got, " ") != "mine.md" {
					t.Fatalf("wrote through the link: %q", got)
				}
			} else if _, err := os.Stat(filepath.Join(elsewhere, "claude/skills/koinon-alpha/SKILL.md")); err != nil {
				t.Fatalf("not written through the higher link: %v\n%s", err, out)
			}
			if b, _ := os.ReadFile(mine); string(b) != "mine\n" {
				t.Fatalf("the link target changed: %q", b)
			}
			if !strings.Contains(out, "created   codex ") {
				t.Fatalf("codex was not installed:\n%s", out)
			}
		})
	}
}

// A write that fails ends the script with a failure.
func TestInstallSkillsReportsAWriteFailure(t *testing.T) {
	needTools(t)
	if os.Geteuid() == 0 {
		t.Skip("root can write a read-only folder")
	}
	rel := release(t, skillsArchive(t, []skillsEntry{{name: "alpha/SKILL.md", body: skillBody("alpha")}}), "")
	home := t.TempDir()
	skills := filepath.Join(home, ".agents", "skills")
	if err := os.MkdirAll(skills, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(skills, 0o500); err != nil {
		t.Fatal(err)
	}
	defer os.Chmod(skills, 0o700)
	if out, ok := installSkills(t, rel, home); ok || strings.Contains(out, "codex "+filepath.Join(skills, "koinon-alpha")) {
		t.Fatalf("a failed write was reported as done:\n%s", out)
	}
}

// A destination that is not a folder or a regular file stops the script before it writes any
// root, and the script does not report it as replaced.
func TestInstallSkillsRefusesAnOddDestination(t *testing.T) {
	needTools(t)
	rel := release(t, skillsArchive(t, []skillsEntry{{name: "alpha/SKILL.md", body: skillBody("alpha")}}), "")
	for _, c := range []struct {
		name string
		path string // relative to HOME
		dir  bool   // a folder with a sentinel file, else a regular file
	}{
		{"folder at the entry file", ".agents/skills/koinon-alpha/SKILL.md", true},
		{"file at the entry folder", ".agents/skills/koinon-alpha", false},
		{"file at the skills folder", ".agents/skills", false},
		{"file at the configuration folder", ".agents", false},
	} {
		t.Run(c.name, func(t *testing.T) {
			home := t.TempDir()
			path := filepath.Join(home, c.path)
			sentinel := path
			if c.dir {
				sentinel = filepath.Join(path, "existing")
			}
			if err := os.MkdirAll(filepath.Dir(sentinel), 0o700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(sentinel, []byte("existing\n"), 0o644); err != nil {
				t.Fatal(err)
			}
			out, ok := installSkills(t, rel, home)
			if ok || strings.Contains(out, "replaced") || strings.Contains(out, "created") || !strings.Contains(out, path+" is not a") {
				t.Fatalf("succeeded %v:\n%s", ok, out)
			}
			if got := files(t, home); strings.Join(got, " ") != strings.TrimPrefix(sentinel, home+string(filepath.Separator)) {
				t.Fatalf("wrote %q", got)
			}
			if b, _ := os.ReadFile(sentinel); string(b) != "existing\n" {
				t.Fatalf("the destination changed: %q", b)
			}
		})
	}
}

// Each hash tool gives the same install: sha256sum where it exists, else shasum. PATH holds
// only the tools that the script uses, with one of the two hash tools.
func TestInstallSkillsHashTools(t *testing.T) {
	needTools(t)
	rel := release(t, skillsArchive(t, []skillsEntry{{name: "alpha/SKILL.md", body: skillBody("alpha")}}), "")
	tools := []string{"sh", "curl", "awk", "sed", "tar", "gzip", "cut", "sort", "mkdir", "mktemp", "rm", "cp", "chmod", "mv", "cmp", "basename"}
	for _, hash := range []string{"sha256sum", "shasum"} {
		t.Run(hash, func(t *testing.T) {
			bin := t.TempDir()
			need := append(append([]string{}, tools...), hash)
			if hash == "shasum" {
				need = append(need, "perl") // shasum is a Perl program
			}
			for _, tool := range need {
				path, err := exec.LookPath(tool)
				if err != nil {
					t.Skip(tool + " is not installed")
				}
				if err := os.Symlink(path, filepath.Join(bin, tool)); err != nil {
					t.Fatal(err)
				}
			}
			home := t.TempDir()
			cmd := exec.Command(filepath.Join(bin, "sh"), filepath.Join(root(t), "scripts/install-skills.sh"), "v9.9.9")
			cmd.Env = []string{"PATH=" + bin, "HOME=" + home, "KOINON_SKILLS_BASE=file://" + rel}
			out, err := cmd.CombinedOutput()
			if err != nil || !strings.Contains(string(out), "created   codex ") {
				t.Fatalf("%v:\n%s", err, out)
			}
			// A wrong hash fails with this tool too.
			bad := release(t, skillsArchive(t, []skillsEntry{{name: "alpha/SKILL.md", body: skillBody("alpha")}}), strings.Repeat("0", 64)+"  koinon-skills.tar.gz\n")
			cmd = exec.Command(filepath.Join(bin, "sh"), filepath.Join(root(t), "scripts/install-skills.sh"), "v9.9.9")
			cmd.Env = []string{"PATH=" + bin, "HOME=" + t.TempDir(), "KOINON_SKILLS_BASE=file://" + bad}
			if out, err := cmd.CombinedOutput(); err == nil || !strings.Contains(string(out), "does not match SHA256SUMS") {
				t.Fatalf("a wrong hash passed with %s:\n%s", hash, out)
			}
		})
	}
}
