package install

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rwcii/koinon/internal/core"
)

// script answers the terminal questions in order and ends the input after the last answer.
type script struct {
	t       *testing.T
	answers []string
	prompts []string
}

func (s *script) line(prompt string) (string, error) {
	s.prompts = append(s.prompts, prompt)
	if len(s.answers) == 0 {
		return "", io.EOF
	}
	answer := s.answers[0]
	s.answers = s.answers[1:]
	return answer, nil
}

// questions sets up synthetic host addresses and a resolver that accepts only those
// addresses (documentation ranges), and returns the script and the terminal output.
func questions(e *env, answers ...string) (*script, *bytes.Buffer) {
	s := &script{answers: answers}
	var told bytes.Buffer
	e.o.Line, e.o.Tell = s.line, &told
	e.o.HostAddresses = func() ([]string, error) { return []string{"192.0.2.10", "2001:db8::10"}, nil }
	e.o.ResolveDashboard = func(_ context.Context, address string) (core.DashboardAddress, error) {
		switch address {
		case "192.0.2.10:8443", "192.0.2.10:9443":
			return core.DashboardAddress{Host: "192.0.2.10", IP: "192.0.2.10", Port: 8443}, nil
		case "[2001:db8::10]:8443":
			return core.DashboardAddress{Host: "2001:db8::10", IP: "2001:db8::10", Port: 8443}, nil
		case "board.example.test:8443":
			return core.DashboardAddress{Host: "board.example.test", IP: "192.0.2.10", Port: 8443}, nil
		}
		return core.DashboardAddress{}, core.Refusal{Code: "dashboard_address_unavailable", Message: "the dashboard address is not an address of this host"}
	}
	return s, &told
}

func saved(t *testing.T, e *env) (*core.DashboardAddress, bool) {
	t.Helper()
	a, err := core.ReadDashboardAddress(e.o.StateDir)
	if err != nil {
		t.Fatal(err)
	}
	local, err := core.ReadDashboardLocal(e.o.StateDir)
	if err != nil {
		t.Fatal(err)
	}
	return a, local
}

// The local answer, also the empty default, is saved apart from the network setting, so a
// later install asks nothing; --dashboard-address none saves the same choice (#624 a, b).
func TestInstallDashboardQuestionLocal(t *testing.T) {
	for _, answer := range []string{"", "1"} {
		e := newEnv(t, "systemd")
		s, told := questions(e, answer)
		r, err := Install(context.Background(), e.o)
		if err != nil || !r.OK || r.DashboardNetwork != nil {
			t.Fatalf("install %+v %v", r, err)
		}
		if !strings.Contains(told.String(), "Where should the dashboard open?") || len(s.prompts) != 1 {
			t.Fatalf("question %q %q", told, s.prompts)
		}
		if a, local := saved(t, e); a != nil || !local {
			t.Fatalf("saved %+v %v", a, local)
		}
		info, err := os.Lstat(filepath.Join(e.o.StateDir, "dashboard-local.json"))
		if err != nil || info.Mode().Perm() != 0600 {
			t.Fatalf("marker %v %v", info, err)
		}
		s.prompts = nil
		if _, err := Install(context.Background(), e.o); err != nil || len(s.prompts) != 0 {
			t.Fatalf("asked again %q %v", s.prompts, err)
		}
	}
	e := newEnv(t, "systemd")
	e.o.DashboardAddress = "none"
	if _, err := Install(context.Background(), e.o); err != nil {
		t.Fatal(err)
	}
	if _, local := saved(t, e); !local {
		t.Fatal("--dashboard-address none saved no local choice")
	}
	s, _ := questions(e)
	e.o.DashboardAddress = ""
	if _, err := Install(context.Background(), e.o); err != nil || len(s.prompts) != 0 {
		t.Fatalf("asked after none %q %v", s.prompts, err)
	}
	// An explicit network setting wins over the saved local choice.
	e.o.DashboardAddress = "192.0.2.10:8443"
	if _, err := Install(context.Background(), e.o); err != nil {
		t.Fatal(err)
	}
	if a, _ := saved(t, e); a == nil || a.IP != "192.0.2.10" {
		t.Fatalf("explicit setting %+v", a)
	}
}

// An unsafe local-choice file is reported, not trusted.
func TestInstallDashboardLocalMarkerUnsafe(t *testing.T) {
	e := newEnv(t, "systemd")
	s, _ := questions(e, "1")
	path := filepath.Join(e.o.StateDir, "dashboard-local.json")
	if err := os.WriteFile(path, []byte(`{"choice":"local"}`), 0644); err != nil {
		t.Fatal(err)
	}
	if _, err := Install(context.Background(), e.o); err == nil || !strings.Contains(err.Error(), "dashboard_configuration_invalid") || len(s.prompts) != 0 {
		t.Fatalf("unsafe marker %v %q", err, s.prompts)
	}
}

// No question with a saved network setting, an explicit flag, an upgrade, a plan or without a
// terminal (#624 f).
func TestInstallDashboardQuestionsSkipped(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t, "systemd")
	s, _ := questions(e)
	e.o.DashboardAddress = "192.0.2.10:8443"
	if _, err := Install(ctx, e.o); err != nil || len(s.prompts) != 0 {
		t.Fatalf("flag asked %q %v", s.prompts, err)
	}
	e.o.DashboardAddress = ""
	if _, err := Install(ctx, e.o); err != nil || len(s.prompts) != 0 {
		t.Fatalf("saved setting asked %q %v", s.prompts, err)
	}

	e = newEnv(t, "systemd")
	s, _ = questions(e, "3")
	e.o.Upgrade = true
	if _, err := Install(ctx, e.o); err != nil || len(s.prompts) != 0 {
		t.Fatalf("upgrade asked %q %v", s.prompts, err)
	}
	e.o.Upgrade = false
	status := e.o.Status
	e.o.Status = func(context.Context, string, string) (json.RawMessage, error) {
		return json.RawMessage(`{"maintainer_secret_set":false}`), nil
	}
	if _, err := Plan(ctx, e.o); err != nil || len(s.prompts) != 0 {
		t.Fatalf("plan asked %q %v", s.prompts, err)
	}
	e.o.Line, e.o.Status = nil, status
	if _, err := Install(ctx, e.o); err != nil {
		t.Fatal(err)
	}
	if a, local := saved(t, e); a != nil || local {
		t.Fatalf("no terminal saved %+v %v", a, local)
	}
}

// Ended input at any question saves no choice, so the next install asks again (#624 b, c).
func TestInstallDashboardQuestionsEndedInput(t *testing.T) {
	for _, answers := range [][]string{{}, {"3"}, {"3", "1"}} {
		e := newEnv(t, "systemd")
		s, _ := questions(e, answers...)
		r, err := Install(context.Background(), e.o)
		if err != nil || !r.OK || r.DashboardNetwork != nil {
			t.Fatalf("%q: install %+v %v", answers, r, err)
		}
		if a, local := saved(t, e); a != nil || local {
			t.Fatalf("%q: saved %+v %v", answers, a, local)
		}
		if len(s.prompts) != len(answers)+1 {
			t.Fatalf("%q: prompts %q", answers, s.prompts)
		}
		s.answers = []string{"1"}
		if _, err := Install(context.Background(), e.o); err != nil || len(s.prompts) != len(answers)+2 {
			t.Fatalf("%q: not asked again %q %v", answers, s.prompts, err)
		}
	}
}

// A network answer lists this host's addresses; a refused answer is shown with its reason
// and asked again; a number, an FQDN and the default port are accepted (#624 c, f).
func TestInstallDashboardQuestionsNetwork(t *testing.T) {
	for _, tc := range []struct {
		answers []string
		host    string
	}{
		{[]string{"3", "1", ""}, "192.0.2.10"},
		{[]string{"3", "2", "8443"}, "2001:db8::10"},
		{[]string{"x", "3", "7", "198.51.100.7", "", "board.example.test", ""}, "board.example.test"},
	} {
		e := newEnv(t, "systemd")
		e.o.NoStart = true
		s, told := questions(e, tc.answers...)
		r, err := Install(context.Background(), e.o)
		if err != nil || !r.OK {
			t.Fatalf("%q: install %+v %v", tc.answers, r, err)
		}
		a, local := saved(t, e)
		if a == nil || a.Host != tc.host || local {
			t.Fatalf("%q: saved %+v %v\n%s", tc.answers, a, local, told)
		}
		out := told.String()
		if !strings.Contains(out, "  1. 192.0.2.10\n  2. 2001:db8::10\n") || len(s.answers) != 0 {
			t.Fatalf("%q: list %q left %q", tc.answers, out, s.answers)
		}
		if len(tc.answers) > 3 && (!strings.Contains(out, "Type 1, 2 or 3.") || !strings.Contains(out, "There is no address 7.") ||
			!strings.Contains(out, "This address is refused: dashboard_address_unavailable") || strings.Count(out, "  1. 192.0.2.10") != 3) {
			t.Fatalf("refusals not shown or not asked again: %s", out)
		}
		// --no-start saves the address and asks for no secret.
		if strings.Contains(out, "needs the maintainer secret") || r.Service != "staged" {
			t.Fatalf("%q: no-start offered the secret or started: %s %+v", tc.answers, out, r)
		}
	}
}

// fakeNetworkDaemon is a background daemon whose status shows the network dashboard as the
// real daemon does: it opens only when the secret was set before its start.
type fakeNetworkDaemon struct {
	*fakeDaemon
	secretSet bool
	listening map[int]bool
	actions   []string
	failStart bool
}

func newNetworkDaemon(t *testing.T, e *env) *fakeNetworkDaemon {
	d := &fakeNetworkDaemon{fakeDaemon: newDaemon(e, "v1"), listening: map[int]bool{}}
	e.r.fail = func([]string) bool { return true } // no service manager: the background path
	status := e.o.Status
	e.o.Status = func(ctx context.Context, address, secret string) (json.RawMessage, error) {
		data, err := status(ctx, address, secret)
		if err != nil {
			return nil, err
		}
		var s map[string]any
		if err := json.Unmarshal(data, &s); err != nil {
			return nil, err
		}
		s["maintainer_secret_set"] = d.secretSet
		if a, _ := core.ReadDashboardAddress(e.o.StateDir); a != nil {
			n := core.DashboardNetworkStatus{Address: a.Address(), Bind: a.Bind(), URL: "https://" + a.Address() + "/dashboard/"}
			switch {
			case d.listening[d.pid]:
				n.Code, n.Fingerprint = "running", "AA:BB"
			case !d.secretSet:
				n.Code, n.Step = "dashboard_secret_required", "Set the maintainer secret."
			default:
				n.Code, n.Step = "dashboard_restart_required", "Run koinon install."
			}
			s["dashboard_network"] = n
		}
		return json.Marshal(s)
	}
	start := e.o.Background
	e.o.Background = func(binary, state, log string) (Started, error) {
		if d.failStart && len(d.starts) > 0 {
			return Started{}, errors.New("synthetic start failure")
		}
		started, err := start(binary, state, log)
		d.listening[d.pid] = d.secretSet
		return started, err
	}
	e.o.MaintainerSecret = func(_ context.Context, _, bearer, action, secret string) (string, error) {
		if bearer != strings.Repeat("ab", 32) {
			t.Fatalf("bearer %q", bearer)
		}
		d.actions = append(d.actions, action+" "+secret)
		d.secretSet = true
		if action == "generate" {
			return "synthetic-generated-secret", nil
		}
		return "", nil
	}
	e.o.NewSecret = func() (string, error) { return "synthetic typed secret", nil }
	return d
}

// Without a secret, install asks to set one, sets it through the daemon's route and restarts
// the daemon once; the report has the status after the restart, and a generated secret is
// shown only on the terminal (#624 d, e).
func TestInstallDashboardSecretAndRestart(t *testing.T) {
	for _, tc := range []struct {
		choice, action string
	}{{"", "set synthetic typed secret"}, {"2", "generate "}} {
		e := newEnv(t, "systemd")
		d := newNetworkDaemon(t, e)
		_, told := questions(e, "3", "1", "", tc.choice)
		r, err := Install(context.Background(), e.o)
		if err != nil || !r.OK {
			t.Fatalf("install %+v %v", r, err)
		}
		if len(d.starts) != 2 || len(d.stops) != 1 || d.stops[0] != d.starts[0] || len(d.actions) != 1 || d.actions[0] != tc.action {
			t.Fatalf("starts %v stops %v actions %q", d.starts, d.stops, d.actions)
		}
		if r.DashboardNetwork == nil || r.DashboardNetwork.Code != "running" || r.DashboardNetwork.Fingerprint != "AA:BB" {
			t.Fatalf("status after restart %+v", r.DashboardNetwork)
		}
		out := told.String()
		if !strings.Contains(out, "https://192.0.2.10:8443/dashboard/") || !strings.Contains(out, "AA:BB") || !strings.Contains(out, "Compare it once") {
			t.Fatalf("terminal %s", out)
		}
		report, _ := json.Marshal(r)
		if strings.Contains(string(report), "synthetic") {
			t.Fatalf("a secret is in the report: %s", report)
		}
		if tc.choice == "2" && !strings.Contains(out, "  synthetic-generated-secret\n") {
			t.Fatalf("generated secret not shown: %s", out)
		}
	}
}

// A secret already set opens the listener at the first start: no question, no restart.
func TestInstallDashboardSecretAlreadySet(t *testing.T) {
	e := newEnv(t, "systemd")
	d := newNetworkDaemon(t, e)
	d.secretSet = true
	_, told := questions(e, "3", "1", "")
	r, err := Install(context.Background(), e.o)
	if err != nil || r.DashboardNetwork == nil || r.DashboardNetwork.Code != "running" || len(d.starts) != 1 || len(d.actions) != 0 {
		t.Fatalf("install %+v %v starts %v actions %q", r.DashboardNetwork, err, d.starts, d.actions)
	}
	if strings.Contains(told.String(), "needs the maintainer secret") {
		t.Fatalf("asked for a secret: %s", told)
	}
}

// Declining, ended input and a mistyped secret leave the network setting saved and report
// dashboard_secret_required with its step, without a restart (#624 d).
func TestInstallDashboardSecretDeclined(t *testing.T) {
	for _, answers := range [][]string{{"3"}, {}, {"9", "3"}} {
		e := newEnv(t, "systemd")
		d := newNetworkDaemon(t, e)
		_, told := questions(e, append([]string{"3", "1", ""}, answers...)...)
		r, err := Install(context.Background(), e.o)
		if err != nil || !r.OK || r.DashboardNetwork == nil || r.DashboardNetwork.Code != "dashboard_secret_required" || r.DashboardNetwork.Step == "" {
			t.Fatalf("%q: install %+v %v", answers, r.DashboardNetwork, err)
		}
		if a, _ := saved(t, e); a == nil || len(d.starts) != 1 || len(d.actions) != 0 {
			t.Fatalf("%q: saved %+v starts %v actions %q", answers, a, d.starts, d.actions)
		}
		if !strings.Contains(told.String(), "is not open: dashboard_secret_required") {
			t.Fatalf("%q: terminal %s", answers, told)
		}
	}
	// A refused entry is shown and asked again.
	e := newEnv(t, "systemd")
	d := newNetworkDaemon(t, e)
	entries := []error{errors.New("the two entries differ; nothing was changed"), nil}
	e.o.NewSecret = func() (string, error) {
		err := entries[0]
		entries = entries[1:]
		if err != nil {
			return "", err
		}
		return "short", nil
	}
	_, told := questions(e, "3", "1", "", "1", "1", "3")
	if _, err := Install(context.Background(), e.o); err != nil || len(d.actions) != 0 {
		t.Fatalf("refused entries %v %q", err, d.actions)
	}
	if out := told.String(); !strings.Contains(out, "The secret is not set: the two entries differ") || !strings.Contains(out, "at least 12 characters") {
		t.Fatalf("terminal %s", out)
	}
}

// A failed restart is reported with its step, never as running (#624 e).
func TestInstallDashboardRestartFailure(t *testing.T) {
	e := newEnv(t, "systemd")
	d := newNetworkDaemon(t, e)
	d.failStart = true
	questions(e, "3", "1", "", "2")
	r, err := Install(context.Background(), e.o)
	if err == nil || !strings.Contains(err.Error(), "daemon_restart_failed") || r.OK {
		t.Fatalf("install %+v %v", r, err)
	}
	if r.DashboardNetwork == nil || r.DashboardNetwork.Code != "dashboard_restart_failed" || !strings.Contains(r.DashboardNetwork.Step, "install") || r.Service != "restart_failed" {
		t.Fatalf("report %+v %q", r.DashboardNetwork, r.Service)
	}
}

// With a service manager, the restart goes through the manager once more, and the install
// waits for the new process before it reads the status (#624 e).
func TestInstallDashboardRestartThroughManager(t *testing.T) {
	for _, backend := range []string{"systemd", "launchd"} {
		t.Run(backend, func(t *testing.T) {
			e := newEnv(t, backend)
			pid, secretSet, listening, restarts, fail := 100, false, false, 0, false
			run := e.o.Services.Run
			e.o.Services.Run = func(ctx context.Context, argv ...string) ([]byte, error) {
				command := strings.Join(argv, " ")
				if strings.Contains(command, "systemctl --user restart") || strings.Contains(command, "launchctl kickstart") {
					if fail && restarts > 0 {
						return []byte("refused"), errors.New("exit 1")
					}
					restarts++
					pid++
					listening = secretSet
				}
				return run(ctx, argv...)
			}
			e.o.Status = func(context.Context, string, string) (json.RawMessage, error) {
				n := core.DashboardNetworkStatus{URL: "https://192.0.2.10:8443/dashboard/", Code: "dashboard_secret_required"}
				if listening {
					n.Code, n.Fingerprint = "running", "CC:DD"
				}
				return json.Marshal(map[string]any{"pid": pid, "dashboard_network": n})
			}
			e.o.MaintainerSecret = func(context.Context, string, string, string, string) (string, error) {
				secretSet = true
				return "synthetic-generated-secret", nil
			}
			questions(e, "3", "1", "", "2")
			r, err := Install(context.Background(), e.o)
			if err != nil || restarts != 2 || r.DashboardNetwork == nil || r.DashboardNetwork.Code != "running" || r.Service != "running" {
				t.Fatalf("install %+v %v restarts %d", r.DashboardNetwork, err, restarts)
			}
			// A refused restart reports the failure.
			e2 := newEnv(t, backend)
			e2.o.Services.Run, e2.o.Status, e2.o.MaintainerSecret = e.o.Services.Run, e.o.Status, e.o.MaintainerSecret
			pid, secretSet, listening, restarts, fail = 100, false, false, 0, true
			questions(e2, "3", "1", "", "2")
			r, err = Install(context.Background(), e2.o)
			if err == nil || !strings.Contains(err.Error(), "daemon_restart_failed") || r.DashboardNetwork == nil || r.DashboardNetwork.Code != "dashboard_restart_failed" {
				t.Fatalf("refused restart %+v %v", r.DashboardNetwork, err)
			}
		})
	}
}

// A cancel, as after Ctrl-C, saves no answer and ends the install (#624 b).
func TestInstallDashboardQuestionCancel(t *testing.T) {
	for _, answers := range [][]string{{}, {"3"}, {"3", "1"}} {
		e := newEnv(t, "systemd")
		s, _ := questions(e, answers...)
		e.o.Line = func(prompt string) (string, error) {
			if len(s.answers) == 0 {
				return "", context.Canceled
			}
			return s.line(prompt)
		}
		if _, err := Install(context.Background(), e.o); !errors.Is(err, context.Canceled) {
			t.Fatalf("%q: %v", answers, err)
		}
		if a, local := saved(t, e); a != nil || local {
			t.Fatalf("%q: saved %+v %v", answers, a, local)
		}
		if _, err := os.Stat(e.o.Services.DaemonArtifact()); err == nil {
			t.Fatalf("%q: the install went on after the cancel", answers)
		}
	}
}
