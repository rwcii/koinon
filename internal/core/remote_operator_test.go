package core

import (
	"context"
	"encoding/json"
	"io"
	"strings"
	"testing"

	"github.com/rwcii/koinon/internal/remote"
)

const syntheticOperatorNext = "Tailscale refused this account. Run sudo tailscale set --operator=synthetic-user, then run koinon remote enable"

// operatorDenied refuses each Serve write as tailscaled does on Linux for an
// account that is neither root nor the operator (#652).
type operatorDenied struct{ base *syntheticTailscale }

func (o operatorDenied) Run(ctx context.Context, args ...string) ([]byte, error) {
	if len(args) > 1 && args[0] == "serve" && args[1] == "--bg" {
		return nil, &remote.Diagnostic{Code: remote.OperatorRequired, Next: syntheticOperatorNext}
	}
	return o.base.Run(ctx, args...)
}

func TestRemoteOperatorRefusalNamesTheStepInEnableAndStatus(t *testing.T) {
	d, _, fake := remoteFixture(t)
	d.remote.mu.Lock()
	d.remote.manager = remote.Manager{Adapter: operatorDenied{fake}}
	d.remote.mu.Unlock()
	response := request(t, d, "/v1/remote/enable", `{}`, d.secret)
	var refused struct {
		OK    bool   `json:"ok"`
		Code  string `json:"code"`
		Error string `json:"error"`
	}
	body, _ := io.ReadAll(response.Body)
	response.Body.Close()
	if err := json.Unmarshal(body, &refused); err != nil || response.StatusCode != 409 || refused.OK || refused.Code != remote.OperatorRequired || !strings.Contains(refused.Error, syntheticOperatorNext) {
		t.Fatalf("enable refusal: %d %s %v", response.StatusCode, body, err)
	}
	// A status read still succeeds; it reports the refusal and its next step.
	response = request(t, d, "/v1/remote/status", "", d.secret)
	body, _ = io.ReadAll(response.Body)
	response.Body.Close()
	var status struct {
		Serve string `json:"serve"`
		Error string `json:"error"`
	}
	if err := json.Unmarshal(body, &status); err != nil || response.StatusCode != 200 || status.Serve != "missing" || status.Error != remote.OperatorRequired+": "+syntheticOperatorNext {
		t.Fatalf("status: %d %s %v", response.StatusCode, body, err)
	}
}

func TestDashboardRemoteSetupShowsTheOperatorStepBeforeReady(t *testing.T) {
	d, root, fake := onboardingFixture(t)
	d.remote.mu.Lock()
	d.remote.manager = remote.Manager{Adapter: &onboardingTailscale{base: fake.base, complete: true}}
	d.remote.mu.Unlock()
	client := newActionClient(t, d, root)
	ready := dashboardDo(t, d, "GET", "/dashboard/remote/status?fragment=1&setup=1", client.cookie, nil, nil)
	if ready.status != 200 || strings.Contains(ready.body, "Allow Koinon to configure Tailscale") {
		t.Fatalf("operator step before any refusal: %d %s", ready.status, ready.body)
	}
	// HTTPS and the account approval are ready, so only the refused write remains.
	d.remote.mu.Lock()
	d.remote.manager = remote.Manager{Adapter: operatorDenied{fake.base}}
	d.remote.mu.Unlock()
	if err := d.remoteEnable(context.Background(), 0); err == nil {
		t.Fatal("enable succeeded through a refused Serve write")
	}
	d.remote.mu.Lock()
	d.remote.manager = remote.Manager{Adapter: &onboardingTailscale{base: fake.base, complete: true}}
	d.remote.mu.Unlock()
	data := d.dashboardRemoteState(context.Background(), true, "csrf")
	if !data.Operator || data.Ready {
		t.Fatalf("operator state: %+v", data)
	}
	got := dashboardDo(t, d, "GET", "/dashboard/remote/status?fragment=1&setup=1", client.cookie, nil, nil)
	if got.status != 200 || !strings.Contains(got.body, "Allow Koinon to configure Tailscale") || strings.Contains(got.body, "Your computer is ready") || strings.Contains(got.body, "Finish Tailscale setup") {
		t.Fatalf("operator step missing or not first: %d %s", got.status, got.body)
	}
	if strings.Count(got.body, `action="/dashboard/actions/remote-enable"`) != 1 {
		t.Fatalf("operator step needs exactly one enable action: %s", got.body)
	}
	if command := remote.OperatorCommand(); command != "" && !strings.Contains(got.body, "<code>"+command+"</code>") {
		t.Fatalf("operator command %q missing: %s", command, got.body)
	} else if command == "" && !strings.Contains(got.body, "outside a root shell, run <code>id -un</code>") {
		t.Fatalf("fallback step missing: %s", got.body)
	}
	if strings.Contains(got.body, "$USER") {
		t.Fatal("dashboard names $USER")
	}
}
