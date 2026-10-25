package core

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
)

// The retired Python runtime wrote its own instructions between these markers into the Codex
// AGENTS.md. The Go runtime has other instructions, so setup, install and the daemon's start
// take the blocks out (#487).
var retiredCodexMarkers = [][2]string{
	{"<!-- BEGIN KOINON CODEX -->", "<!-- END KOINON CODEX -->"},
	{"<!-- BEGIN CODEX PEER BRIDGE -->", "<!-- END CODEX PEER BRIDGE -->"},
}

// CodexInstructions is the Codex AGENTS.md: in CODEX_HOME when it is set, else in ~/.codex.
func CodexInstructions(codexHome, home string) string {
	if codexHome == "" {
		codexHome = filepath.Join(home, ".codex")
	}
	return filepath.Join(codexHome, "AGENTS.md")
}

// withoutRetiredBlocks returns data without each complete retired block: the begin line
// through the end line, and the empty line that the Python runtime put before it. A begin
// line without its end line leaves the rest as it is. Every other byte stays, CRLF line ends
// included.
func withoutRetiredBlocks(data []byte) []byte {
	var out []byte
	for len(data) > 0 {
		line, rest := data, []byte(nil)
		if i := bytes.IndexByte(data, '\n'); i >= 0 {
			line, rest = data[:i+1], data[i+1:]
		}
		end := ""
		for _, m := range retiredCodexMarkers {
			if string(bytes.TrimRight(line, "\r\n")) == m[0] {
				end = m[1]
			}
		}
		if end == "" {
			out = append(out, line...)
			data = rest
			continue
		}
		after, found := blockEnd(rest, end)
		if !found {
			out = append(out, line...)
			data = rest
			continue
		}
		// The block's own separator: the empty line just before it.
		if n := len(out); n > 0 && out[n-1] == '\n' {
			if n >= 2 && out[n-2] == '\r' && (n == 2 || out[n-3] == '\n') {
				out = out[:n-2]
			} else if n == 1 || out[n-2] == '\n' {
				out = out[:n-1]
			}
		}
		data = after
	}
	return out
}

// blockEnd returns what follows the line that is exactly end, and whether there is one.
func blockEnd(data []byte, end string) ([]byte, bool) {
	for len(data) > 0 {
		line, rest := data, []byte(nil)
		if i := bytes.IndexByte(data, '\n'); i >= 0 {
			line, rest = data[:i+1], data[i+1:]
		}
		if string(bytes.TrimRight(line, "\r\n")) == end {
			return rest, true
		}
		data = rest
	}
	return nil, false
}

// RemoveRetiredCodexBlocks takes the retired blocks out of the file at path and reports
// "removed", "unchanged" or "absent". It writes the file in place of a symbolic link's target,
// with the file's mode.
func RemoveRetiredCodexBlocks(path string) (string, error) {
	real, err := filepath.EvalSymlinks(path)
	if errors.Is(err, os.ErrNotExist) {
		return "absent", nil
	}
	if err != nil {
		return "", err
	}
	info, err := os.Stat(real)
	if err != nil {
		return "", err
	}
	data, err := os.ReadFile(real)
	if err != nil {
		return "", err
	}
	cleaned := withoutRetiredBlocks(data)
	if bytes.Equal(cleaned, data) {
		return "unchanged", nil
	}
	tmp, err := os.CreateTemp(filepath.Dir(real), ".AGENTS-*")
	if err != nil {
		return "", err
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.Write(cleaned); err != nil {
		tmp.Close()
		return "", err
	}
	if err := tmp.Chmod(info.Mode().Perm()); err != nil {
		tmp.Close()
		return "", err
	}
	if err := tmp.Close(); err != nil {
		return "", err
	}
	if err := os.Rename(tmp.Name(), real); err != nil {
		return "", err
	}
	return "removed", nil
}
