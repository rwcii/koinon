package core

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// Exercise real remote.js with controllable DOM/transport boundaries. These are
// synthetic browser contracts, not real-device or cellular acceptance evidence.
func TestDashboardRemoteBrowserRecovery(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("Node unavailable")
	}
	dir := t.TempDir()
	script, err := webFiles.ReadFile("web/static/remote.js")
	if err != nil {
		t.Fatal(err)
	}
	source := filepath.Join(dir, "remote.js")
	harness := filepath.Join(dir, "harness.js")
	if err = os.WriteFile(source, script, 0600); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(harness, []byte(remoteBrowserHarness), 0600); err != nil {
		t.Fatal(err)
	}
	if out, err := exec.Command(node, harness, source).CombinedOutput(); err != nil {
		t.Fatalf("remote browser contract: %v\n%s", err, out)
	}
}

const remoteBrowserHarness = `
const vm=require('vm'),fs=require('fs'),assert=require('assert/strict');
const code=fs.readFileSync(process.argv[2],'utf8');
function browser(){
 let html='old measured status',busy=false,focused=false,approval=null,now=1000;
 const events={},requests=[],intervals=[],input={tagName:'INPUT'},status={textContent:''};
 const panel={get innerHTML(){return html},set innerHTML(v){html=v;approval={text:'new approval',enableAllowed:true,replaceWith(old){approval=old}}},querySelector(s){if(s==='#remote-setup')return approval;return busy?{}:null},contains(n){return n===input}};
 const document={visibilityState:'visible',get activeElement(){return focused?input:null},getElementById(id){return id==='remote-panel'?panel:status},addEventListener(n,f){(events[n]??=[]).push(f)}};
 const window={location:{href:'https://host.example.ts.net/dashboard/settings'},addEventListener:document.addEventListener,setInterval(f){intervals.push(f)}};
 class Clock extends Date{static now(){return now}}
 vm.runInNewContext(code,{document,window,URL,AbortController,Date:Clock,setTimeout(){return 1},clearTimeout(){},fetch(url,options){return new Promise((resolve,reject)=>requests.push({url,options,resolve,reject}))}});
 return {requests,status,panel,events,intervals,set busy(v){busy=v},set focused(v){focused=v},set approval(v){approval=v},get approval(){return approval},advance(n){now+=n},event(n){for(const f of events[n]||[])f({})},check(){for(const f of events.click)f({target:{closest(){return {}}},preventDefault(){}})}};
}
const settle=()=>new Promise(r=>setImmediate(r));
function answer(r,text){r.resolve({ok:true,text:async()=>text})}
(async()=>{
 const b=browser();assert.equal(b.requests.length,1);assert.equal(b.requests[0].options.method,undefined);assert.equal(b.requests[0].options.redirect,'error');
 answer(b.requests[0],'off status');await settle();assert.equal(b.panel.innerHTML,'off status');
 b.check();b.check();assert.equal(b.requests.length,2);assert.match(b.requests[1].url,/setup=1/);
 answer(b.requests[1],'needs approval');await settle();const provider={text:'validated provider approval',enableAllowed:false};b.approval=provider;
 b.event('online');assert.equal(b.requests.length,3);assert.match(b.requests[2].url,/checked=1/);assert.doesNotMatch(b.requests[2].url,/setup=1/);
 answer(b.requests[2],'fresh measured status');await settle();assert.equal(b.panel.innerHTML,'fresh measured status');assert.equal(b.approval,provider);assert.equal(b.approval.enableAllowed,false);
 b.busy=true;b.event('online');assert.equal(b.requests.length,3);b.busy=false;
 b.event('online');b.focused=true;answer(b.requests[3],'discard while typing');await settle();assert.equal(b.panel.innerHTML,'fresh measured status');b.focused=false;
 b.event('visibilitychange');b.requests[4].reject(new Error('offline'));await settle();assert.match(b.status.textContent,/unavailable.*stale/);assert.equal(b.panel.innerHTML,'fresh measured status');
 b.advance(31000);b.intervals[0]();assert.match(b.status.textContent,/stale/);answer(b.requests[5],'recovered');await settle();assert.equal(b.panel.innerHTML,'recovered');assert.match(b.status.textContent,/Remote status checked/);assert.doesNotMatch(b.status.textContent,/stale/);
 assert.equal(b.requests.filter(r=>r.options.method==='POST').length,0);assert.ok(b.requests.every(r=>r.options.cache==='no-store'));
 console.log('remote browser read, approval, single-flight, form preservation and recovery passed');
})().catch(e=>{console.error(e);process.exit(1)});
`
