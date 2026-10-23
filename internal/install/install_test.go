package install

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/rwcii/koinon/internal/core"
	"github.com/rwcii/koinon/internal/platform"
	"github.com/rwcii/koinon/internal/setup"
)

// recorder is a patched service manager: it logs every command and answers with the
// outcome its fail function chooses. No test reaches a real manager.
type recorder struct {
	calls   []string
	fail    func(argv []string) bool
	running bool
}

func (r *recorder) run(_ context.Context, argv ...string) ([]byte, error) {
	r.calls = append(r.calls, strings.Join(argv, " "))
	if r.fail != nil && r.fail(argv) {
		return []byte("refused"), errors.New("exit 1")
	}
	command := strings.Join(argv, " ")
	switch {
	case strings.Contains(command, "restart") || strings.Contains(command, "bootstrap"):
		r.running = true
	case strings.Contains(command, "disable --now") || strings.Contains(command, "bootout"):
		r.running = false
	case strings.HasPrefix(command, "launchctl print gui/501/"):
		if !r.running {
			return []byte("Could not find service"), errors.New("exit status 113")
		}
	case strings.Contains(command, "is-active"):
		if !r.running {
			return []byte("inactive"), errors.New("exit 3")
		}
	}
	return nil, nil
}

type env struct {
	o        Options
	r        *recorder
	setups   []string
	removals []string
	adopts   []string
	// adopted lists the families whose fake earlier setup Adopt finds.
	adopted map[string]bool
}

func newEnv(t *testing.T, backend string) *env {
	t.Helper()
	dir := t.TempDir()
	source := filepath.Join(dir, "koinon-download")
	if err := os.WriteFile(source, []byte("synthetic binary"), 0700); err != nil {
		t.Fatal(err)
	}
	state, err := platform.PrivateDir(filepath.Join(dir, "state"))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(state, "secret"), []byte(strings.Repeat("ab", 32)), 0600); err != nil {
		t.Fatal(err)
	}
	e := &env{r: &recorder{}}
	e.o = Options{Prefix: filepath.Join(dir, "prefix"), StateDir: state, PythonPrefix: filepath.Join(dir, "python"), Source: source,
		Path: filepath.Join(dir, "empty-path"), ClaudeConfig: filepath.Join(dir, "claude-config"),
		Opencode: filepath.Join(dir, "opencode-config"), AgyRoot: filepath.Join(dir, "agy-config"),
		Services: platform.Services{Backend: backend, Home: filepath.Join(dir, "home"), UID: 501, Run: e.r.run,
			User: "alice", Group: "staff", SystemDir: filepath.Join(dir, "LaunchDaemons")},
		Setup: func(_ context.Context, o setup.Options) (setup.Report, error) {
			e.setups = append(e.setups, o.Family+" "+o.Binary)
			return setup.Report{OK: true, Family: o.Family}, nil
		},
		Adopt: func(_ context.Context, o setup.Options) (setup.Report, bool, error) {
			e.adopts = append(e.adopts, o.Family+" "+o.Binary+" "+o.StateDir)
			return setup.Report{OK: true, Family: o.Family, Changed: []string{o.Family + " CLI in launchers.json"}}, e.adopted[o.Family], nil
		},
		Remove: func(_ context.Context, o setup.Options) (setup.Report, error) {
			e.removals = append(e.removals, o.Family+" "+o.Binary)
			return setup.Report{OK: true, Family: o.Family}, nil
		},
		Status: func(context.Context, string, string) (json.RawMessage, error) { return json.RawMessage(`{}`), nil },
	}
	return e
}

// pickupResults is the result of each reported koinon-pickup skill, by family.
func pickupResults(skills []PickupSkill) map[string]string {
	results := map[string]string{}
	for _, skill := range skills {
		results[skill.Family] = skill.Result
	}
	return results
}

// The install writes the managed cycle's koinon-pickup skill for the person, under the Claude
// Code configuration root and in ~/.agents/skills for Codex, whatever agents it names; a
// different file is replaced. The uninstall removes only files that hold this skill (#406).
func TestInstallPickupSkills(t *testing.T) {
	e := newEnv(t, "systemd")
	claude := filepath.Join(e.o.ClaudeConfig, "skills", "koinon-pickup", "SKILL.md")
	codex := filepath.Join(e.o.Services.Home, ".agents", "skills", "koinon-pickup", "SKILL.md")
	// A project's own pickup skill and a personal pickup skill are not touched.
	personal := filepath.Join(e.o.ClaudeConfig, "skills", "pickup", "SKILL.md")
	if err := os.MkdirAll(filepath.Dir(personal), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(personal, []byte("a personal pickup\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	r, err := Install(context.Background(), e.o)
	if err != nil || !r.OK || !reflect.DeepEqual(pickupResults(r.PickupSkills), map[string]string{"claude": "created", "codex": "created"}) {
		t.Fatalf("install %+v %v", r.PickupSkills, err)
	}
	for _, skill := range r.PickupSkills {
		if want := map[string]string{"claude": claude, "codex": codex}[skill.Family]; skill.Path != want {
			t.Fatalf("%s path %q, want %q", skill.Family, skill.Path, want)
		}
	}
	for _, path := range []string{claude, codex} {
		data, err := os.ReadFile(path)
		if err != nil || string(data) != string(core.PickupSkill()) || !strings.Contains(string(data), "name: koinon-pickup\n") {
			t.Fatalf("%s: %q %v", path, data, err)
		}
	}
	if data, _ := os.ReadFile(personal); string(data) != "a personal pickup\n" {
		t.Fatalf("personal pickup changed: %q", data)
	}
	if err := os.WriteFile(codex, []byte("an older entry\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	again, err := Install(context.Background(), e.o)
	if err != nil || !reflect.DeepEqual(pickupResults(again.PickupSkills), map[string]string{"claude": "unchanged", "codex": "replaced"}) {
		t.Fatalf("repeat %+v %v", again.PickupSkills, err)
	}
	if data, _ := os.ReadFile(codex); string(data) != string(core.PickupSkill()) {
		t.Fatalf("codex skill not replaced: %q", data)
	}
	// An edited file is kept at the uninstall; the binary's skill is removed with its directory.
	if err := os.WriteFile(claude, []byte("edited\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	u, err := Uninstall(context.Background(), e.o)
	if err != nil || !u.OK || !reflect.DeepEqual(pickupResults(u.PickupSkills), map[string]string{"claude": "kept", "codex": "removed"}) {
		t.Fatalf("uninstall %+v %v", u.PickupSkills, err)
	}
	if _, err := os.Lstat(filepath.Dir(codex)); !os.IsNotExist(err) {
		t.Fatalf("codex skill directory left: %v", err)
	}
	if data, _ := os.ReadFile(claude); string(data) != "edited\n" {
		t.Fatalf("edited claude skill: %q", data)
	}
	os.Remove(claude)
	repeat, err := Uninstall(context.Background(), e.o)
	if err != nil || !reflect.DeepEqual(pickupResults(repeat.PickupSkills), map[string]string{"claude": "absent", "codex": "absent"}) {
		t.Fatalf("repeat uninstall %+v %v", repeat.PickupSkills, err)
	}
	if data, _ := os.ReadFile(personal); string(data) != "a personal pickup\n" {
		t.Fatalf("personal pickup changed: %q", data)
	}
}

// teamResults is the result of each reported team skill entry, by family and name.
func teamResults(skills []PickupSkill) map[string]string {
	results := map[string]string{}
	for _, skill := range skills {
		results[skill.Family+" "+skill.Name] = skill.Result
	}
	return results
}

// The install writes each team skill entry for Claude and Codex at the koinon-pickup places;
// a project's or person's own skill of the bare name is not touched, and the uninstall
// removes only files that hold the binary's entry (#453).
func TestInstallTeamSkills(t *testing.T) {
	e := newEnv(t, "systemd")
	skills, err := core.TeamSkills()
	if err != nil {
		t.Fatal(err)
	}
	want := func(result string) map[string]string {
		m := map[string]string{}
		for _, skill := range skills {
			m["claude "+skill.Name], m["codex "+skill.Name] = result, result
		}
		return m
	}
	personal := filepath.Join(e.o.ClaudeConfig, "skills", "handoff", "SKILL.md")
	if err := os.MkdirAll(filepath.Dir(personal), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(personal, []byte("a personal handoff\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	r, err := Install(context.Background(), e.o)
	if err != nil || !r.OK || !reflect.DeepEqual(teamResults(r.TeamSkills), want("created")) {
		t.Fatalf("install %+v %v", r.TeamSkills, err)
	}
	for _, skill := range skills {
		for _, path := range []string{core.ClaudeSkillPath(e.o.ClaudeConfig, skill.Name), core.CodexSkillPath(e.o.Services.Home, skill.Name)} {
			if data, err := os.ReadFile(path); err != nil || !bytes.Equal(data, skill.Content) {
				t.Fatalf("%s: %q %v", path, data, err)
			}
		}
	}
	again, err := Install(context.Background(), e.o)
	if err != nil || !reflect.DeepEqual(teamResults(again.TeamSkills), want("unchanged")) {
		t.Fatalf("repeat %+v %v", again.TeamSkills, err)
	}
	edited := core.ClaudeSkillPath(e.o.ClaudeConfig, skills[0].Name)
	if err := os.WriteFile(edited, []byte("edited\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	u, err := Uninstall(context.Background(), e.o)
	removed := want("removed")
	removed["claude "+skills[0].Name] = "kept"
	if err != nil || !u.OK || !reflect.DeepEqual(teamResults(u.TeamSkills), removed) {
		t.Fatalf("uninstall %+v %v", u.TeamSkills, err)
	}
	if _, err := os.Lstat(filepath.Dir(core.CodexSkillPath(e.o.Services.Home, skills[0].Name))); !os.IsNotExist(err) {
		t.Fatalf("codex entry directory left: %v", err)
	}
	if data, _ := os.ReadFile(personal); string(data) != "a personal handoff\n" {
		t.Fatalf("personal handoff changed: %q", data)
	}
}

// OpenCode and agy get koinon-pickup and every skill entry in their configuration directories,
// whose discovery was verified, only when the directory exists: no directory is made for an
// agent that is not on the computer, and the uninstall removes what the install wrote (#461).
func TestInstallSkillEntriesOtherFamilies(t *testing.T) {
	e := newEnv(t, "systemd")
	if err := os.MkdirAll(e.o.AgyRoot, 0o700); err != nil {
		t.Fatal(err)
	}
	skills, err := core.TeamSkills()
	if err != nil {
		t.Fatal(err)
	}
	r, err := Install(context.Background(), e.o)
	if err != nil || !r.OK {
		t.Fatalf("install %+v %v", r, err)
	}
	if !reflect.DeepEqual(pickupResults(r.PickupSkills), map[string]string{"claude": "created", "codex": "created", "agy": "created"}) {
		t.Fatalf("pickup %+v", r.PickupSkills)
	}
	agy := 0
	for _, entry := range r.TeamSkills {
		if entry.Family == "opencode" {
			t.Fatalf("an entry for an absent OpenCode: %+v", entry)
		}
		if entry.Family == "agy" {
			agy++
			if entry.Path != filepath.Join(e.o.AgyRoot, "skills", entry.Name, "SKILL.md") || entry.Result != "created" {
				t.Fatalf("agy entry %+v", entry)
			}
		}
	}
	if agy != len(skills) {
		t.Fatalf("agy entries %d, want %d", agy, len(skills))
	}
	for _, skill := range skills {
		if data, err := os.ReadFile(filepath.Join(e.o.AgyRoot, "skills", skill.Name, "SKILL.md")); err != nil || !bytes.Equal(data, skill.Content) {
			t.Fatalf("agy %s: %q %v", skill.Name, data, err)
		}
	}
	if data, err := os.ReadFile(filepath.Join(e.o.AgyRoot, "skills", core.PickupSkillName, "SKILL.md")); err != nil || !bytes.Equal(data, core.PickupSkill()) {
		t.Fatalf("agy pickup: %q %v", data, err)
	}
	if _, err := os.Lstat(e.o.Opencode); !os.IsNotExist(err) {
		t.Fatalf("made OpenCode's directory: %v", err)
	}
	// Once OpenCode's directory exists, the next install writes its entries too.
	if err := os.MkdirAll(e.o.Opencode, 0o700); err != nil {
		t.Fatal(err)
	}
	again, err := Install(context.Background(), e.o)
	if err != nil {
		t.Fatal(err)
	}
	results := map[string]int{}
	for _, entry := range again.TeamSkills {
		results[entry.Family+" "+entry.Result]++
		if entry.Family == "opencode" && entry.Path != filepath.Join(e.o.Opencode, "skills", entry.Name, "SKILL.md") {
			t.Fatalf("opencode entry %+v", entry)
		}
	}
	if results["opencode created"] != len(skills) || results["agy unchanged"] != len(skills) {
		t.Fatalf("repeat %v", results)
	}
	u, err := Uninstall(context.Background(), e.o)
	if err != nil || !u.OK {
		t.Fatalf("uninstall %+v %v", u, err)
	}
	for _, root := range []string{e.o.AgyRoot, e.o.Opencode} {
		for _, skill := range append(skills, core.TeamSkill{Name: core.PickupSkillName}) {
			if _, err := os.Lstat(filepath.Join(root, "skills", skill.Name)); !os.IsNotExist(err) {
				t.Fatalf("%s %s left: %v", root, skill.Name, err)
			}
		}
	}
}

// An entry directory or file that is a link, such as a link to a project's own skill, is the
// person's: install, a repeat and uninstall keep it and the linked files unchanged (#461).
func TestInstallKeepsLinkedEntries(t *testing.T) {
	e := newEnv(t, "systemd")
	project := filepath.Join(t.TempDir(), "project", "agents", "skills")
	linked := map[string]string{} // entry path -> the project file it reaches
	for _, link := range []struct{ entry, target string }{
		// A directory link and a file link.
		{filepath.Dir(core.CodexSkillPath(e.o.Services.Home, "koinon-check")), filepath.Join(project, "check")},
		{core.ClaudeSkillPath(e.o.ClaudeConfig, core.PickupSkillName), filepath.Join(project, "pickup", "SKILL.md")},
	} {
		file := link.target
		if filepath.Base(file) != "SKILL.md" {
			file = filepath.Join(link.target, "SKILL.md")
		}
		if err := os.MkdirAll(filepath.Dir(file), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(file, []byte("the project's skill\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		if err := os.MkdirAll(filepath.Dir(link.entry), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(link.target, link.entry); err != nil {
			t.Fatal(err)
		}
		linked[link.entry] = file
	}
	check := core.CodexSkillPath(e.o.Services.Home, "koinon-check")
	pickup := core.ClaudeSkillPath(e.o.ClaudeConfig, core.PickupSkillName)
	unchanged := func(step string) {
		t.Helper()
		for entry, file := range linked {
			if data, err := os.ReadFile(file); err != nil || string(data) != "the project's skill\n" {
				t.Fatalf("%s changed %s through %s: %q %v", step, file, entry, data, err)
			}
			if info, err := os.Lstat(entry); err != nil || info.Mode()&os.ModeSymlink == 0 {
				t.Fatalf("%s replaced the link %s: %v", step, entry, err)
			}
		}
	}
	results := func(r Report) map[string]string {
		m := map[string]string{}
		for _, s := range append(r.PickupSkills, r.TeamSkills...) {
			if s.Path == check || s.Path == pickup {
				m[s.Path] = s.Result
			}
		}
		return m
	}
	for _, step := range []string{"install", "repeat"} {
		r, err := Install(context.Background(), e.o)
		if err != nil || !reflect.DeepEqual(results(r), map[string]string{check: "kept", pickup: "kept"}) {
			t.Fatalf("%s %v %v", step, results(r), err)
		}
		unchanged(step)
	}
	u, err := Uninstall(context.Background(), e.o)
	if err != nil || !reflect.DeepEqual(results(u), map[string]string{check: "kept", pickup: "kept"}) {
		t.Fatalf("uninstall %v %v", results(u), err)
	}
	unchanged("uninstall")
}

// A koinon-pickup skill that cannot be written fails the install with the path, after the
// service and setup steps, which a repeated install completes.
func TestInstallPickupSkillFailure(t *testing.T) {
	e := newEnv(t, "systemd")
	// A file where the skills directory belongs.
	if err := os.MkdirAll(e.o.ClaudeConfig, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(e.o.ClaudeConfig, "skills"), []byte("not a directory"), 0o600); err != nil {
		t.Fatal(err)
	}
	r, err := Install(context.Background(), e.o)
	if err == nil || r.OK || !strings.HasPrefix(err.Error(), "pickup_skill_failed: "+filepath.Join(e.o.ClaudeConfig, "skills")) || r.Service != "running" {
		t.Fatalf("install %+v %v", r, err)
	}
}

func TestInstallAndUninstall(t *testing.T) {
	for _, backend := range []string{"systemd", "launchd"} {
		t.Run(backend, func(t *testing.T) {
			e := newEnv(t, backend)
			e.o.Agents = []string{"codex", "claude"}
			r, err := Install(context.Background(), e.o)
			if err != nil || !r.OK || r.Service != "running" || r.BinaryResult != "replaced" {
				t.Fatalf("install %+v %v", r, err)
			}
			data, err := os.ReadFile(r.Binary)
			if err != nil || string(data) != "synthetic binary" {
				t.Fatalf("binary %q %v", data, err)
			}
			if info, _ := os.Stat(r.Binary); info.Mode().Perm() != 0755 {
				t.Fatalf("binary mode %v", info.Mode())
			}
			artifact, _ := os.ReadFile(r.Artifact)
			if !strings.Contains(string(artifact), platform.ServiceMarker) || !strings.Contains(string(artifact), r.Binary) {
				t.Fatalf("artifact %s", artifact)
			}
			want := map[string]string{"systemd": "systemctl --user enable koinon.service", "launchd": "launchctl bootstrap gui/501 " + r.Artifact}[backend]
			if !strings.Contains(strings.Join(e.r.calls, "\n"), want) {
				t.Fatalf("manager calls %v", e.r.calls)
			}
			if len(e.setups) != 2 || e.setups[0] != "codex "+r.Binary {
				t.Fatalf("setup %v", e.setups)
			}
			// A repeated install keeps the binary and artifact and restarts the service.
			again, err := Install(context.Background(), e.o)
			if err != nil || !again.OK || again.BinaryResult != "unchanged" {
				t.Fatalf("repeat %+v %v", again, err)
			}
			u, err := Uninstall(context.Background(), e.o)
			// Uninstall removes setup for the agents given, here codex and claude.
			if err != nil || !u.OK || u.Service != "removed" || u.BinaryResult != "removed" || len(e.removals) != 2 {
				t.Fatalf("uninstall %+v %v %v", u, err, e.removals)
			}
			if _, err := os.Stat(r.Artifact); !os.IsNotExist(err) {
				t.Fatalf("artifact left: %v", err)
			}
			if _, err := os.Stat(e.o.StateDir); err != nil {
				t.Fatalf("state removed: %v", err)
			}
			repeat, err := Uninstall(context.Background(), e.o)
			if err != nil || !repeat.OK || repeat.Service != "absent" || repeat.BinaryResult != "absent" {
				t.Fatalf("repeat uninstall %+v %v", repeat, err)
			}
		})
	}
}

func TestInstallRefusals(t *testing.T) {
	e := newEnv(t, "systemd")
	// An artifact at the daemon's path without the marker is never replaced.
	os.MkdirAll(filepath.Dir(e.o.Services.DaemonArtifact()), 0755)
	os.WriteFile(e.o.Services.DaemonArtifact(), []byte("[Service]\nExecStart=/usr/bin/other\n"), 0644)
	if r, err := Install(context.Background(), e.o); err == nil || r.OK || !strings.Contains(err.Error(), "service_artifact_unowned") {
		t.Fatalf("unowned artifact: %v", err)
	}
	if r, err := Uninstall(context.Background(), e.o); err == nil || r.OK || !strings.Contains(err.Error(), "service_artifact_unowned") {
		t.Fatalf("unowned removal: %v", err)
	}
	os.Remove(e.o.Services.DaemonArtifact())
	// A Python-era installation refuses a plain install.
	os.MkdirAll(e.o.PythonPrefix, 0700)
	os.WriteFile(filepath.Join(e.o.PythonPrefix, "install.json"), []byte(`{"state_root": "/x"}`), 0600)
	if r, err := Install(context.Background(), e.o); err == nil || r.OK || !strings.Contains(err.Error(), "python_install_present") {
		t.Fatalf("python present: %v", err)
	}
	e.o.Upgrade = true
	// Without a reachable manager the install reports the start command.
	e.r.fail = func(argv []string) bool { return argv[0] == "systemctl" }
	r, err := Install(context.Background(), e.o)
	if err != nil || r.Service != "manual_required" || r.StartCommand != r.Binary+" serve --state-dir "+e.o.StateDir {
		t.Fatalf("manual %+v %v", r, err)
	}
	// --no-start stages without any manager call.
	e.r.calls = nil
	e.o.NoStart = true
	if r, err := Install(context.Background(), e.o); err != nil || r.Service != "staged" || len(e.r.calls) != 0 {
		t.Fatalf("staged %+v %v %v", r, err, e.r.calls)
	}
}

func TestInstallPathLink(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t, "systemd")
	home := e.o.Services.Home
	linkDir := filepath.Join(home, ".local", "bin")
	link := filepath.Join(linkDir, "koinon")

	// The link directory is not on PATH: the link is made and the report names the step.
	r, err := Install(ctx, e.o)
	if err != nil || r.Link != link || r.LinkResult != "created" || r.OnPath != "" ||
		r.PathStep != `add `+linkDir+` to PATH in your shell's startup file: export PATH="`+linkDir+`:$PATH"` {
		t.Fatalf("not on PATH %+v %v", r, err)
	}
	if target, err := os.Readlink(link); err != nil || target != r.Binary {
		t.Fatalf("link %q %v", target, err)
	}

	// On PATH, koinon runs the installed binary and no step is needed.
	e.o.Path = "relative:" + linkDir
	if r, err = Install(ctx, e.o); err != nil || r.LinkResult != "unchanged" || r.OnPath != link || r.PathStep != "" {
		t.Fatalf("on PATH %+v %v", r, err)
	}

	// Another koinon earlier on PATH, such as the Homebrew copy, is named.
	brew := filepath.Join(home, "brew", "bin")
	os.MkdirAll(brew, 0755)
	os.WriteFile(filepath.Join(brew, "koinon"), []byte("other"), 0755)
	e.o.Path = brew + string(filepath.ListSeparator) + linkDir
	if r, err = Install(ctx, e.o); err != nil || r.OnPath != filepath.Join(brew, "koinon") ||
		r.PathStep != "PATH runs "+r.OnPath+" first; put "+linkDir+" before its directory in PATH, or run "+r.Binary {
		t.Fatalf("other on PATH %+v %v", r, err)
	}

	// Uninstall removes the link it made; a repeat finds nothing.
	if u, err := Uninstall(ctx, e.o); err != nil || u.LinkResult != "removed" {
		t.Fatalf("uninstall %+v %v", u, err)
	}
	if _, err := os.Lstat(link); !os.IsNotExist(err) {
		t.Fatalf("link left: %v", err)
	}
	if u, err := Uninstall(ctx, e.o); err != nil || u.LinkResult != "absent" {
		t.Fatalf("repeat uninstall %+v %v", u, err)
	}

	// A link the install did not make is used but never removed.
	os.Symlink(Binary(e.o.Prefix), link)
	if r, err = Install(ctx, e.o); err != nil || r.LinkResult != "unchanged" {
		t.Fatalf("existing link %+v %v", r, err)
	}
	if u, err := Uninstall(ctx, e.o); err != nil || u.LinkResult != "kept" {
		t.Fatalf("existing link uninstall %+v %v", u, err)
	}
	os.Remove(link)

	// A made link that now points elsewhere is kept.
	if r, err = Install(ctx, e.o); err != nil || r.LinkResult != "created" {
		t.Fatalf("relink %+v %v", r, err)
	}
	os.Remove(link)
	os.Symlink(filepath.Join(brew, "koinon"), link)
	if u, err := Uninstall(ctx, e.o); err != nil || u.LinkResult != "kept" {
		t.Fatalf("retargeted uninstall %+v %v", u, err)
	}
	if target, _ := os.Readlink(link); target != filepath.Join(brew, "koinon") {
		t.Fatalf("retargeted link changed: %q", target)
	}
	os.Remove(link)

	// Another file at the link's path is never replaced.
	os.WriteFile(link, []byte("mine"), 0755)
	e.o.Path = linkDir
	if r, err = Install(ctx, e.o); err != nil || r.LinkResult != "occupied" || r.OnPath != link ||
		r.PathStep != link+" is not a link to the installed binary; replace it with ln -sf "+r.Binary+" "+link+", or run "+r.Binary {
		t.Fatalf("occupied %+v %v", r, err)
	}
	if u, err := Uninstall(ctx, e.o); err != nil || u.LinkResult != "kept" {
		t.Fatalf("occupied uninstall %+v %v", u, err)
	}
	if data, _ := os.ReadFile(link); string(data) != "mine" {
		t.Fatalf("occupied file changed: %q", data)
	}
	if _, err := os.Stat(e.o.Prefix); !os.IsNotExist(err) {
		t.Fatalf("prefix left: %v", err)
	}
}

// An install that the installed binary runs copies nothing. When another koinon on PATH,
// such as the Homebrew copy after brew upgrade, has other content, the report names it and
// the step that installs it (#380).
func TestInstallNamesOtherBinaryWhenTheInstalledCopyRuns(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t, "systemd")
	linkDir := filepath.Join(e.o.Services.Home, ".local", "bin")
	brew := filepath.Join(e.o.Services.Home, "brew", "bin")
	other := filepath.Join(brew, "koinon")
	os.MkdirAll(brew, 0755)
	e.o.Path = linkDir + string(filepath.ListSeparator) + brew
	if r, err := Install(ctx, e.o); err != nil || r.BinaryResult != "replaced" || r.OtherBinary != "" || r.SourceStep != "" {
		t.Fatalf("first install %+v %v", r, err)
	}
	e.o.Source = filepath.Join(linkDir, "koinon")

	// No other koinon, then an identical copy: nothing to name.
	for _, content := range []string{"", "synthetic binary"} {
		if content != "" {
			os.WriteFile(other, []byte(content), 0755)
		}
		if r, err := Install(ctx, e.o); err != nil || r.BinaryResult != "unchanged" || r.OtherBinary != "" || r.SourceStep != "" {
			t.Fatalf("other %q: %+v %v", content, r, err)
		}
	}

	// A newer copy later on PATH is named, with its install step.
	os.WriteFile(other, []byte("newer binary"), 0755)
	r, err := Install(ctx, e.o)
	if err != nil || !r.OK || r.BinaryResult != "unchanged" || r.OnPath != e.o.Source || r.OtherBinary != other ||
		r.SourceStep != "this ran the installed binary, so nothing was copied; to install "+other+", run '"+other+"' install" {
		t.Fatalf("newer other %+v %v", r, err)
	}

	// The step's command runs in a POSIX shell, also from a directory with a space and a quote.
	quoted := filepath.Join(e.o.Services.Home, "brew source's copy", "bin")
	os.MkdirAll(quoted, 0755)
	os.WriteFile(filepath.Join(quoted, "koinon"), []byte("#!/bin/sh\necho ran \"$0\" \"$@\"\n"), 0755)
	e.o.Path = linkDir + string(filepath.ListSeparator) + quoted
	r, err = Install(ctx, e.o)
	_, command, _ := strings.Cut(r.SourceStep, ", run ")
	out, runErr := exec.Command("/bin/sh", "-c", command).CombinedOutput()
	if err != nil || r.OtherBinary != filepath.Join(quoted, "koinon") || runErr != nil ||
		string(out) != "ran "+filepath.Join(quoted, "koinon")+" install\n" {
		t.Fatalf("quoted step %+v %v: %q %v", r, err, out, runErr)
	}
	e.o.Path = linkDir + string(filepath.ListSeparator) + brew

	// Installing from the other binary copies it and names nothing.
	e.o.Source = other
	if r, err := Install(ctx, e.o); err != nil || r.BinaryResult != "replaced" || r.OtherBinary != "" || r.SourceStep != "" {
		t.Fatalf("from other %+v %v", r, err)
	}
	if data, _ := os.ReadFile(r.Binary); string(data) != "newer binary" {
		t.Fatalf("installed %q", data)
	}
}

func TestInstallLinkMarkerRefusesUnrelatedFile(t *testing.T) {
	ctx := context.Background()
	for _, kind := range []string{"symlink", "file"} {
		t.Run(kind, func(t *testing.T) {
			e := newEnv(t, "systemd")
			retained := filepath.Join(t.TempDir(), "retained")
			os.WriteFile(retained, []byte("unrelated"), 0600)
			marker := filepath.Join(e.o.Prefix, "link")
			os.MkdirAll(e.o.Prefix, 0755)
			if kind == "symlink" {
				os.Symlink(retained, marker)
			} else {
				os.WriteFile(marker, []byte("unrelated"), 0600)
			}
			// Install and uninstall refuse the marker and change neither file.
			if _, err := Install(ctx, e.o); err == nil || !strings.Contains(err.Error(), "link_marker_invalid") {
				t.Fatalf("install: %v", err)
			}
			if _, err := os.Lstat(filepath.Join(e.o.Services.Home, ".local", "bin", "koinon")); !os.IsNotExist(err) {
				t.Fatalf("link made: %v", err)
			}
			if _, err := Uninstall(ctx, e.o); err == nil || !strings.Contains(err.Error(), "link_marker_invalid") {
				t.Fatalf("uninstall: %v", err)
			}
			if data, _ := os.ReadFile(retained); string(data) != "unrelated" {
				t.Fatalf("retained file changed: %q", data)
			}
			if kind == "file" {
				if data, _ := os.ReadFile(marker); string(data) != "unrelated" {
					t.Fatalf("marker file changed: %q", data)
				}
			}
		})
	}
}

func TestInstallReportsRelativePathEntries(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t, "systemd")
	linkDir := filepath.Join(e.o.Services.Home, ".local", "bin")
	wd := t.TempDir()
	t.Chdir(wd)
	os.MkdirAll(filepath.Join(wd, "rel"), 0755)
	os.WriteFile(filepath.Join(wd, "rel", "koinon"), []byte("relative"), 0755)
	os.WriteFile(filepath.Join(wd, "koinon"), []byte("current"), 0755)
	sep := string(filepath.ListSeparator)
	// A relative entry before the link directory runs first, as in a shell.
	e.o.Path = "rel" + sep + linkDir
	r, err := Install(ctx, e.o)
	if err != nil || r.OnPath != filepath.Join(wd, "rel", "koinon") ||
		r.PathStep != "PATH runs "+r.OnPath+" first; put "+linkDir+" before its directory in PATH, or run "+r.Binary {
		t.Fatalf("relative entry %+v %v", r, err)
	}
	// An empty entry is the current directory.
	e.o.Path = sep + linkDir
	if r, err = Install(ctx, e.o); err != nil || r.OnPath != filepath.Join(wd, "koinon") {
		t.Fatalf("empty entry %+v %v", r, err)
	}
	// After the link directory, neither matters.
	e.o.Path = linkDir + sep + "rel" + sep
	if r, err = Install(ctx, e.o); err != nil || r.OnPath != filepath.Join(linkDir, "koinon") || r.PathStep != "" {
		t.Fatalf("link first %+v %v", r, err)
	}
}

// An install records the launcher CLI of each other family that an earlier setup configured,
// and reports only those; the named families are set up as before (#273).
func TestInstallAdoptsEarlierSetup(t *testing.T) {
	e := newEnv(t, "systemd")
	e.o.Agents = []string{"codex", "claude"}
	e.adopted = map[string]bool{"agy": true}
	r, err := Install(context.Background(), e.o)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(e.adopts, []string{"agy " + r.Binary + " " + e.o.StateDir, "opencode " + r.Binary + " " + e.o.StateDir, "grok " + r.Binary + " " + e.o.StateDir}) {
		t.Fatalf("adopt calls %v", e.adopts)
	}
	if len(r.Agents) != 3 || r.Agents[2].Family != "agy" {
		t.Fatalf("agents %+v", r.Agents)
	}
	e = newEnv(t, "launchd")
	e.o.Adopt = func(context.Context, setup.Options) (setup.Report, bool, error) {
		return setup.Report{}, false, errors.New("unsafe launcher configuration")
	}
	if _, err := Install(context.Background(), e.o); err == nil || err.Error() != "record claude launcher: unsafe launcher configuration" {
		t.Fatalf("adopt failure: %v", err)
	}
}

// homebrewKeg writes a koinon keg as Homebrew pours it under brew: the binary, its receipt,
// the opt link to the keg and the bin link to the binary. It returns the bin link.
func homebrewKeg(t *testing.T, brew, version, content string) string {
	t.Helper()
	keg := filepath.Join(brew, "Cellar", "koinon", version)
	if err := os.MkdirAll(filepath.Join(keg, "bin"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(keg, "bin", "koinon"), []byte(content), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(keg, "INSTALL_RECEIPT.json"), []byte("{}"), 0644); err != nil {
		t.Fatal(err)
	}
	for link, target := range map[string]string{
		filepath.Join(brew, "opt", "koinon"): filepath.Join("..", "Cellar", "koinon", version),
		filepath.Join(brew, "bin", "koinon"): filepath.Join("..", "Cellar", "koinon", version, "bin", "koinon"),
	} {
		os.MkdirAll(filepath.Dir(link), 0755)
		os.Remove(link)
		if err := os.Symlink(target, link); err != nil {
			t.Fatal(err)
		}
	}
	return filepath.Join(brew, "bin", "koinon")
}

// A user-mode install from a Homebrew keg links the installed binary to the formula's opt
// path, so brew upgrade alone replaces what the service runs (#397). Any other source is
// copied over the link, and the uninstall removes the link and keeps the keg.
func TestInstallLinksAHomebrewKeg(t *testing.T) {
	for _, backend := range []string{"systemd", "launchd"} {
		t.Run(backend, func(t *testing.T) {
			ctx := context.Background()
			e := newEnv(t, backend)
			e.o.Agents = []string{"claude"}
			// The installer resolves the keg's path; on macOS the temporary directory is under a
			// link (/var to /private/var).
			tmp, err := filepath.EvalSymlinks(t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			brew := filepath.Join(tmp, "brew")
			opt := filepath.Join(brew, "opt", "koinon", "bin", "koinon")
			e.o.Source = homebrewKeg(t, brew, "0.3.1", "release 0.3.1")
			r, err := Install(ctx, e.o)
			if err != nil || !r.OK || r.BinaryResult != "linked" {
				t.Fatalf("install %+v %v", r, err)
			}
			if target, err := os.Readlink(r.Binary); err != nil || target != opt {
				t.Fatalf("installed binary links to %q: %v", target, err)
			}
			artifact, _ := os.ReadFile(r.Artifact)
			if !strings.Contains(string(artifact), r.Binary) || len(e.setups) != 1 || e.setups[0] != "claude "+r.Binary {
				t.Fatalf("artifact %s, setup %v", artifact, e.setups)
			}
			if again, err := Install(ctx, e.o); err != nil || again.BinaryResult != "unchanged" {
				t.Fatalf("repeat %+v %v", again, err)
			}
			// brew upgrade: a new keg and the opt link moved to it. The installed path runs it.
			e.o.Source = homebrewKeg(t, brew, "0.3.2", "release 0.3.2")
			if data, err := os.ReadFile(r.Binary); err != nil || string(data) != "release 0.3.2" {
				t.Fatalf("after the upgrade %q %v", data, err)
			}
			if again, err := Install(ctx, e.o); err != nil || again.BinaryResult != "unchanged" {
				t.Fatalf("install from the new keg %+v %v", again, err)
			}

			// Not a keg: no receipt, an opt link elsewhere, or a plain download. Each is copied.
			receipt := filepath.Join(brew, "Cellar", "koinon", "0.3.2", "INSTALL_RECEIPT.json")
			os.Rename(receipt, receipt+".moved")
			if got, err := Install(ctx, e.o); err != nil || got.BinaryResult != "replaced" {
				t.Fatalf("without receipt %+v %v", got, err)
			}
			if info, err := os.Lstat(r.Binary); err != nil || !info.Mode().IsRegular() {
				t.Fatalf("copy %v %v", info, err)
			}
			os.Rename(receipt+".moved", receipt)
			if got, err := Install(ctx, e.o); err != nil || got.BinaryResult != "linked" {
				t.Fatalf("relink %+v %v", got, err)
			}
			os.Remove(filepath.Join(brew, "opt", "koinon"))
			os.Symlink(t.TempDir(), filepath.Join(brew, "opt", "koinon"))
			if got, err := Install(ctx, e.o); err != nil || got.BinaryResult != "replaced" {
				t.Fatalf("foreign opt %+v %v", got, err)
			}
			homebrewKeg(t, brew, "0.3.2", "release 0.3.2")
			if got, err := Install(ctx, e.o); err != nil || got.BinaryResult != "linked" {
				t.Fatalf("relink %+v %v", got, err)
			}
			// The same content as the keg, but from a download: a copy, not "unchanged".
			download := filepath.Join(t.TempDir(), "koinon")
			os.WriteFile(download, []byte("release 0.3.2"), 0755)
			e.o.Source = download
			if got, err := Install(ctx, e.o); err != nil || got.BinaryResult != "replaced" {
				t.Fatalf("download over the link %+v %v", got, err)
			}
			if info, err := os.Lstat(r.Binary); err != nil || !info.Mode().IsRegular() {
				t.Fatalf("copy %v %v", info, err)
			}

			e.o.Source = filepath.Join(brew, "bin", "koinon")
			if got, err := Install(ctx, e.o); err != nil || got.BinaryResult != "linked" {
				t.Fatalf("relink %+v %v", got, err)
			}
			u, err := Uninstall(ctx, e.o)
			if err != nil || !u.OK || u.BinaryResult != "removed" {
				t.Fatalf("uninstall %+v %v", u, err)
			}
			if _, err := os.Lstat(r.Binary); !os.IsNotExist(err) {
				t.Fatalf("link left: %v", err)
			}
			if data, err := os.ReadFile(opt); err != nil || string(data) != "release 0.3.2" {
				t.Fatalf("keg after uninstall %q %v", data, err)
			}
		})
	}
}
