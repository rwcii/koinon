package core

import (
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
)

// submitScript runs app.js in a Node VM and submits a form whose "action" and "method"
// properties are form controls, as a browser makes them for a control of that name: the
// Sessions bulk form has <select name="action"> (#654). It prints the fetched URL.
const submitScript = `
const fs = require('fs');
const vm = require('vm');
let submit;
const document = { getElementById() { return null; }, visibilityState: 'visible',
  addEventListener(type, f) { if (type === 'submit') { submit = f; } } };
const control = { tagName: 'SELECT', toString() { return '[object HTMLSelectElement]'; } };
const form = (attributes) => ({ action: control, method: control, dataset: {},
  getAttribute(name) { return name in attributes ? attributes[name] : null; },
  querySelectorAll() { return []; } });
const fetched = [];
vm.runInNewContext(fs.readFileSync(process.argv[2], 'utf8'), { document, URL, URLSearchParams,
  FormData: class { append() {} }, AbortController,
  fetch(url) { fetched.push(url); return new Promise(() => {}); },
  window: { location: { href: 'https://host.example.ts.net/dashboard/sessions', origin: 'https://host.example.ts.net' },
    setInterval() {}, setTimeout() { return 0; }, clearTimeout() {}, addEventListener() {} } });
const send = (f, submitter) => submit({ target: f, submitter, preventDefault() { f.prevented = true; } });
const bulk = form({ method: 'post', action: '/dashboard/actions/bulk' });
send(bulk, null);
console.log('bulk ' + fetched.pop());
const choice = form({ method: 'post', action: '/dashboard/actions/decision' });
send(choice, { name: '', getAttribute(name) { return name === 'formaction' ? '/dashboard/actions/decision-approve' : null; } });
console.log('formaction ' + fetched.pop());
const filter = form({ method: 'get', action: '/dashboard/sessions' });
send(filter, null);
console.log('get ' + (filter.prevented === true) + ' ' + fetched.length);
`

// A form control named like a form property hides that property in a browser, so the
// dashboard script reads the action and method attributes (#654).
func TestSubmitUsesTheFormAttributes(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		if os.Getenv("CI") != "" {
			t.Fatal("node required in CI")
		}
		t.Skip("node not installed")
	}
	dir := t.TempDir()
	script := filepath.Join(dir, "submit.js")
	if err := os.WriteFile(script, []byte(submitScript), 0600); err != nil {
		t.Fatal(err)
	}
	app, err := filepath.Abs(filepath.Join("web", "static", "app.js"))
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(node, script, app)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("node: %v %s", err, out)
	}
	want := "bulk https://host.example.ts.net/dashboard/actions/bulk\n" +
		"formaction https://host.example.ts.net/dashboard/actions/decision-approve\n" +
		"get false 0\n"
	if string(out) != want {
		t.Fatalf("submit:\n%s\nwant:\n%s", out, want)
	}
}

// No template names a form control after a property that a dashboard script reads from a
// form, because the control would hide that property (#654).
func TestNoControlHidesAFormPropertyThatAScriptReads(t *testing.T) {
	read := map[string]bool{}
	scripts, err := filepath.Glob(filepath.Join("web", "static", "*.js"))
	if err != nil || len(scripts) == 0 {
		t.Fatal(scripts, err)
	}
	for _, path := range scripts {
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		for _, m := range regexp.MustCompile(`\bform\.([A-Za-z_$][A-Za-z0-9_$]*)`).FindAllStringSubmatch(string(data), -1) {
			read[m[1]] = true
		}
	}
	if !read["getAttribute"] {
		t.Fatalf("the submit handler no longer reads form attributes: %v", read)
	}
	templates, err := filepath.Glob(filepath.Join("web", "*.html"))
	if err != nil || len(templates) == 0 {
		t.Fatal(templates, err)
	}
	control := regexp.MustCompile(`<(?:input|select|textarea|button|fieldset|output|object)\b[^>]*?\b(?:name|id)="([^"{]+)"`)
	var hidden []string
	for _, path := range templates {
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		for _, m := range control.FindAllStringSubmatch(string(data), -1) {
			if read[m[1]] {
				hidden = append(hidden, filepath.Base(path)+": "+m[1])
			}
		}
	}
	sort.Strings(hidden)
	if len(hidden) > 0 {
		t.Fatalf("controls hide form properties that a script reads: %s", strings.Join(hidden, ", "))
	}
}
