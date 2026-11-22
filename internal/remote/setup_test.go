package remote

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"
)

type setupAdapter struct {
	response []byte
	err      error
	calls    [][]string
	cancel   context.CancelFunc
}

func (a *setupAdapter) Run(_ context.Context, args ...string) ([]byte, error) {
	a.calls = append(a.calls, append([]string(nil), args...))
	if a.cancel != nil {
		a.cancel()
	}
	return a.response, a.err
}
func setupResponse(complete bool, url string, wait bool) []byte {
	data, _ := json.Marshal(map[string]any{"Complete": complete, "URL": url, "ShouldWait": wait, "Text": "synthetic private provider prose\u001b[31m"})
	return data
}

const syntheticSetupURL = "https://login.tailscale.com/f/serve?node=Synthetic-Node_123"

func TestSetupNativeQueryIsReadOnlyAndReturnsOnlyValidatedFields(t *testing.T) {
	for _, tc := range []struct {
		name     string
		complete bool
		url      string
		wait     bool
	}{
		{"activation", false, syntheticSetupURL, true},
		{"provider does not wait", false, syntheticSetupURL, false},
		{"already complete without URL", true, "", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			a := &setupAdapter{response: setupResponse(tc.complete, tc.url, tc.wait)}
			got, err := (Manager{Adapter: a}).Setup(context.Background())
			if err != nil || got != (Setup{Complete: tc.complete, URL: tc.url, ShouldWait: tc.wait}) {
				t.Fatalf("got %+v: %v", got, err)
			}
			want := [][]string{{"debug", "localapi", "POST", "/localapi/v0/query-feature?feature=serve"}}
			if !reflect.DeepEqual(a.calls, want) {
				t.Fatalf("unexpected command: %v", a.calls)
			}
			encoded, _ := json.Marshal(got)
			if strings.Contains(string(encoded), "provider prose") || strings.Contains(string(encoded), "Text") {
				t.Fatal("raw Text escaped setup response")
			}
		})
	}
}

func TestSetupRejectsMissingMalformedAndHostileResponses(t *testing.T) {
	urls := []string{
		"", "http://login.tailscale.com/f/serve?node=Synthetic", "https://evil.example/f/serve?node=Synthetic",
		"https://login.tailscale.com.evil.example/f/serve?node=Synthetic", "https://user@login.tailscale.com/f/serve?node=Synthetic",
		"https://login.tailscale.com:443/f/serve?node=Synthetic", "https://login.tailscale.com/f/serve/?node=Synthetic",
		"https://login.tailscale.com/f/funnel?node=Synthetic", "https://login.tailscale.com/f/serve?node=",
		"https://login.tailscale.com/f/serve?node=Synthetic#fragment", "https://login.tailscale.com/f/serve?node=Synthetic#",
		"https://login.tailscale.com/f/serve?node=Synthetic&node=Other", "https://login.tailscale.com/f/serve?node=Synthetic&extra=value",
		"https://login.tailscale.com/f/serve?node=Synthetic%0aInjected", "https://login.tailscale.com/f/serve?node=Synthetic%1bInjected",
		"https://login.tailscale.com/f/serve?node=Synthetic\nInjected", "https://login.tailscale.com/f/serve?node=Synthetic\x00Injected",
		"https://login.tailscale.com/f/serve?node=Synthetic;extra=value", "https://login.tailscale.com/f/serve?node=Synthetic+Node",
		"https://login.tailscale.com/f/serve?node=Synthetic/Node", "https://login.tailscale.com/f/serve?node=" + strings.Repeat("x", 257),
		"https://login.tailscale.com/f/serve?node=" + strings.Repeat("x", 2050), "https://LOGIN.tailscale.com/f/serve?node=Synthetic",
	}
	for i, url := range urls {
		a := &setupAdapter{response: setupResponse(false, url, true)}
		got, err := (Manager{Adapter: a}).Setup(context.Background())
		assertSetupErrorSafe(t, i, got, err)
	}
	for i, raw := range []string{"", "null", "[]", "not JSON synthetic private details", "{}", "{} {}", `{"Complete":"yes"}`, `{"Complete":false,"URL":23}`, `{"Complete":true,"ShouldWait":"true"}`, strings.Repeat("x", maxOutput+1)} {
		a := &setupAdapter{response: []byte(raw)}
		got, err := (Manager{Adapter: a}).Setup(context.Background())
		assertSetupErrorSafe(t, i, got, err)
	}
	// Completion never makes an unvalidated URL eligible for display.
	a := &setupAdapter{response: setupResponse(true, "https://evil.example", false)}
	got, err := (Manager{Adapter: a}).Setup(context.Background())
	assertSetupErrorSafe(t, 0, got, err)
}
func assertSetupErrorSafe(t *testing.T, caseID int, got Setup, err error) {
	t.Helper()
	if got != (Setup{}) || code(err) != "tailscale_setup_unavailable" {
		t.Fatalf("case %d: got %+v, error %v", caseID, got, err)
	}
	if !strings.Contains(err.Error(), HTTPSURL) {
		t.Fatalf("case %d: missing official fallback", caseID)
	}
	for _, private := range []string{"Synthetic", "private details", "provider prose", "evil.example", "node="} {
		if strings.Contains(err.Error(), private) {
			t.Fatalf("case %d: error leaked provider data", caseID)
		}
	}
}

func TestSetupSanitizesNativeCommandFailures(t *testing.T) {
	for _, failure := range []error{errors.New("synthetic private stderr and account values"), &Diagnostic{Code: "synthetic raw code", Next: "synthetic private provider prose"}} {
		a := &setupAdapter{response: setupResponse(false, syntheticSetupURL, true), err: failure}
		got, err := (Manager{Adapter: a}).Setup(context.Background())
		assertSetupErrorSafe(t, 0, got, err)
		if strings.Contains(err.Error(), "synthetic") {
			t.Fatal("native failure leaked")
		}
	}
}
func TestSetupCancellationStopsBeforeOrAfterNativeQuery(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	a := &setupAdapter{response: setupResponse(false, syntheticSetupURL, true)}
	got, err := (Manager{Adapter: a}).Setup(ctx)
	if got != (Setup{}) || !errors.Is(err, context.Canceled) || len(a.calls) != 0 {
		t.Fatalf("pre-canceled: %+v %v calls=%d", got, err, len(a.calls))
	}
	ctx, cancel = context.WithCancel(context.Background())
	defer cancel()
	a = &setupAdapter{response: setupResponse(false, syntheticSetupURL, true), cancel: cancel}
	got, err = (Manager{Adapter: a}).Setup(ctx)
	if got != (Setup{}) || !errors.Is(err, context.Canceled) || len(a.calls) != 1 {
		t.Fatalf("canceled query: %+v %v calls=%d", got, err, len(a.calls))
	}
}
