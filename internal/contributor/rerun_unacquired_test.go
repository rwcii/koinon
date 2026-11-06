package contributor

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// fakeGH answers the script's gh calls from canned JSON files in $FAKE and applies each --jq
// filter with jq, so the test checks the script's own filters. With --paginate, it applies the
// filter to each page file (NAME.json, NAME-2.json, ...), as gh does. A file named
// fail-KIND makes that kind of call fail. It records a POST in $FAKE/posted.
const fakeGH = `#!/bin/sh
set -eu
filter=
post=no
endpoint=
while [ $# -gt 0 ]; do
	case $1 in
	api | --paginate) ;;
	--jq) filter=$2; shift ;;
	-X) [ "$2" = POST ] && post=yes; shift ;;
	*) endpoint=$1 ;;
	esac
	shift
done
if [ "$post" = yes ]; then
	echo "$endpoint" >> "$FAKE/posted"
	exit 0
fi
endpoint=${endpoint%%\?*}
case $endpoint in
*/attempts/*/jobs) name=jobs ;;
*/check-runs/*/annotations) id=${endpoint#*/check-runs/}; name=annotations-${id%/annotations} ;;
*/actions/runs/*) name=run ;;
*) echo "unexpected endpoint $endpoint" >&2; exit 2 ;;
esac
kind=${name%%-*}
if [ -e "$FAKE/fail-$kind" ]; then
	echo "HTTP 502: synthetic failure" >&2
	exit 1
fi
for page in "$FAKE/$name.json" "$FAKE/$name"-*.json; do
	[ -e "$page" ] || continue
	jq -r "$filter" "$page"
done
`

// A Tests or lifecycle run is rerun only when no runner acquired any of its failed jobs, at
// most twice, and never for a failure that ran, a stale event or an API error (#630; WDR 004,
// Decision 6).
func TestRerunUnacquired(t *testing.T) {
	if _, err := exec.LookPath("jq"); err != nil {
		t.Skip("jq is not installed")
	}
	workflow := read(t, ".github/workflows/rerun-unacquired.yml")
	for _, want := range []string{"workflow_run:", `workflows: ["Tests", "Native Go lifecycle evidence"]`, "types: [completed]",
		"github.event.workflow_run.conclusion == 'failure'", "github.event.workflow_run.run_attempt < 3", "actions: write", "checks: read",
		"contents: read", "ref: ${{ github.event.repository.default_branch }}", "persist-credentials: false", "timeout-minutes: 5",
		"sh scripts/rerun-unacquired.sh"} {
		if !strings.Contains(workflow, want) {
			t.Errorf("rerun-unacquired.yml lacks %q", want)
		}
	}
	const (
		unacquired = `[{"message":"Due to capacity constraints, jobs targeting macOS arm64 runners may experience longer queue times."},{"message":"The job was not acquired by Runner of type hosted even after multiple attempts"}]`
		other      = `[{"message":"The operation was canceled."}]`
		ran        = `{"id":4,"conclusion":"failure","runner_id":8,"steps":[{"name":"Run go test","conclusion":"failure"}]}`
		waiting    = `{"id":2,"conclusion":"cancelled","runner_id":0,"steps":[]}`
		failed     = `{"run_attempt":1,"status":"completed","conclusion":"failure"}`
	)
	for _, c := range []struct {
		name    string
		attempt string
		files   map[string]string
		rerun   bool
		fails   bool
	}{
		{"every failed job unacquired", "1", map[string]string{
			"jobs.json":          `{"jobs":[{"id":1,"conclusion":"success","runner_id":7,"steps":[{"conclusion":"success"}]},` + waiting + `,{"id":3,"conclusion":"failure","runner_id":null}]}`,
			"annotations-2.json": unacquired, "annotations-3.json": unacquired, "run.json": failed}, true, false},
		{"second attempt", "2", map[string]string{"jobs.json": `{"jobs":[` + waiting + `]}`, "annotations-2.json": unacquired,
			"run.json": `{"run_attempt":2,"status":"completed","conclusion":"failure"}`}, true, false},
		{"a failed job ran", "1", map[string]string{"jobs.json": `{"jobs":[` + waiting + `,` + ran + `]}`, "annotations-2.json": unacquired, "run.json": failed}, false, false},
		{"no runner but a step ran", "1", map[string]string{"jobs.json": `{"jobs":[{"id":5,"conclusion":"cancelled","runner_id":0,"steps":[{"conclusion":"cancelled"}]}]}`,
			"annotations-5.json": unacquired, "run.json": failed}, false, false},
		{"annotation that only contains the text", "1", map[string]string{"jobs.json": `{"jobs":[` + waiting + `]}`,
			"annotations-2.json": `[{"message":"Note: The job was not acquired by Runner of type hosted even after multiple attempts, then it ran"}]`, "run.json": failed}, false, false},
		{"another annotation", "1", map[string]string{"jobs.json": `{"jobs":[` + waiting + `]}`, "annotations-2.json": other, "run.json": failed}, false, false},
		{"annotation on the second page", "1", map[string]string{"jobs.json": `{"jobs":[` + waiting + `]}`, "annotations-2.json": other,
			"annotations-2-2.json": unacquired, "run.json": failed}, true, false},
		{"real failure on the second jobs page", "1", map[string]string{"jobs.json": `{"jobs":[` + waiting + `]}`, "jobs-2.json": `{"jobs":[` + ran + `]}`,
			"annotations-2.json": unacquired, "run.json": failed}, false, false},
		{"no failed job", "1", map[string]string{"jobs.json": `{"jobs":[{"id":1,"conclusion":"success","runner_id":7}]}`, "run.json": failed}, false, false},
		{"third attempt", "3", map[string]string{"jobs.json": `{"jobs":[` + waiting + `]}`, "annotations-2.json": unacquired,
			"run.json": `{"run_attempt":3,"status":"completed","conclusion":"failure"}`}, false, false},
		{"stale event after a rerun", "1", map[string]string{"jobs.json": `{"jobs":[` + waiting + `]}`, "annotations-2.json": unacquired,
			"run.json": `{"run_attempt":2,"status":"queued","conclusion":null}`}, false, false},
		{"run not completed", "1", map[string]string{"jobs.json": `{"jobs":[` + waiting + `]}`, "annotations-2.json": unacquired,
			"run.json": `{"run_attempt":1,"status":"in_progress","conclusion":null}`}, false, false},
		{"annotations API error", "1", map[string]string{"jobs.json": `{"jobs":[` + waiting + `]}`, "fail-annotations": "", "run.json": failed}, false, true},
		{"jobs API error", "1", map[string]string{"fail-jobs": "", "run.json": failed}, false, true},
		{"malformed jobs answer", "1", map[string]string{"jobs.json": `<html>`, "run.json": failed}, false, true},
	} {
		t.Run(c.name, func(t *testing.T) {
			dir := t.TempDir()
			bin := filepath.Join(dir, "bin")
			if err := os.Mkdir(bin, 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(bin, "gh"), []byte(fakeGH), 0o755); err != nil {
				t.Fatal(err)
			}
			for name, body := range c.files {
				if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
					t.Fatal(err)
				}
			}
			cmd := exec.Command("sh", filepath.Join(root(t), "scripts/rerun-unacquired.sh"))
			cmd.Env = append(os.Environ(), "PATH="+bin+string(os.PathListSeparator)+os.Getenv("PATH"),
				"FAKE="+dir, "REPO=example/synthetic", "RUN=42", "ATTEMPT="+c.attempt)
			out, err := cmd.CombinedOutput()
			if (err != nil) != c.fails {
				t.Fatalf("exit error %v, want failure %v\n%s", err, c.fails, out)
			}
			posted, _ := os.ReadFile(filepath.Join(dir, "posted"))
			if got := string(posted) == "repos/example/synthetic/actions/runs/42/rerun-failed-jobs\n"; got != c.rerun || !c.rerun && len(posted) != 0 {
				t.Fatalf("rerun %v (%q), want %v\n%s", got, posted, c.rerun, out)
			}
		})
	}
}
