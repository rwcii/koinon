package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"syscall"
	"time"

	"github.com/rwcii/koinon/internal/mcp"
)

// mcpResumeVariable carries a replaced koinon mcp program's state to the new one (#440).
const mcpResumeVariable = "KOINON_MCP_RESUME"

// mcpReplacement returns how koinon mcp sees that its executable was replaced, as the
// daemon sees it, and how it runs the new one in its own process, keeping its process ID,
// standard input and output, and its arguments, so the new one watches the same path. Both are nil when the launch path does not resolve to a regular
// file. A replacement that does not happen waits for the next change of the file.
func mcpReplacement() (func() bool, func([]byte) error) {
	path := launchPath()
	if !filepath.IsAbs(path) {
		// An agent that starts koinon by its name finds it on PATH.
		found, err := exec.LookPath(path)
		if err != nil {
			return nil, nil
		}
		if path, err = filepath.Abs(found); err != nil {
			return nil, nil
		}
	}
	start, err := os.Stat(path)
	if err != nil || !start.Mode().IsRegular() {
		return nil, nil
	}
	changed := func() bool {
		now, err := os.Stat(path)
		return replacedExecutable(start, now, err)
	}
	replace := func(state []byte) error {
		// The file that the path resolves to now is the one probed and run, never one that a
		// later move of a link names (#441 review).
		file, err := filepath.EvalSymlinks(path)
		if err != nil {
			return err
		}
		probed, err := os.Stat(file)
		if err != nil {
			return err
		}
		start = probed
		if err := resumes(file); err != nil {
			return err
		}
		if now, err := os.Stat(file); err != nil || !os.SameFile(probed, now) {
			return errors.New("the new program was replaced during its version command")
		}
		return syscall.Exec(file, os.Args, append(os.Environ(), mcpResumeVariable+"="+string(state)))
	}
	return changed, replace
}

// resumeProbe bounds the new program's version command; a variable only so that tests can
// set it.
var resumeProbe = 10 * time.Second

// resumes runs the new program's version command and requires that it reads the state this
// program passes. An older release, or a file that does not run here, keeps this program.
func resumes(path string) error {
	ctx, cancel := context.WithTimeout(context.Background(), resumeProbe)
	defer cancel()
	out, err := exec.CommandContext(ctx, path, "version").Output()
	if err != nil {
		return fmt.Errorf("the new program's version command failed: %w", err)
	}
	var reply struct {
		Version   string `json:"version"`
		MCPResume string `json:"mcp_resume"`
	}
	if json.Unmarshal(out, &reply) != nil || reply.MCPResume != strconv.Itoa(mcp.ResumeVersion) {
		return errors.New("the new program " + strconv.Quote(reply.Version) + " does not continue a running session; restart the session to use it")
	}
	return nil
}
