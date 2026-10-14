package core

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// refreshScript runs the dashboard's app.js in a Node VM with a synthetic list and a fetch
// that the script completes by hand, and prints one line per case.
const refreshScript = `
const fs = require('fs');
const vm = require('vm');
function run(name, script) {
  const state = { focused: false, requests: 0, complete: null };
  const input = { tagName: 'TEXTAREA' };
  // The list's disclosures: one with a form, one read-only with a data-key, one read-only
  // without. Replacing the list's HTML replaces them with closed ones.
  const fresh = () => [{ form: true, open: false }, { key: 'k', open: false }, { open: false }];
  let html = 'unsaved edit form';
  const list = {
    dataset: { refresh: '5000' }, details: fresh(),
    get innerHTML() { return html; },
    set innerHTML(v) { html = v; this.details = fresh(); },
    querySelector(selector) {
      const match = {
        'details[open]:not([data-key])': d => d.open && !d.key,
        'details[open] form': d => d.open && d.form,
        'form[data-busy]': () => false
      };
      return selector.split(', ').some(part => {
        if (!match[part]) { throw new Error('unmodelled selector ' + part); }
        return this.details.some(match[part]);
      }) ? {} : null;
    },
    querySelectorAll(selector) {
      const all = this.details.filter(d => d.key).map(d => ({ dataset: { key: d.key }, d,
        get open() { return d.open; }, set open(v) { d.open = v; } }));
      if (selector === 'details[data-key]') { return all; }
      if (selector === 'details[open][data-key]') { return all.filter(e => e.open); }
      throw new Error('unmodelled selector ' + selector);
    },
    contains(node) { return node === input; },
    removeAttribute() {}, setAttribute() {}
  };
  Object.defineProperty(state, 'open', { set(v) { list.details[0].open = v; } });
  let tick;
  const document = {
    getElementById() { return list; }, visibilityState: 'visible', addEventListener() {},
    get activeElement() { return state.focused ? input : null; }
  };
  const context = { document, URL, window: { location: { href: 'http://localhost/dashboard/memory' }, setInterval(f) { tick = f; } },
    fetch() { state.requests++; return new Promise(resolve => { state.complete = resolve; }); } };
  vm.runInNewContext(fs.readFileSync(process.argv[2], 'utf8'), context);
  return script(state, () => tick(), list).then(result => console.log(name + ' ' + result));
}
const answer = state => state.complete({ ok: true, text: async () => 'replacement fragment' });
const settle = () => new Promise(resolve => setImmediate(resolve));
(async () => {
  await run('idle', async (state, tick, list) => { tick(); answer(state); await settle(); return list.innerHTML; });
  await run('open-before', async (state, tick, list) => { state.open = true; tick(); return state.requests + ' ' + list.innerHTML; });
  await run('open-in-flight', async (state, tick, list) => { tick(); state.open = true; answer(state); await settle(); return list.innerHTML; });
  await run('focus-in-flight', async (state, tick, list) => { tick(); state.focused = true; answer(state); await settle(); return list.innerHTML; });
  await run('keyed-open', async (state, tick, list) => { list.details[1].open = true; tick(); answer(state); await settle(); return list.innerHTML + ' reopened ' + list.details[1].open; });
  await run('unkeyed-open', async (state, tick, list) => { list.details[2].open = true; tick(); return state.requests + ' ' + list.innerHTML; });
})();
`

// The 5-second refresh never replaces a list whose disclosure is open or whose form field
// has focus, also when the person opens it while a refresh is in flight (#243 review). An
// open read-only disclosure with a data-key does not hold it and is open again after (#373).
func TestRefreshKeepsFormsInUse(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		if os.Getenv("CI") != "" {
			t.Fatal("node required in CI")
		}
		t.Skip("node not installed")
	}
	dir := t.TempDir()
	script := filepath.Join(dir, "refresh.js")
	if err := os.WriteFile(script, []byte(refreshScript), 0600); err != nil {
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
	want := "idle replacement fragment\nopen-before 0 unsaved edit form\nopen-in-flight unsaved edit form\nfocus-in-flight unsaved edit form\n" +
		"keyed-open replacement fragment reopened true\nunkeyed-open 0 unsaved edit form\n"
	if string(out) != want {
		t.Fatalf("refresh:\n%s\nwant:\n%s", out, strings.TrimSpace(want))
	}
}
