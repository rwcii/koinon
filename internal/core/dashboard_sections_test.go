package core

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"
)

// TestSettingsSectionsRenderClosed checks every rendered Settings variant (#719): no action
// section has open, each has a unique key equal to its id, nested details stay unkeyed, and
// sections.js is linked on Settings only.
func TestSettingsSectionsRenderClosed(t *testing.T) {
	pages, err := dashboardTemplates()
	if err != nil {
		t.Fatal(err)
	}
	tag := regexp.MustCompile(`<details\b[^>]*>`)
	attr := func(s, name string) (string, bool) {
		m := regexp.MustCompile(`\b` + name + `="([^"]*)"`).FindStringSubmatch(s)
		if m == nil {
			return "", false
		}
		return m[1], true
	}
	for _, v := range []struct {
		name   string
		remote bool
		link   *LinkStatus
		want   []string
		nested int
	}{
		{"local", false, &LinkStatus{}, []string{"signed-in-devices", "remote-access", "computers", "spoken-alert", "session-timing"}, 0},
		{"remote", true, nil, []string{"signed-in-devices", "draft-recovery", "remote-access", "spoken-alert", "session-timing"}, 0},
		{"configured link", false, &LinkStatus{Configured: true, Remote: true, Name: "synthetic"}, []string{"signed-in-devices", "remote-access", "computers", "spoken-alert", "session-timing"}, 1},
	} {
		var out bytes.Buffer
		page := dashboardPage{View: "settings", Title: "Settings", CSRF: "synthetic-csrf", Views: dashboardViews, Now: time.UnixMilli(1),
			Remote: v.remote, Data: settingsData{Current: DefaultSettings, Default: DefaultSettings, Link: v.link}}
		if err := pages["settings"].ExecuteTemplate(&out, "page", page); err != nil {
			t.Fatalf("%s: %v", v.name, err)
		}
		body := out.String()
		var keys []string
		nested := 0
		for _, d := range tag.FindAllString(body, -1) {
			if regexp.MustCompile(`\bopen\b`).MatchString(d) {
				t.Fatalf("%s: a section starts open: %s", v.name, d)
			}
			class, _ := attr(d, "class")
			key, keyed := attr(d, "data-section")
			if !strings.Contains(" "+class+" ", " action ") {
				if keyed {
					t.Fatalf("%s: nested details has a section key: %s", v.name, d)
				}
				nested++
				continue
			}
			if id, _ := attr(d, "id"); !keyed || id != key {
				t.Fatalf("%s: action section without a stable key equal to its id: %s", v.name, d)
			}
			keys = append(keys, key)
		}
		if strings.Join(keys, " ") != strings.Join(v.want, " ") || nested != v.nested {
			t.Fatalf("%s: sections %v (nested %d), want %v (nested %d)", v.name, keys, nested, v.want, v.nested)
		}
		if strings.Count(body, `src="/dashboard/static/sections.js"`) != 1 {
			t.Fatalf("%s: sections.js is not linked once", v.name)
		}
	}
	layout, err := webFiles.ReadFile("web/layout.html")
	if err != nil {
		t.Fatal(err)
	}
	for _, line := range strings.Split(string(layout), "\n") {
		if strings.Contains(line, "sections.js") && !strings.HasPrefix(line, `{{if eq .View "settings"}}`) {
			t.Fatalf("sections.js is linked outside Settings: %s", line)
		}
	}
	if _, err := webFiles.ReadFile("web/static/sections.js"); err != nil {
		t.Fatal(err)
	}
}

// TestSettingsSectionsBrowser runs the real sections.js, and remote.js before it in page
// order, against a synthetic DOM and storage (#719).
func TestSettingsSectionsBrowser(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node not installed")
	}
	dir := t.TempDir()
	for _, name := range []string{"sections.js", "remote.js"} {
		data, err := webFiles.ReadFile("web/static/" + name)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, name), data, 0600); err != nil {
			t.Fatal(err)
		}
	}
	harness := filepath.Join(dir, "harness.cjs")
	if err := os.WriteFile(harness, []byte(sectionsBrowserHarness), 0600); err != nil {
		t.Fatal(err)
	}
	if out, err := exec.Command(node, harness, filepath.Join(dir, "sections.js"), filepath.Join(dir, "remote.js")).CombinedOutput(); err != nil {
		t.Fatalf("settings sections contract: %v\n%s", err, out)
	}
}

const sectionsBrowserHarness = `
const fs=require('fs'),vm=require('vm'),assert=require('assert/strict');
const sectionsCode=fs.readFileSync(process.argv[2],'utf8'),remoteCode=fs.readFileSync(process.argv[3],'utf8');
const STORE='koinon.settings.open-sections';
const KEYS=['signed-in-devices','remote-access','computers','spoken-alert','session-timing'];
const settle=()=>new Promise(r=>setImmediate(r));
function storage(mode,data){
 data=data||{};
 return {data,getItem(k){if(mode==='read-throws')throw new Error('SecurityError');return k in data?data[k]:null},
  setItem(k,v){if(mode==='write-throws')throw new Error('QuotaExceededError');data[k]=String(v)}};
}
// A browser fires one queued toggle event after a change of open.
function section(key){
 const s={id:key,dataset:{section:key},listeners:[],value:false,addEventListener(n,f){if(n==='toggle')this.listeners.push(f)},
  get open(){return this.value},set open(v){v=!!v;if(v===this.value)return;this.value=v;setImmediate(()=>this.listeners.forEach(f=>f({})))}};
 return s;
}
function page(store,hash,opts){
 opts=opts||{};
 const sections=KEYS.map(section),events={};
 const document={querySelectorAll(sel){assert.ok(['details.action[data-section]','details.action'].includes(sel),sel);return sections},
  getElementById(id){return id==='remote-panel'?opts.panel||null:{textContent:'',hidden:true}},addEventListener(){},visibilityState:'visible',activeElement:null};
 const location={hash:hash||'',href:'https://host.example.ts.net/dashboard/settings'+(hash||'')};
 const window={location,addEventListener(n,f){(events[n]??=[]).push(f)},setInterval(){}};
 if(store==='getter-throws'){Object.defineProperty(window,'localStorage',{get(){throw new Error('SecurityError')}})}else{window.localStorage=store}
 const ctx={document,window,URL,AbortController,Date,setTimeout(){return 1},clearTimeout(){},fetch(){return new Promise(()=>{})}};
 if(opts.preopen)for(const k of opts.preopen)sections[KEYS.indexOf(k)].value=true;
 if(opts.remote)vm.runInNewContext(remoteCode,ctx);
 vm.runInNewContext(sectionsCode,ctx);
 const by=k=>sections[KEYS.indexOf(k)];
 return {sections,by,openKeys:()=>sections.filter(s=>s.open).map(s=>s.dataset.section),
  navigate(h){location.hash=h;for(const f of events.hashchange||[])f({})}};
}
(async()=>{
 // Empty storage: every section is closed and nothing is written.
 let store=storage();let p=page(store);await settle();
 assert.deepEqual(p.openKeys(),[]);assert.equal(store.data[STORE],undefined);
 // Open two, close one: a new document opens exactly the current set.
 p.by('computers').open=true;p.by('spoken-alert').open=true;await settle();
 assert.deepEqual(JSON.parse(store.data[STORE]),['computers','spoken-alert']);
 p.by('computers').open=false;await settle();
 assert.deepEqual(JSON.parse(store.data[STORE]),['spoken-alert']);
 p=page(store);await settle();assert.deepEqual(p.openKeys(),['spoken-alert']);
 assert.deepEqual(JSON.parse(store.data[STORE]),['spoken-alert']);
 // An initial hash opens its section and keeps the saved one; a hashchange opens another.
 p=page(store,'#remote-access');await settle();assert.deepEqual(p.openKeys(),['remote-access','spoken-alert']);
 p.navigate('#session-timing');await settle();assert.deepEqual(p.openKeys(),['remote-access','spoken-alert','session-timing']);
 // Unknown hash targets and unknown or malformed saved keys are ignored.
 p.navigate('#nope');p.navigate('#%E0%A4%A');await settle();assert.equal(p.openKeys().length,3);
 for(const raw of ['{bad','{"a":1}','"spoken-alert"','[1,null,"nope","computers"]']){
  const s=storage(undefined,{[STORE]:raw});const q=page(s);await settle();
  assert.deepEqual(q.openKeys(),raw.includes('computers')?['computers']:[],raw);
  q.by('session-timing').open=true;await settle();assert.ok(JSON.parse(s.data[STORE]).includes('session-timing'),raw);
 }
 // Storage that throws on read, on write or on access keeps the page and its toggles usable.
 for(const mode of ['read-throws','write-throws','getter-throws']){
  const q=page(mode==='getter-throws'?mode:storage(mode,{[STORE]:'["computers"]'}),'#computers');await settle();
  assert.deepEqual(q.openKeys(),['computers'],mode);
  q.by('spoken-alert').open=true;await settle();q.by('computers').open=false;await settle();
  assert.deepEqual(q.openKeys(),['spoken-alert'],mode);
 }
 // remote.js runs first on Settings: a #remote-access link opens Remote access and keeps the
 // sections that were already open or saved (C6).
 const panel={innerHTML:'',querySelector(){return null},contains(){return false}};
 store=storage(undefined,{[STORE]:'["spoken-alert"]'});
 p=page(store,'#remote-access',{remote:true,panel,preopen:['computers']});await settle();
 assert.deepEqual(p.openKeys(),['remote-access','computers','spoken-alert']);
 assert.deepEqual(JSON.parse(store.data[STORE]),['remote-access','computers','spoken-alert']);
 console.log('settings sections: closed default, kept set, links and storage faults passed');
})().catch(e=>{console.error(e);process.exitCode=1});
`
