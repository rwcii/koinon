package core

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// speakScript runs the dashboard's speak.js in Node VMs, one for each tab, over one shared
// browser model: localStorage, Web Locks, a speech engine whose utterances a case ends or
// fails by hand, timers and a synthetic waiting route with a small page size. Each case
// prints "ok NAME" or the failed check. It checks the logic, not audible speech.
const speakScript = `
const fs = require('fs');
const vm = require('vm');
const source = fs.readFileSync(process.argv[2], 'utf8');
const settle = async () => { for (let i = 0; i < 20; i++) { await new Promise(r => setImmediate(r)); } };
function browser(opts = {}) {
  const b = { data: {}, items: [], version: 1, pageSize: 2, requests: 0, spoken: [], utterances: [], mode: opts.mode || 'allow',
    cancels: 0, onPage: null, storageFails: !!opts.storageFails };
  b.storage = {
    getItem(k) { if (b.storageFails) { throw new Error('denied'); } return k in b.data ? b.data[k] : null; },
    setItem(k, v) { if (b.storageFails) { throw new Error('denied'); } b.data[k] = String(v); },
    removeItem(k) { if (b.storageFails) { throw new Error('denied'); } delete b.data[k]; }
  };
  let chain = Promise.resolve();
  b.locks = opts.noLocks ? undefined : { request(name, task) { const p = chain.then(() => task({ name })); chain = p.catch(() => {}); return p; } };
  b.state = () => b.data['koinon.speak.state'] === undefined ? undefined : JSON.parse(b.data['koinon.speak.state']);
  b.on = () => { b.data['koinon.speak'] = 'true'; };
  b.add = (reference, kind, repository, revision = 1) => {
    b.items = b.items.filter(i => i.reference !== reference).concat([{ reference, kind, repository, revision, id: b.items.length + 1 + Math.random() }]);
    b.items.sort((x, y) => x.id - y.id);
    b.version++;
  };
  b.remove = reference => { b.items = b.items.filter(i => i.reference !== reference); b.version++; };
  b.fetch = url => {
    b.requests++;
    const after = Number(new URL(url, 'http://h').searchParams.get('after') || 0);
    if (b.onPage) { b.onPage(after); }
    const rest = b.items.filter(i => i.id > after);
    const items = rest.slice(0, b.pageSize);
    const more = rest.length > b.pageSize;
    const body = { version: String(b.version), items: items.map(({ reference, kind, repository, revision }) => ({ reference, kind, repository, revision })),
      next: more ? items[items.length - 1].id : 0, more };
    return Promise.resolve({ ok: true, json: async () => body });
  };
  b.end = (i = 0, how = 'end') => { const u = b.utterances.splice(i, 1)[0]; if (how === 'end') { b.spoken.push(u.text); u.onend(); } else { u.onerror({ error: how }); } };
  b.tab = (page = {}) => {
    const t = { timers: [], clicks: [], elements: {}, interval: null, storageListeners: [] };
    b.tabs = (b.tabs || []).concat([t]);
    // Each tab's storage tells the other tabs of a change, as a browser's storage event does.
    const notify = key => b.tabs.filter(o => o !== t).forEach(o => o.storageListeners.forEach(f => setImmediate(() => f({ key }))));
    const storage = {
      getItem(k) { return b.storage.getItem(k); },
      setItem(k, v) { b.storage.setItem(k, v); notify(k); },
      removeItem(k) { b.storage.removeItem(k); notify(k); }
    };
    const list = { dataset: { refresh: '5000' } };
    const main = { firstChild: null, insertBefore(el) { t.elements[el.id] = el; } };
    list.parentNode = main;
    t.elements.list = list;
    if (page.settings) {
      const handlers = {};
      t.elements['speak-switch'] = { disabled: true, checked: false, addEventListener(e, f) { handlers.change = f; } };
      t.elements['speak-test'] = { disabled: true, addEventListener(e, f) { handlers.test = f; } };
      t.elements['speak-status'] = { textContent: 'needs script' };
      t.toggle = async on => { t.elements['speak-switch'].checked = on; handlers.change(); await settle(); };
      t.uncheck = () => { t.elements['speak-switch'].checked = false; handlers.change(); };
    }
    const speech = page.noSpeech ? undefined : {
      speak(u) { u.tab = t; b.utterances.push(u); if (b.mode === 'allow') { setImmediate(() => b.end(b.utterances.indexOf(u))); }
        else if (b.mode === 'block') { setImmediate(() => b.end(b.utterances.indexOf(u), 'not-allowed')); } },
      cancel() {
        b.cancels++;
        b.utterances.filter(u => u.tab === t).forEach(u => { b.utterances.splice(b.utterances.indexOf(u), 1); setImmediate(() => u.onerror({ error: 'canceled' })); });
      }
    };
    const window = {
      localStorage: storage, speechSynthesis: speech,
      addEventListener(e, f) { if (e === 'storage') { t.storageListeners.push(f); } },
      SpeechSynthesisUtterance: page.noSpeech ? undefined : function (text) { this.text = text; },
      setInterval(f) { t.interval = f; }, setTimeout(f) { t.timers.push(f); return t.timers.length; }, clearTimeout(id) { t.timers[id - 1] = null; }
    };
    const document = {
      getElementById(id) { return t.elements[id] || null; },
      createElement() { return { setAttribute() {}, hidden: false }; },
      addEventListener(e, f) { if (e === 'click') { t.clicks.push(f); } }
    };
    vm.runInNewContext(source, { window, document, navigator: { locks: b.locks }, fetch: b.fetch, URL, Promise, JSON, Error });
    t.tick = async () => { t.interval(); await settle(); };
    t.click = async () => { t.clicks.forEach(f => f()); await settle(); };
    t.note = () => t.elements['speak-note'] ? !t.elements['speak-note'].hidden : false;
    return t;
  };
  return b;
}
const cases = [];
const test = (name, f) => cases.push([name, f]);
const eq = (got, want, what) => { if (JSON.stringify(got) !== JSON.stringify(want)) { throw new Error(what + ': ' + JSON.stringify(got) + ', want ' + JSON.stringify(want)); } };

test('off-no-fetch', async () => {
  const b = browser(); b.add('D-1', 'decision', 'koinon');
  const t = b.tab(); await settle(); await t.tick();
  eq(b.requests, 0, 'requests while off');
});
test('baseline-then-new', async () => {
  const b = browser(); b.add('D-1', 'decision', 'koinon'); b.on();
  const t = b.tab(); await settle();
  eq(b.spoken, [], 'baseline speech'); eq(b.state().seen, ['D-1@1'], 'baseline seen');
  b.add('T-1', 'task', 'koinon'); await t.tick();
  eq(b.spoken, ['koinon: a task waits for you'], 'new task');
  await t.tick(); eq(b.spoken.length, 1, 'no repeat');
});
test('grouped-and-plain', async () => {
  const b = browser(); b.on(); const t = b.tab(); await settle();
  b.add('A', 'decision', 'koinon'); b.add('B', 'task', 'koinon'); b.add('C', 'decision', ''); b.add('E', 'decision', 'tools');
  await t.tick();
  eq(b.spoken, ['koinon: 2 items wait for you', 'A decision waits for you', 'tools: a decision waits for you'], 'sentences');
});
test('revision-and-removal', async () => {
  const b = browser(); b.add('D-1', 'decision', 'koinon'); b.add('D-2', 'decision', 'koinon'); b.on();
  const t = b.tab(); await settle();
  b.remove('D-1'); await t.tick(); eq(b.spoken, [], 'removal speaks');
  b.add('D-2', 'decision', 'koinon', 2); await t.tick();
  eq(b.spoken, ['koinon: a decision waits for you'], 'a new revision');
});
test('pages-and-restart', async () => {
  const b = browser(); for (const r of ['A', 'B', 'C', 'D', 'E']) { b.add(r, 'decision', 'x'); } b.on();
  const t = b.tab(); await settle(); eq(b.state().seen.length, 5, 'complete baseline over pages');
  const n = b.requests; await t.tick(); eq(b.requests - n, 1, 'requests of an unchanged poll');
  b.add('F', 'decision', 'x');
  let once = true;
  b.onPage = after => { if (after && once) { once = false; b.add('G', 'task', 'x'); } };
  await t.tick();
  eq(b.spoken, ['x: 2 items wait for you'], 'restarted snapshot');
  eq(b.state().seen.length, 7, 'seen after restart');
});
test('mixed-versions-change-nothing', async () => {
  const b = browser(); for (const r of ['A', 'B', 'C']) { b.add(r, 'decision', 'x'); } b.on();
  const t = b.tab(); await settle(); const before = b.data['koinon.speak.state'];
  b.add('D', 'decision', 'x');
  b.onPage = after => { if (after) { b.version++; } };
  await t.tick();
  eq(b.data['koinon.speak.state'], before, 'state after three mixed snapshots'); eq(b.spoken, [], 'speech');
  b.onPage = null; await t.tick(); eq(b.spoken, ['x: a decision waits for you'], 'next poll');
});
test('bad-state-new-baseline', async () => {
  const b = browser(); b.add('D-1', 'decision', 'koinon'); b.on(); b.data['koinon.speak.state'] = '{"version":"1"}';
  const t = b.tab(); await settle();
  eq(b.spoken, [], 'baseline'); eq(b.state().seen, ['D-1@1'], 'seen');
  b.add('T-1', 'task', 'koinon'); await t.tick(); eq(b.spoken, ['koinon: a task waits for you'], 'works after');
});
test('reload-keeps-state', async () => {
  const b = browser(); b.add('D-1', 'decision', 'koinon'); b.on();
  b.tab(); await settle();
  b.add('T-1', 'task', 'koinon');
  b.tab(); await settle();
  eq(b.spoken, ['koinon: a task waits for you'], 'item that came between pages');
});
test('blocked-pending-reload-click', async () => {
  const b = browser({ mode: 'block' }); b.on(); const t = b.tab(); await settle();
  b.add('D-1', 'decision', 'koinon'); await t.tick();
  eq(b.spoken, [], 'blocked'); eq(t.note(), true, 'note'); eq(b.state().pending, ['koinon: a decision waits for you'], 'pending');
  const t2 = b.tab(); await settle(); eq(b.state().pending.length, 1, 'pending after reload'); eq(t2.note(), true, 'note after reload');
  b.mode = 'allow'; await t2.click();
  eq(b.spoken, ['koinon: a decision waits for you'], 'spoken at the click'); eq(b.state().pending, [], 'pending cleared'); eq(t2.note(), false, 'note hidden');
});
test('late-not-allowed', async () => {
  const b = browser({ mode: 'manual' }); b.on(); const t = b.tab(); await settle();
  b.add('D-1', 'decision', 'koinon'); await t.tick();
  eq(b.utterances.length, 1, 'speaking'); b.end(0, 'not-allowed'); await settle();
  eq(b.state().pending.length, 1, 'kept'); eq(t.note(), true, 'note');
});
test('two-tabs-one-speech', async () => {
  const b = browser({ mode: 'manual' }); b.on(); const t1 = b.tab(); const t2 = b.tab(); await settle();
  b.add('D-1', 'decision', 'koinon');
  t1.interval(); t2.interval(); await settle();
  eq(b.utterances.length, 1, 'one utterance while the first is pending');
  b.end(0); await settle();
  eq(b.utterances.length, 0, 'no second utterance'); eq(b.spoken.length, 1, 'spoken once');
});
test('no-locks-can-double', async () => {
  const b = browser({ noLocks: true, mode: 'manual' }); b.on(); const t1 = b.tab(); const t2 = b.tab(); await settle();
  b.add('D-1', 'decision', 'koinon');
  t1.interval(); t2.interval(); await settle();
  eq(b.utterances.length >= 1, true, 'speaks');
});
test('guard-and-late-end', async () => {
  const b = browser({ mode: 'manual' }); b.on(); const t = b.tab(); await settle();
  b.add('D-1', 'decision', 'koinon'); await t.tick();
  const u = b.utterances[0];
  t.timers.filter(Boolean).forEach(f => f()); await settle();
  eq(b.cancels, 1, 'cancelled'); eq(b.state().pending.length, 1, 'kept'); eq(t.note(), true, 'note');
  u.onend(); await settle(); eq(b.state().pending.length, 1, 'late end ignored');
});
test('inherited-names', async () => {
  const b = browser(); b.on(); const t = b.tab(); await settle();
  b.add('A', 'decision', 'constructor'); b.add('B', 'task', '__proto__'); b.add('C', 'decision', 'toString'); await t.tick();
  eq(b.spoken, ['constructor: a decision waits for you', '__proto__: a task waits for you', 'toString: a decision waits for you'], 'sentences');
});
test('off-during-queue', async () => {
  const b = browser({ mode: 'manual' }); const t = b.tab({ settings: true }); await settle();
  await t.toggle(true); b.add('A', 'decision', 'one'); b.add('B', 'decision', 'two'); await t.tick();
  eq(b.utterances.length, 1, 'speaking the first');
  t.uncheck();
  eq(b.data['koinon.speak'], 'false', 'stored at once'); eq(b.cancels, 1, 'cancelled at once');
  await settle();
  eq(b.utterances.length, 0, 'no second sentence'); eq(b.spoken, [], 'spoken');
});
test('off-from-another-tab', async () => {
  const b = browser({ mode: 'manual' }); b.on(); const t1 = b.tab(); const t2 = b.tab({ settings: true }); await settle();
  b.add('A', 'decision', 'one'); b.add('B', 'decision', 'two'); await t1.tick();
  eq(b.utterances.length, 1, 'speaking'); eq(b.utterances[0].tab, t1, 'in the first tab');
  t2.uncheck(); await settle();
  eq(b.utterances.length, 0, 'stopped'); eq(b.spoken, [], 'spoken');
});
test('storage-fails-later', async () => {
  const b = browser(); const t = b.tab({ settings: true }); await settle();
  await t.toggle(true); eq(t.elements['speak-status'].textContent, '', 'status before');
  b.storageFails = true;
  b.add('D-1', 'decision', 'koinon'); await t.tick();
  eq(b.spoken, ['koinon: a decision waits for you'], 'still detects');
  eq(t.elements['speak-status'].textContent, 'This browser does not keep the switch: it lasts for this page only.', 'status after');
  b.add('D-2', 'task', 'koinon'); await t.tick(); eq(b.spoken.length, 2, 'next');
});
test('settings-switch', async () => {
  const b = browser(); b.add('D-1', 'decision', 'koinon');
  const t = b.tab({ settings: true }); await settle();
  eq(t.elements['speak-switch'].disabled, false, 'enabled'); eq(t.elements['speak-switch'].checked, false, 'off');
  await t.toggle(true); eq(b.spoken, [], 'baseline'); eq(b.state().seen, ['D-1@1'], 'seen');
  b.add('D-2', 'decision', 'koinon'); await t.tick(); eq(b.spoken.length, 1, 'new');
  await t.toggle(false); eq(b.state(), undefined, 'state cleared'); const n = b.requests; await t.tick(); eq(b.requests, n, 'off');
});
test('no-speech', async () => {
  const b = browser(); b.on(); const t = b.tab({ settings: true, noSpeech: true }); await settle();
  eq(t.elements['speak-status'].textContent, 'This browser cannot speak.', 'status'); eq(t.elements['speak-switch'].disabled, true, 'disabled');
  eq(b.requests, 0, 'requests');
});
test('storage-fails', async () => {
  const b = browser({ storageFails: true }); const t = b.tab({ settings: true }); await settle();
  eq(t.elements['speak-status'].textContent, 'This browser does not keep the switch: it lasts for this page only.', 'status');
  await t.toggle(true); b.add('D-1', 'decision', 'koinon'); await t.tick();
  eq(b.spoken, ['koinon: a decision waits for you'], 'page-only state');
});
(async () => {
  for (const [name, f] of cases) {
    try { await f(); console.log('ok ' + name); } catch (e) { console.log('FAIL ' + name + ': ' + e.message); }
  }
})();
`

// The spoken alert (#553) in speak.js: the switch, the snapshot comparison, the pending
// sentences and their delivery, one tab at a time.
func TestSpokenAlertScript(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		if os.Getenv("CI") != "" {
			t.Fatal("node required in CI")
		}
		t.Skip("node not installed")
	}
	dir := t.TempDir()
	script := filepath.Join(dir, "speak.js")
	if err := os.WriteFile(script, []byte(speakScript), 0600); err != nil {
		t.Fatal(err)
	}
	source, err := filepath.Abs(filepath.Join("web", "static", "speak.js"))
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(node, script, source)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("node: %v %s", err, out)
	}
	want := []string{"off-no-fetch", "baseline-then-new", "grouped-and-plain", "revision-and-removal", "pages-and-restart",
		"mixed-versions-change-nothing", "bad-state-new-baseline", "reload-keeps-state", "blocked-pending-reload-click", "late-not-allowed", "two-tabs-one-speech",
		"no-locks-can-double", "guard-and-late-end", "inherited-names", "off-during-queue", "off-from-another-tab", "storage-fails-later", "settings-switch", "no-speech", "storage-fails"}
	if got := strings.TrimSpace(string(out)); got != "ok "+strings.Join(want, "\nok ") {
		t.Fatalf("speak.js:\n%s", got)
	}
}
