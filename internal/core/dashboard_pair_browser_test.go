package core

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

func TestRemoteBrowserSessionFixed48Hours(t *testing.T) {
	now := time.Date(2026, 11, 11, 12, 0, 0, 0, time.UTC)
	auth, err := newDashboardAuth(func() time.Time { return now })
	if err != nil {
		t.Fatal(err)
	}
	remoteID, _ := auth.start("remote:https://host.example.ts.net")
	localID, _ := auth.start("dashboard")
	now = now.Add(48*time.Hour - time.Millisecond)
	auth.mu.Lock()
	auth.prune(now)
	remoteHash, _ := tokenHash(remoteID)
	localHash, _ := tokenHash(localID)
	remoteAlive, localAlive := auth.sessions[remoteHash] != nil, auth.sessions[localHash] != nil
	auth.mu.Unlock()
	if !remoteAlive || localAlive {
		t.Fatalf("before 48h idle: remote=%v local=%v", remoteAlive, localAlive)
	}
	now = now.Add(time.Millisecond)
	auth.mu.Lock()
	auth.prune(now)
	alive := auth.sessions[remoteHash] != nil
	auth.mu.Unlock()
	if alive {
		t.Fatal("remote login survived absolute lifetime")
	}
}

func TestDashboardPairBrowser(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("Node unavailable")
	}
	dir := t.TempDir()
	pair, err := webFiles.ReadFile("web/static/pair.js")
	if err != nil {
		t.Fatal(err)
	}
	remote, err := webFiles.ReadFile("web/static/remote.js")
	if err != nil {
		t.Fatal(err)
	}
	files := map[string][]byte{"pair.js": pair, "remote.js": remote, "test.js": []byte(pairBrowserHarness)}
	for name, data := range files {
		if err := os.WriteFile(filepath.Join(dir, name), data, 0600); err != nil {
			t.Fatal(err)
		}
	}
	out, err := exec.Command(node, filepath.Join(dir, "test.js"), filepath.Join(dir, "pair.js"), filepath.Join(dir, "remote.js")).CombinedOutput()
	if err != nil {
		t.Fatalf("pair browser contract: %v\n%s", err, out)
	}
}

const pairBrowserHarness = `
const assert=require('assert/strict'),vm=require('vm'),fs=require('fs');
const pairCode=fs.readFileSync(process.argv[2],'utf8'),remoteCode=fs.readFileSync(process.argv[3],'utf8');
const settle=()=>new Promise(r=>setImmediate(r)),token='a'.repeat(64);
function pair(fragment='#ticket='+token){
 const requests=[],history=[],navigations=[],events={};let cleaned=false;
 const nodes={'pair-status':{textContent:''},'pair-help':{hidden:true}};
 const location=new URL('https://host.example.ts.net/dashboard/pair'+fragment);location.replace=url=>navigations.push(url);
 const context={URL,URLSearchParams,AbortController,setTimeout(){return 1},clearTimeout(){},document:{getElementById(id){return nodes[id]}},window:{location,history:{replaceState(...args){history.push(args);cleaned=true}},addEventListener(n,f){events[n]=f}},fetch(url,options){assert.equal(cleaned,true);assert.equal(url,'/dashboard/pair');return new Promise((resolve,reject)=>requests.push({url,options,resolve,reject}))}};
 vm.runInNewContext(pairCode,context);return {requests,history,navigations,nodes,events};
}
function remote(){
 const requests=[],events={},intervals={},revoked=[];let now=1000,blobID=0,ticketNode;
 function fresh(){const elements={image:{src:''},output:{hidden:true},message:{textContent:''},result:{textContent:'',hidden:true},button:{dataset:{csrf:'synthetic-csrf'},disabled:false,textContent:'Show sign-in QR'}};return {elements,replaceWith(old){ticketNode=old}}}
 ticketNode=fresh();const status={textContent:'',hidden:true};let html='enabled';
 const panel={querySelector(s){if(s==='#remote-ticket')return ticketNode;return null},contains(){return false},get innerHTML(){return html},set innerHTML(v){html=v;ticketNode=v==='off'?null:fresh()}};
 const ids={'remote-ticket-image':'image','remote-ticket-output':'output','remote-ticket-message':'message','remote-ticket-result':'result'};
 const document={visibilityState:'visible',activeElement:null,getElementById(id){if(id==='remote-panel')return panel;if(id==='remote-read-status')return status;return ticketNode?.elements[ids[id]]||null},addEventListener(n,f){(events[n]??=[]).push(f)}};
 class Clock extends Date{static now(){return now}}
 class BrowserURL extends URL{static createObjectURL(){return 'blob:ticket-'+(++blobID)}static revokeObjectURL(v){revoked.push(v)}}
 const window={location:new URL('https://host.example.ts.net/dashboard/settings'),setInterval(f,ms){intervals[ms]=f},addEventListener:document.addEventListener};
 vm.runInNewContext(remoteCode,{document,window,URL:BrowserURL,URLSearchParams,AbortController,Date:Clock,setTimeout(){return 1},clearTimeout(){},fetch(url,options){return new Promise((resolve,reject)=>requests.push({url,options,resolve,reject}))}});
 return {requests,revoked,status,get ticket(){return ticketNode},intervals,event(n){for(const f of events[n]||[])f({})},click(){const button=ticketNode.elements.button;for(const f of events.click)f({target:{closest(selector){return selector.startsWith('button')?button:null}},preventDefault(){}})},advance(ms){now+=ms;intervals[1000]()}};
}
function read(request,html='enabled'){request.resolve({ok:true,text:async()=>html})}
function image(request){request.resolve({ok:true,headers:{get(){return '120'}},blob:async()=>({type:'image/png'})})}
(async()=>{
 const p=pair();assert.equal(p.requests.length,1);assert.equal(p.requests[0].options.body.get('ticket'),token);assert.equal(p.history[0][2],'/dashboard/pair');assert.equal(p.requests[0].options.method,'POST');assert.equal(p.requests[0].options.cache,'no-store');assert.doesNotMatch(p.requests[0].url,/ticket=/);
 p.requests[0].resolve({ok:true,status:200,redirected:true,url:'https://host.example.ts.net/dashboard/attention'});await settle();assert.deepEqual(p.navigations,['https://host.example.ts.net/dashboard/attention']);
 const replay=pair();replay.requests[0].resolve({ok:false,status:401});await settle();assert.match(replay.nodes['pair-status'].textContent,/expired|used/);assert.equal(replay.nodes['pair-help'].hidden,false);assert.equal(replay.navigations.length,0);
 const lost=pair();lost.requests[0].reject(new Error('lost'));await settle();assert.match(lost.nodes['pair-status'].textContent,/confirm/);assert.equal(lost.requests.length,1);assert.equal(Object.keys(lost.events).length,0);
 const malicious=pair();malicious.requests[0].resolve({ok:true,status:200,redirected:true,url:'https://evil.example/dashboard/sessions'});await settle();assert.equal(malicious.navigations.length,0);assert.match(malicious.nodes['pair-status'].textContent,/confirm/);
 const empty=pair('');assert.equal(empty.requests.length,0);assert.equal(empty.history.length,1);
 const b=remote();assert.equal(b.requests.length,1);assert.equal(b.requests[0].options.method,undefined);read(b.requests[0]);await settle();b.click();b.click();assert.equal(b.requests.length,2);assert.equal(b.requests[1].options.method,'POST');assert.equal(b.requests[1].options.body.get('csrf'),'synthetic-csrf');image(b.requests[1]);await settle();
 const displayed=b.ticket;assert.equal(displayed.elements.output.hidden,false);assert.equal(displayed.elements.image.src,'blob:ticket-1');b.event('online');read(b.requests[2]);await settle();assert.equal(b.ticket,displayed);assert.equal(b.requests.filter(r=>r.options.method==='POST').length,1);
 b.advance(120000);assert.equal(displayed.elements.output.hidden,true);assert.match(displayed.elements.result.textContent,/expired/);assert.deepEqual(b.revoked,['blob:ticket-1']);assert.equal(b.requests.filter(r=>r.options.method==='POST').length,1);
 b.click();b.requests[3].reject(new Error('lost mint response'));await settle();assert.equal(b.ticket.elements.button.disabled,false);b.event('online');read(b.requests[4]);await settle();assert.equal(b.requests.filter(r=>r.options.method==='POST').length,2);
 console.log('fragment erasure, one-shot redemption, expiry, image preservation and no auto write replay passed');
})().catch(e=>{console.error(e);process.exit(1)});
`
