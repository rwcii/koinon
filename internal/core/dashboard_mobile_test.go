package core

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// These tests execute the shipped browser script with synthetic DOM nodes and explicit
// network completions. They prove the read/write contract, not a phone's connectivity.
const mobileBrowserScript = `
const assert = require('assert/strict');
const fs = require('fs');
const vm = require('vm');
const code = fs.readFileSync(process.argv[2], 'utf8');
const settle = () => new Promise(resolve => setImmediate(resolve));
function browser() {
  let now = 100000, html = 'retained data';
  const events = {}, timers = [], intervals = {}, requests = [], navigations = [];
  const nodes = {};
  function node(extra = {}) { return Object.assign({ dataset: {}, hidden: false, textContent: '',
    setAttribute(name, value) { this[name] = value; }, removeAttribute(name) { delete this[name]; },
    addEventListener(name, fn) { this[name] = fn; } }, extra); }
  nodes.list = node({ dataset: { refresh: '5000' }, dirty: false, formOpen: false,
    querySelectorAll(selector) {
      if (selector === 'input, textarea, select') { return [{tagName:'TEXTAREA', type:'textarea', defaultValue:'', value:this.dirty ? 'unsent draft' : ''}]; }
      return [];
    },
    querySelector() { return this.formOpen ? {} : null; }, contains() { return false; },
    get innerHTML() { return html; }, set innerHTML(value) { html = value; }
  });
  nodes.connection = node({ dataset: { refreshed: String(now) } });
  for (const id of ['connection-state', 'refresh-time', 'sign-in', 'reconnect', 'action-result']) { nodes[id] = node(); }
  const location = new URL('https://host.example.ts.net/dashboard/decisions?status=active');
  location.assign = url => navigations.push(url);
  location.reload = () => navigations.push('reload');
  function listen(name, fn) { (events[name] ||= []).push(fn); }
  const document = { getElementById:id => nodes[id] || null, activeElement: null, visibilityState:'visible', addEventListener:listen };
  class Clock extends Date { constructor(value) { super(value === undefined ? now : value); } static now() { return now; } }
  class Fields { constructor(form) { this.values = form.fields.slice(); } append(name,value) { this.values.push([name,value]); } [Symbol.iterator]() { return this.values[Symbol.iterator](); } }
  const window = { location, addEventListener:listen,
    setInterval(fn,ms) { intervals[ms] = fn; },
    setTimeout(fn,ms) { const id = timers.length; timers.push({fn,ms}); return id; },
    clearTimeout(id) { timers[id] = null; }
  };
  vm.runInNewContext(code, { document, window, navigator: {}, URL, URLSearchParams, FormData:Fields, AbortController, Date:Clock,
    fetch(url, options) { return new Promise((resolve,reject) => requests.push({ url, options, resolve, reject })); }
  });
  return {nodes, requests, navigations, document, intervals, timers,
    advance(ms) { now += ms; intervals[1000](); },
    event(name, value) { for (const fn of events[name] || []) { fn(value); } },
    form(action='/dashboard/actions/decision-approve') { const button = {disabled:false}; return {button, method:'post', action:new URL(action,location).toString(), getAttribute(name) { return name === 'method' ? 'post' : name === 'action' ? action : null; }, dataset:{}, fields:[['csrf','synthetic-csrf'],['key','one-operation'],['note','unsent draft']], querySelectorAll() { return [button]; } }; },
    submit(form, submitter) { this.event('submit',{target:form, submitter, preventDefault() {}}); }
  };
}
function read(request, text='new data') { request.resolve({ok:true, status:200, text:async () => text}); }
(async () => {
  const b = browser();
  assert.equal(b.nodes['connection-state'].textContent, 'Connected');
  b.advance(15000);
  assert.match(b.nodes['connection-state'].textContent, /Stale data/);
  b.nodes.list.dirty = true;
  b.nodes.list.formOpen = true;
  b.event('online');
  assert.equal(b.requests.length,1);
  assert.equal(b.requests[0].options.method,undefined);
  read(b.requests[0]); await settle();
  assert.equal(b.nodes.list.innerHTML,'retained data');
  assert.match(b.nodes['connection-state'].textContent,/Connected.*Stale data.*Form kept/);
  b.nodes.list.dirty = false; b.nodes.list.formOpen = false;
  b.event('visibilitychange'); read(b.requests[1]); await settle();
  assert.equal(b.nodes.list.innerHTML,'new data');
  assert.equal(b.nodes['connection-state'].textContent,'Connected');
  assert.ok(b.nodes['refresh-time'].dateTime);
  b.event('offline'); assert.match(b.nodes['connection-state'].textContent,/Connection unavailable/);
  b.nodes.reconnect.click();
  b.requests[2].resolve({ok:false,status:401}); await settle();
  assert.match(b.nodes['connection-state'].textContent,/Sign-in required/);
  assert.equal(b.nodes['sign-in'].href,'/dashboard/login?return=%2Fdashboard%2Fdecisions%3Fstatus%3Dactive');
  assert.equal(b.nodes.list.innerHTML,'new data');
  // Reads are single-flight and timed out without a write or a navigation.
  b.event('online'); b.event('online'); assert.equal(b.requests.length,4);
  const readTimer = b.timers.find(t => t && t.ms === 10000); readTimer.fn();
  assert.equal(b.requests[3].options.signal.aborted,true);
  b.requests[3].reject(new Error('lost')); await settle();
  assert.equal(b.navigations.length,0);

  const w = browser(), form = w.form();
  const submitter = {name:'choice',value:'feedback',getAttribute(name) {return name === 'formaction' ? '/dashboard/actions/decision-feedback' : null;}};
  w.submit(form,submitter); w.submit(form,submitter);
  assert.equal(w.requests.length,1);
  assert.equal(form.button.disabled,true);
  assert.match(w.requests[0].url,/decision-feedback$/);
  assert.equal(w.requests[0].options.body.get('csrf'),'synthetic-csrf');
  assert.equal(w.requests[0].options.body.get('key'),'one-operation');
  assert.equal(w.requests[0].options.body.get('choice'),'feedback');
  assert.equal(w.requests[0].options.redirect,'follow');
  w.requests[0].reject(new Error('response lost')); await settle();
  assert.equal(w.nodes['action-result'].textContent,'Result unknown; check the latest state before trying again.');
  assert.equal(form.fields[2][1],'unsent draft');
  assert.equal(form.button.disabled,false);
  w.event('online');
  assert.equal(w.requests.length,2);
  assert.equal(w.requests[1].options.method,undefined);
  read(w.requests[1]); await settle();
  assert.equal(w.requests.filter(r => r.options.method === 'POST').length,1);
  assert.equal(w.navigations.length,0);

  const success = browser(), save = success.form(); success.submit(save);
  success.requests[0].resolve({ok:true,status:200,redirected:true,url:'https://host.example.ts.net/dashboard/decisions?notice=decision_approved'}); await settle();
  assert.equal(success.navigations[0],'https://host.example.ts.net/dashboard/decisions?notice=decision_approved');
  const expired = browser(); expired.submit(expired.form());
  expired.requests[0].resolve({ok:false,status:401}); await settle();
  assert.equal(expired.nodes['sign-in'].hidden,false);
  assert.equal(expired.navigations.length,0);
  assert.match(expired.nodes['action-result'].textContent,/will not be replayed/);
  const timeout = browser(); timeout.submit(timeout.form());
  timeout.timers.find(t => t && t.ms === 20000).fn();
  assert.equal(timeout.requests[0].options.signal.aborted,true);
  timeout.requests[0].reject(new Error('aborted')); await settle();
  assert.match(timeout.nodes['action-result'].textContent,/Result unknown/);
  assert.equal(timeout.requests.length,1);
  console.log('mobile refresh and write contract passed');
})().catch(error => { console.error(error); process.exitCode=1; });
`

func TestMobileBrowserRefreshAndWrites(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		if os.Getenv("CI") != "" {
			t.Fatal("node required in CI")
		}
		t.Skip("node not installed")
	}
	dir := t.TempDir()
	script := filepath.Join(dir, "mobile.js")
	if err := os.WriteFile(script, []byte(mobileBrowserScript), 0600); err != nil {
		t.Fatal(err)
	}
	source, err := filepath.Abs(filepath.Join("web", "static", "app.js"))
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(node, script, source)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("browser script: %v\n%s", err, out)
	}
}

// The optional layout check uses a disposable Chrome profile and synthetic dashboard
// pages. It measures CSS viewport behavior; it is not real iOS/Android/keyboard evidence.
func TestMobileViewportLayout(t *testing.T) {
	if os.Getenv("KOINON_BROWSER_TEST") != "1" {
		t.Skip("set KOINON_BROWSER_TEST=1 for the disposable Chrome viewport check")
	}
	chrome, err := exec.LookPath("google-chrome")
	if err != nil {
		chrome = "/Applications/Google Chrome.app/Contents/MacOS/Google Chrome"
	}
	if _, err := os.Stat(chrome); err != nil {
		t.Fatal("Chrome required for KOINON_BROWSER_TEST=1")
	}
	d, root := startTestDaemon(t)
	bearer, err := ReadSecret(root)
	if err != nil {
		t.Fatal(err)
	}
	cookie := dashboardLogin(t, d, bearer)
	peer := join(t, d.store, "codex", "synthetic-mobile-session", namedRepo(t, strings.Repeat("long", 20)))
	receiver := join(t, d.store, "claude", "synthetic-mobile-receiver", "")
	body := strings.Repeat("long-synthetic-identifier", 40)
	if _, err := d.store.Send(context.Background(), Key{peer.Family, peer.ID}, receiver.Name, body); err != nil {
		t.Fatal(err)
	}
	room := mustRoom(t, d.store, "synthetic mobile room")
	mustAdd(t, d.store, room, peer.Name)
	if _, err := d.store.RoomPost(context.Background(), Key{peer.Family, peer.ID}, room, body, "mobile-post", d.store.clock()+600); err != nil {
		t.Fatal(err)
	}
	fields := decisionFields("Review the synthetic mobile decision", "decision")
	fields.Context = ptr(body)
	fields.Recommendation = ptr("Keep every existing action reachable on the phone.")
	mustCreate(t, d.store, Key{peer.Family, peer.ID}, "MOBILE-1", fields)
	pages := map[string]string{}
	for _, view := range dashboardViews {
		path := "/dashboard/" + view
		if view == "rooms" {
			path += "?room=" + room
		}
		response := dashboardDo(t, d, "GET", path, cookie, nil, nil)
		if response.status != 200 {
			t.Fatalf("%s: %d", view, response.status)
		}
		// Rendering only: the fixture serves the real templates and CSS, without private data
		// or browser write/refresh scripts. The audit below runs after layout completes.
		html := strings.ReplaceAll(response.body, `<script src="/dashboard/static/app.js" defer></script>`, "")
		html = strings.ReplaceAll(html, `<script src="/dashboard/static/speak.js" defer></script>`, "")
		html = strings.Replace(html, "</body>", `<script src="/audit.js"></script></body>`, 1)
		pages["/"+view] = html
	}
	static, err := webFiles.ReadFile("web/static/app.css")
	if err != nil {
		t.Fatal(err)
	}
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/dashboard/static/app.css" {
			w.Header().Set("Content-Type", "text/css")
			w.Write(static)
			return
		}
		if r.URL.Path == "/audit.js" {
			w.Header().Set("Content-Type", "text/javascript")
			io.WriteString(w, `window.addEventListener('load',function(){
    const failures=[];
    if(document.documentElement.scrollWidth>window.innerWidth) failures.push('viewport overflow '+document.documentElement.scrollWidth+' > '+window.innerWidth);
    for(const field of document.querySelectorAll('input:not([type=hidden]),textarea,select')){
     if(parseFloat(getComputedStyle(field).fontSize)<16)failures.push('form text below 16px');
    }
    for(const target of document.querySelectorAll('button,summary,nav a,input:not([type=hidden]),select')){
     const size=target.getBoundingClientRect();
     if(size.width && (size.height<44 || size.width<44))failures.push('touch target below 44px: '+target.outerHTML);
    }
    const result=document.createElement('pre');result.id='layout-result';result.textContent=failures.length?failures.join('\n'):'LAYOUT_OK';document.body.append(result);
   });`)
			return
		}
		if html, ok := pages[r.URL.Path]; ok {
			io.WriteString(w, html)
			return
		}
		http.NotFound(w, r)
	})
	server := httptest.NewServer(handler)
	defer server.Close()

	node, err := exec.LookPath("node")
	if err != nil {
		t.Fatal("Node required for Chrome viewport check")
	}
	dir := t.TempDir()
	script := filepath.Join(dir, "layout.js")
	if err := os.WriteFile(script, []byte(mobileLayoutScript), 0600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, node, script, chrome, server.URL, filepath.Join(dir, "chrome-profile"))
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("Chrome viewport check: %v\n%s", err, out)
	}
	t.Log(strings.TrimSpace(string(out)))
}

const mobileLayoutScript = `
const {spawn}=require('child_process');
(async()=>{
 const chrome=spawn(process.argv[2],['--headless=new','--no-first-run','--no-default-browser-check','--disable-gpu','--disable-extensions','--no-proxy-server','--disable-background-networking','--remote-debugging-port=0','--user-data-dir='+process.argv[4]]);
 process.on('exit',()=>chrome.kill());
 let socket; const watchdog=setTimeout(()=>{chrome.kill();process.exit(2);},80000);
 try {
  const endpoint=await new Promise((resolve,reject)=>{let text='';chrome.stderr.on('data',chunk=>{text+=chunk;const m=text.match(/DevTools listening on (ws:\/\/[^\s]+)/);if(m)resolve(m[1]);});chrome.once('error',reject);chrome.once('exit',code=>reject(new Error('Chrome exited '+code)));});
  socket=new WebSocket(endpoint);
  await new Promise((resolve,reject)=>{socket.addEventListener('open',resolve,{once:true});socket.addEventListener('error',reject,{once:true});});
  let sequence=0;const pending=new Map(),events=new Map();
  socket.addEventListener('message',event=>{const message=JSON.parse(event.data);if(message.id){const task=pending.get(message.id);pending.delete(message.id);message.error?task.reject(message.error):task.resolve(message.result);}else{const waiter=events.get(message.sessionId+':'+message.method);if(waiter){events.delete(message.sessionId+':'+message.method);waiter(message.params);}}});
  const call=(method,params={},sessionId)=>new Promise((resolve,reject)=>{const id=++sequence;const timer=setTimeout(()=>{pending.delete(id);reject(new Error('Chrome command timed out: '+method));},10000);pending.set(id,{resolve(value){clearTimeout(timer);resolve(value);},reject(error){clearTimeout(timer);reject(error);}});socket.send(JSON.stringify({id,method,params,sessionId}));});
  console.log('Browser: '+JSON.stringify(await call('Browser.getVersion')));
  const {targetId}=await call('Target.createTarget',{url:'about:blank'});
  const {sessionId}=await call('Target.attachToTarget',{targetId,flatten:true});
  await call('Page.enable',{},sessionId);
  await call('Emulation.setTouchEmulationEnabled',{enabled:true},sessionId);
  for(const [width,height] of [[360,800],[390,844],[844,390]]){
   await call('Emulation.setDeviceMetricsOverride',{width,height,deviceScaleFactor:1,mobile:true},sessionId);
   for(const view of ['sessions','messages','rooms','decisions','settings','health','work','memory','audit']){
    const loaded=new Promise((resolve,reject)=>{const timer=setTimeout(()=>reject(new Error('Chrome page load timed out: '+width+' '+view)),10000);events.set(sessionId+':Page.loadEventFired',value=>{clearTimeout(timer);resolve(value);});});
    await Promise.all([call('Page.navigate',{url:process.argv[3]+'/'+view},sessionId),loaded]);
    const measured=await call('Runtime.evaluate',{expression:'JSON.stringify({width:innerWidth,result:document.getElementById("layout-result")?.textContent})',returnByValue:true},sessionId);
    const result=JSON.parse(measured.result.value);
    if(result.width!==width||result.result!=='LAYOUT_OK')throw new Error(width+' '+view+': '+JSON.stringify(result));
   }
  }
  console.log('27 real Chromium viewport checks passed: 360x800, 390x844, 844x390; synthetic data only');
 }finally{clearTimeout(watchdog);if(socket)socket.close();chrome.kill();}
})().catch(error=>{console.error(error);process.exitCode=1;});
`
