// skill-commands is repository tooling, not an installed Koinon command.
package main

import (
	"errors"
	"flag"
	"fmt"
	"github.com/rwcii/koinon/internal/skillmeta"
	"os"
	"path/filepath"
	"slices"
	"strings"
)

const marker = skillmeta.CommandMarker

// Native commands and aliases: https://opencode.ai/docs/tui/ and OpenCode 1.18.35's
// command registry (init, review). Keep this list aligned when verifying a new CLI.
var reserved = strings.Fields("connect compact summarize details editor exit quit q export help init review models new clear redo sessions resume continue share themes thinking undo unshare")
var legacyNames = strings.Fields("check handoff pair peer-review peer-tmux pickup release ship sprint")

func main() {
	check := flag.Bool("check", false, "check generated commands without writing")
	flag.Parse()
	if flag.NArg() != 0 {
		fmt.Fprintln(os.Stderr, "usage: go run ./scripts/skill-commands [--check]")
		os.Exit(2)
	}
	if err := generate(".", *check); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func scalar(raw string) (string, error) { return skillmeta.Scalar(raw) }
func metadata(data []byte, directory string) (string, string, error) {
	name, desc, err := skillmeta.Metadata(data, directory)
	if err == nil && slices.Contains(reserved, name) {
		return "", "", fmt.Errorf("name %q is reserved by OpenCode", name)
	}
	return name, desc, err
}
func template(name string) string      { return skillmeta.CommandTemplate(name) }
func wrapper(name, desc string) []byte { return skillmeta.CommandWrapper(name, desc) }
func owned(name string, data []byte) bool {
	text := string(data)
	if strings.Contains(text, "\n"+marker+"\n") {
		return true
	}
	// Adopt only the nine pre-generator wrappers, with their exact original template.
	if !slices.Contains(legacyNames, name) || !strings.HasPrefix(text, "---\ndescription: ") {
		return false
	}
	header, body, ok := strings.Cut(strings.TrimPrefix(text, "---\ndescription: "), "\n---\n")
	if !ok || strings.Contains(header, "\n") || body != template(name) {
		return false
	}
	_, err := scalar(header)
	return err == nil
}
func directory(path string, missing bool) error {
	info, err := os.Lstat(path)
	if missing && os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	if !info.IsDir() {
		return fmt.Errorf("%s must be a real directory", path)
	}
	return nil
}

type change struct {
	path   string
	data   []byte
	remove bool
}

func generate(root string, check bool) error {
	skills := filepath.Join(root, "agents", "skills")
	for _, p := range []string{filepath.Join(root, "agents"), skills} {
		if err := directory(p, false); err != nil {
			return err
		}
	}
	desired := map[string][]byte{}
	entries, err := os.ReadDir(skills)
	if err != nil {
		return err
	}
	for _, entry := range entries {
		if entry.Type()&os.ModeSymlink != 0 {
			return fmt.Errorf("skill metadata must not be a symlink: %s", entry.Name())
		}
		if !entry.IsDir() {
			continue
		}
		file := filepath.Join(skills, entry.Name(), "SKILL.md")
		info, err := os.Lstat(file)
		if err != nil {
			return err
		}
		if !info.Mode().IsRegular() {
			return fmt.Errorf("%s must be a regular file", file)
		}
		data, err := os.ReadFile(file)
		if err != nil {
			return err
		}
		name, desc, err := metadata(data, entry.Name())
		if err != nil {
			return fmt.Errorf("%s: %w", file, err)
		}
		desired[name+".md"] = wrapper(name, desc)
	}
	if len(desired) == 0 {
		return errors.New("no canonical skills found; refusing to remove commands")
	}
	parent := filepath.Join(root, ".opencode")
	commands := filepath.Join(parent, "commands")
	for _, p := range []string{parent, commands} {
		if err := directory(p, true); err != nil {
			return err
		}
	}
	existing, err := os.ReadDir(commands)
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	var changes []change
	for _, entry := range existing {
		// A differently cased custom file can occupy the output path on macOS.
		lower := strings.ToLower(entry.Name())
		if _, collision := desired[lower]; collision && entry.Name() != lower {
			return fmt.Errorf("refusing command-name case collision: %s", entry.Name())
		}
		file := entry.Name()
		want, wanted := desired[file]
		if !strings.HasSuffix(file, ".md") {
			continue
		}
		path := filepath.Join(commands, file)
		info, err := os.Lstat(path)
		if err != nil {
			return err
		}
		if !info.Mode().IsRegular() {
			if wanted {
				return fmt.Errorf("refusing non-regular command: %s", file)
			}
			continue
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		ours := owned(strings.TrimSuffix(file, ".md"), data)
		if !ours {
			if wanted {
				return fmt.Errorf("refusing command of unknown ownership: %s", file)
			}
			continue
		}
		if !wanted {
			changes = append(changes, change{path: path, remove: true})
			continue
		}
		delete(desired, file)
		// Git preserves the executable bit, not checkout group-write permissions.
		// Generation still normalizes owned output to 0644; check accepts umask 002.
		if string(data) != string(want) || info.Mode().Perm()&0111 != 0 || !check && info.Mode().Perm() != 0644 {
			changes = append(changes, change{path: path, data: want})
		}
	}
	for file, data := range desired {
		changes = append(changes, change{path: filepath.Join(commands, file), data: data})
	}
	slices.SortFunc(changes, func(a, b change) int { return strings.Compare(a.path, b.path) })
	if check {
		var drift []string
		for _, c := range changes {
			action := "missing or changed"
			if c.remove {
				action = "obsolete"
			}
			drift = append(drift, action+": "+filepath.ToSlash(c.path))
		}
		if len(drift) > 0 {
			return errors.New(strings.Join(drift, "\n") + "\nRun go run ./scripts/skill-commands to regenerate.")
		}
		return nil
	}
	// Preflight all metadata and collisions before changing any file.
	if err := os.MkdirAll(commands, 0755); err != nil {
		return err
	}
	for _, c := range changes {
		if c.remove {
			if err := os.Remove(c.path); err != nil {
				return err
			}
			continue
		}
		if err := write(c.path, c.data); err != nil {
			return err
		}
	}
	return nil
}
func write(path string, data []byte) error {
	f, err := os.CreateTemp(filepath.Dir(path), ".skill-command-*")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	if _, err = f.Write(data); err != nil {
		f.Close()
		return err
	}
	if err = f.Chmod(0644); err != nil {
		f.Close()
		return err
	}
	if err = f.Close(); err != nil {
		return err
	}
	return os.Rename(f.Name(), path)
}
