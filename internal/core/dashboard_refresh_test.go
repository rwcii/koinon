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
  // The list's form fields as loaded: a note, a hidden field, a select and a checkbox.
  const fields = () => [{ tagName: 'TEXTAREA', type: 'textarea', value: '', defaultValue: '' },
    { tagName: 'INPUT', type: 'hidden', value: 'csrf', defaultValue: 'csrf' },
    { tagName: 'SELECT', options: [{ selected: true, defaultSelected: true }, { selected: false, defaultSelected: false }] },
    { tagName: 'INPUT', type: 'checkbox', checked: false, defaultChecked: false }];
  let html = 'unsaved edit form';
  const list = {
    dataset: { refresh: '5000' }, details: fresh(), fields: fields(), checked: false, held: false,
    get innerHTML() { return html; },
    set innerHTML(v) { html = v; this.details = fresh(); this.fields = fields(); },
    querySelector(selector) {
      const match = {
        'details[open]:not([data-key])': d => d.open && !d.key,
        'details[open] form': d => d.open && d.form,
        'form[data-busy]': () => false
      };
      return selector.split(', ').some(part => {
        if (part === 'input[form=bulk]:checked') { return this.checked; }
        if (!match[part]) { throw new Error('unmodelled selector ' + part); }
        return this.details.some(match[part]);
      }) ? {} : null;
    },
    querySelectorAll(selector) {
      const all = this.details.filter(d => d.key).map(d => ({ dataset: { key: d.key }, d,
        get open() { return d.open; }, set open(v) { d.open = v; } }));
      if (selector === 'details[data-key]') { return all; }
      if (selector === 'details[open][data-key]') { return all.filter(e => e.open); }
      if (selector === 'input, textarea, select') { return this.fields; }
      throw new Error('unmodelled selector ' + selector);
    },
    contains(node) { return node === input; },
    removeAttribute(name) { if (name === 'data-held') { this.held = false; } },
    setAttribute(name) { if (name === 'data-held') { this.held = true; } }
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
  await run('checked-before', async (state, tick, list) => { list.checked = true; tick(); return state.requests + ' ' + list.innerHTML; });
  await run('checked-in-flight', async (state, tick, list) => { tick(); list.checked = true; answer(state); await settle(); return list.innerHTML; });
  // A typed note, a changed select or a changed checkbox holds the refresh without focus, and
  // the list is marked as held; unchanged fields do not hold it (#510).
  await run('typed-unfocused', async (state, tick, list) => { list.fields[0].value = 'my note'; tick(); return state.requests + ' ' + list.innerHTML + ' held ' + list.held; });
  await run('typed-in-flight', async (state, tick, list) => { tick(); list.fields[0].value = 'my note'; answer(state); await settle(); return list.innerHTML + ' held ' + list.held; });
  await run('select-changed', async (state, tick, list) => { list.fields[2].options[0].selected = false; list.fields[2].options[1].selected = true; tick(); return state.requests + ' ' + list.innerHTML; });
  await run('checkbox-changed', async (state, tick, list) => { list.fields[3].checked = true; tick(); return state.requests + ' ' + list.innerHTML; });
  await run('typed-then-cleared', async (state, tick, list) => { list.fields[0].value = 'x'; tick(); list.fields[0].value = ''; tick(); answer(state); await settle(); return list.innerHTML + ' held ' + list.held; });
})();
`

// The 5-second refresh never replaces a list whose disclosure is open or whose form field
// has focus, also when the person opens it while a refresh is in flight (#243 review). An
// open read-only disclosure with a data-key does not hold it and is open again after (#373).
// A session selected for a bulk action holds it, so the selection stays (#429). A field with
// input not yet sent holds it also without focus, and the list is marked held (#510).
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
		"keyed-open replacement fragment reopened true\nunkeyed-open 0 unsaved edit form\n" +
		"checked-before 0 unsaved edit form\nchecked-in-flight unsaved edit form\n" +
		"typed-unfocused 0 unsaved edit form held true\ntyped-in-flight unsaved edit form held true\n" +
		"select-changed 0 unsaved edit form\ncheckbox-changed 0 unsaved edit form\n" +
		"typed-then-cleared replacement fragment held false\n"
	if string(out) != want {
		t.Fatalf("refresh:\n%s\nwant:\n%s", out, strings.TrimSpace(want))
	}
}

// selectAllScript runs app.js in a Node VM with a synthetic sessions list of three session
// boxes and the select-all box, sends change events and prints the boxes after each.
const selectAllScript = `
const fs = require('fs');
const vm = require('vm');
let change;
const box = (row) => ({ checked: false, indeterminate: false,
  matches(selector) { return row && selector === 'input[form=bulk][name=s]'; } });
const rows = [box(true), box(true), box(true)];
const all = box(false);
const outside = box(true);
const list = {
  dataset: {},
  contains(node) { return node !== outside; },
  querySelector(selector) {
    if (selector === 'input[data-select-all]') { return all; }
    throw new Error('unmodelled selector ' + selector);
  },
  querySelectorAll(selector) {
    if (selector === 'input[form=bulk][name=s]') { return rows; }
    throw new Error('unmodelled selector ' + selector);
  }
};
const document = { getElementById() { return list; }, visibilityState: 'visible',
  addEventListener(type, f) { if (type === 'change') { change = f; } } };
vm.runInNewContext(fs.readFileSync(process.argv[2], 'utf8'), { document, URL, window: { location: { href: 'http://localhost/dashboard/sessions' }, setInterval() {} } });
const show = (name) => console.log(name + ' ' + rows.map(r => r.checked ? 'x' : '-').join('') + ' all=' + all.checked + ' some=' + all.indeterminate);
const send = (target) => change({ target });
all.checked = true; send(all); show('select-all');
rows[1].checked = false; send(rows[1]); show('clear-one');
rows[1].checked = true; send(rows[1]); show('check-last');
all.checked = false; send(all); show('clear-all');
rows[0].checked = true; send(rows[0]); show('check-one');
outside.checked = true; send(outside); show('outside');
`

// "Select all sessions on this page" checks and clears every session box of the bulk form,
// and a session box shows whether all, some or none are checked (#429).
func TestSelectAllSessions(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		if os.Getenv("CI") != "" {
			t.Fatal("node required in CI")
		}
		t.Skip("node not installed")
	}
	dir := t.TempDir()
	script := filepath.Join(dir, "select.js")
	if err := os.WriteFile(script, []byte(selectAllScript), 0600); err != nil {
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
	want := "select-all xxx all=true some=false\nclear-one x-x all=false some=true\ncheck-last xxx all=true some=false\n" +
		"clear-all --- all=false some=false\ncheck-one x-- all=false some=true\noutside x-- all=false some=true\n"
	if string(out) != want {
		t.Fatalf("select all:\n%s\nwant:\n%s", out, strings.TrimSpace(want))
	}
}
