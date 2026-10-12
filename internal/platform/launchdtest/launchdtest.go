// Package launchdtest models launchd for system-mode tests (#283). Plain launchctl calls on
// gui/<uid> fail with error 125, as over SSH; calls through sudo change the modeled state.
// No call reaches a real manager or sudo.
package launchdtest

import (
	"context"
	"encoding/json"
	"errors"
	"html"
	"os"
	"regexp"
	"strconv"
	"strings"
)

// Fake is the modeled launchd. Its fields are the state that tests set up and read back.
type Fake struct {
	UID                        int
	Label                      string
	AgentLoaded, AgentDisabled bool
	SystemLoaded               bool
	SystemPID                  int
	AgentPID                   int
	// Stale makes status keep answering with the agent's PID, as when the former agent
	// still holds the address. StatusDown makes status fail.
	Stale, StatusDown bool
	// NoDomain models a user without a console login: every command on gui/<uid> fails.
	NoDomain bool
	// PlistJSON is what plutil -convert json prints for the system plist.
	PlistJSON string
	// Fail makes the first command that contains it fail, after FailSkip matching commands
	// succeeded. Rollback makes every command that contains it fail, to break a rollback.
	Fail     string
	FailSkip int
	Rollback string
	Calls    []string
}

// New returns a launchd with the agent loaded and enabled, as after a plain install.
func New(uid int, label string) *Fake {
	return &Fake{UID: uid, Label: label, AgentLoaded: true, AgentPID: 77, SystemPID: 1000}
}

func (f *Fake) gui() string { return "gui/" + strconv.Itoa(f.UID) }

// Run is the Services.Run function.
func (f *Fake) Run(_ context.Context, argv ...string) ([]byte, error) {
	command := strings.Join(argv, " ")
	f.Calls = append(f.Calls, command)
	if f.Fail != "" && strings.Contains(command, f.Fail) {
		if f.FailSkip == 0 {
			f.Fail = ""
			return []byte("refused"), errors.New("exit status 1")
		}
		f.FailSkip--
	}
	if f.Rollback != "" && strings.Contains(command, f.Rollback) {
		return []byte("refused"), errors.New("exit status 1")
	}
	if f.NoDomain && strings.Contains(command, "launchctl") && strings.Contains(command, " "+f.gui()) {
		return []byte("Bad request.\nCould not find domain for user gui: " + strconv.Itoa(f.UID)), errors.New("exit status 112")
	}
	switch {
	case argv[0] == "launchctl" && len(argv) > 2 && strings.HasPrefix(argv[len(argv)-1], f.gui()):
		return []byte("Domain does not support specified action"), errors.New("exit status 125")
	case argv[0] == "launchctl" && len(argv) > 3 && strings.HasPrefix(argv[2], f.gui()):
		return []byte("Domain does not support specified action"), errors.New("exit status 125")
	case argv[0] == "plutil" && argv[1] == "-lint":
		return []byte(argv[2] + ": OK"), nil
	case argv[0] == "plutil" && f.PlistJSON != "":
		return []byte(f.PlistJSON), nil
	case argv[0] == "plutil":
		return plistJSON(argv[len(argv)-1])
	case argv[0] != "sudo":
		return nil, nil
	}
	a := argv[1:]
	agent, system := f.gui()+"/"+f.Label, "system/"+f.Label
	absent := func() ([]byte, error) {
		return []byte("Could not find service \"" + f.Label + "\""), errors.New("exit status 113")
	}
	switch {
	case a[0] == "install":
		data, err := os.ReadFile(a[len(a)-2])
		if err != nil {
			return []byte(err.Error()), err
		}
		return nil, os.WriteFile(a[len(a)-1], data, 0644)
	case a[0] == "rm":
		os.Remove(a[len(a)-1])
		return nil, nil
	case a[0] != "launchctl":
		return nil, nil
	}
	switch verb, target := a[1], a[len(a)-1]; {
	case verb == "print" && target == agent:
		if !f.AgentLoaded {
			return absent()
		}
		return []byte("state = running\n\tpid = " + strconv.Itoa(f.AgentPID) + "\n"), nil
	case verb == "print-disabled":
		state := "enabled"
		if f.AgentDisabled {
			state = "disabled"
		}
		return []byte("disabled services = {\n\t\"" + f.Label + "\" => " + state + "\n}\n"), nil
	case verb == "disable" && target == agent:
		f.AgentDisabled = true
	case verb == "enable" && target == agent:
		f.AgentDisabled = false
	case verb == "bootout" && target == agent:
		if !f.AgentLoaded {
			return []byte("Boot-out failed: 3: No such process"), errors.New("exit status 3")
		}
		f.AgentLoaded = false
	case verb == "bootstrap" && a[2] == f.gui():
		if f.AgentDisabled {
			return []byte("Bootstrap failed: 5: Input/output error"), errors.New("exit status 5")
		}
		f.AgentLoaded = true
	case verb == "bootstrap" && a[2] == "system":
		if f.SystemLoaded {
			return []byte("Bootstrap failed: 5: Input/output error"), errors.New("exit status 5")
		}
		f.SystemLoaded = true
	case verb == "kickstart" && target == system:
		if !f.SystemLoaded {
			return absent()
		}
		f.SystemPID++
	case verb == "print" && target == system:
		if !f.SystemLoaded {
			return absent()
		}
		return []byte("state = running\n\tpid = " + strconv.Itoa(f.SystemPID) + "\n"), nil
	case verb == "bootout" && target == system:
		if !f.SystemLoaded {
			return []byte("Boot-out failed: 3: No such process"), errors.New("exit status 3")
		}
		f.SystemLoaded = false
	}
	return nil, nil
}

var (
	plistString = regexp.MustCompile(`<key>(Label|UserName)</key>\s*<string>([^<]*)</string>`)
	plistArray  = regexp.MustCompile(`(?s)<key>ProgramArguments</key>\s*<array>(.*?)</array>`)
	arrayString = regexp.MustCompile(`<string>([^<]*)</string>`)
)

// plistJSON converts a plist that Koinon rendered into plutil's JSON for the fields that
// ReadSystem reads.
func plistJSON(path string) ([]byte, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return []byte(err.Error()), err
	}
	fields := map[string]any{}
	for _, m := range plistString.FindAllSubmatch(data, -1) {
		fields[string(m[1])] = html.UnescapeString(string(m[2]))
	}
	if m := plistArray.FindSubmatch(data); m != nil {
		var args []string
		for _, a := range arrayString.FindAllSubmatch(m[1], -1) {
			args = append(args, html.UnescapeString(string(a[1])))
		}
		fields["ProgramArguments"] = args
	}
	return json.Marshal(fields)
}

// StatusPID is the PID that the answering daemon reports.
func (f *Fake) StatusPID(context.Context) (int, error) {
	switch {
	case f.StatusDown:
		return 0, errors.New("connection refused")
	case f.Stale || !f.SystemLoaded:
		return f.AgentPID, nil
	}
	return f.SystemPID, nil
}
