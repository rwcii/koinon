package core

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// Synthetic browser evidence only: storage failures, explicit draft recovery and
// pending receipt resolution never issue a write or persist message bodies.
func TestRemoteBrowserRecovery(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node not installed")
	}
	script := filepath.Join(t.TempDir(), "recovery.cjs")
	if err := os.WriteFile(script, []byte(remoteRecoveryScript), 0600); err != nil {
		t.Fatal(err)
	}
	source, _ := filepath.Abs("web/static/recovery.js")
	if out, err := exec.Command(node, script, source).CombinedOutput(); err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
}

const remoteRecoveryScript = `
const fs=require('fs'),vm=require('vm'),assert=require('assert/strict');
const code=fs.readFileSync(process.argv[2],'utf8'), settle=()=>new Promise(r=>setImmediate(r));
function browser(saved={}, broken=false, sessionID="synthetic-session", clock={}) {
 let nextID=0;
 const storage=new Map(Object.entries(saved)), events={}, requests=[], buttons=[];
 const prefix='koinon.remote.v2:https://synthetic.example:';
 const status={textContent:''},toggle={checked:false,addEventListener(n,f){this[n]=f;}},clear={addEventListener(n,f){this[n]=f;}};
 const form={getAttribute(){return '/dashboard/actions/send';},querySelectorAll(){return [field];}};
 const field={tagName:'TEXTAREA',name:'body',value:'',form,dataset:{},insertAdjacentElement(_,b){buttons.push(b);}};
 const hostTime=clock.hostTime || Date.now();
 class PhoneDate extends Date {static now(){return clock.clientTime === undefined ? Date.now() : clock.clientTime;}}
 const performance={now(){return clock.elapsed || 0;}};
 const document={body:{dataset:{remote:'true',remoteSession:sessionID,hostTime:String(hostTime),authReason:clock.authReason||''}},visibilityState:'visible',
 getElementById(id){return {'recovery-status':status,'save-drafts':toggle,'clear-drafts':clear,'attention-pending-actions':clock.panel}[id]||null;},
 querySelectorAll(selector){return selector.startsWith('textarea')?[field]:[];},addEventListener(n,f){events[n]=f;},
 createElement(){return {addEventListener(n,f){this[n]=f;},remove(){this.removed=true;},append(){}};}};
 const window={location:new URL('https://synthetic.example/dashboard/messages'),crypto:{randomUUID(){return '12345678-1234-1234-1234-'+String(++nextID).padStart(12,'0');}},
 localStorage:{getItem(k){if(broken)throw Error('blocked');return storage.get(k)||null;},setItem(k,v){if(broken)throw Error('quota');storage.set(k,v);},removeItem(k){storage.delete(k);}},addEventListener(){}};
 class FormData {constructor(form){this.form=form;}get(name){return name==='to'?(this.form.recipient||'synthetic-peer'):null;}}
 vm.runInNewContext(code,{document,window,FormData,URLSearchParams,Date:PhoneDate,performance,Promise,fetch(url,opts){return new Promise((resolve,reject)=>requests.push({url,opts,resolve,reject}));}});
 return {window,storage,prefix,status,toggle,clear,field,buttons,events,requests,api:window.koinonRecovery};
}
(async()=>{
 const b=browser();b.field.value='private draft';b.events.input({target:b.field});assert.equal(b.storage.has(b.prefix+'drafts'),false);
 b.toggle.checked=true;b.toggle.change();b.events.input({target:b.field});assert.match(b.storage.get(b.prefix+'drafts'),/private draft/);
 const resumed=browser(Object.fromEntries(b.storage));assert.equal(resumed.field.value,'');assert.equal(resumed.buttons[0].textContent,'Recover draft');resumed.buttons[0].click();assert.equal(resumed.field.value,'private draft');assert.equal(resumed.requests.length,0);
 // A recovery offer must remain bound to its original target and saved text.
 const retargeted=browser(Object.fromEntries(b.storage));
 retargeted.field.form.recipient='other-peer';retargeted.events.change({target:{form:retargeted.field.form}});
 retargeted.buttons[0].click();assert.equal(retargeted.field.value,'');
 assert.ok(!JSON.parse(retargeted.storage.get(retargeted.prefix+'drafts')).some(d=>d.target==='other-peer'));
 const newer=browser(Object.fromEntries(b.storage));newer.field.value='newer text';newer.events.input({target:newer.field});
 newer.buttons[0].click();assert.equal(newer.field.value,'newer text');
 for (const change of ['clear','opt-out','revoke','route','action']) {
  const stale=browser(Object.fromEntries(b.storage));
  if(change==='clear')stale.clear.click();
  if(change==='opt-out'){stale.toggle.checked=false;stale.toggle.change();}
  if(change==='revoke')stale.api.unauthorized({headers:{get(){return 'device_revoked';}}});
  if(change==='route')stale.window.location.pathname='/dashboard/rooms';
  if(change==='action')stale.field.form.getAttribute=()=>'/dashboard/actions/room-post';
  stale.buttons[0].click();assert.equal(stale.field.value,'',change+' must invalidate a stale offer');
 }
 // A refused full page load that names a revocation clears the drafts; an expiry keeps them.
 const revokedPage=browser(Object.fromEntries(b.storage),false,'',{authReason:'device_revoked'});
 assert.equal(JSON.parse(revokedPage.storage.get(revokedPage.prefix+'drafts')||'[]').length,0,'a revoked page load kept drafts');
 const expiredPage=browser(Object.fromEntries(b.storage),false,'',{authReason:'session_expired'});
 assert.match(expiredPage.storage.get(expiredPage.prefix+'drafts')||'',/private draft/,'an expired page load dropped drafts');
 // Unrelated private memory/editor bodies must never be persisted by message-draft opt-in.
 const memory=browser();memory.toggle.checked=true;memory.toggle.change();
 memory.field.form.getAttribute=()=>'/dashboard/actions/memory-record';memory.field.value='private memory record';
 memory.events.input({target:memory.field});assert.ok(!(memory.storage.get(memory.prefix+'drafts')||'').includes('private memory record'));
 b.toggle.checked=false;b.toggle.change();assert.equal(b.storage.has(b.prefix+'drafts'),false);
 const body=new URLSearchParams({to:'synthetic-peer',body:'never store this message'}),form={dataset:{}};
 const op=b.api.prepare(form,new URL('https://synthetic.example/dashboard/actions/send'),body);assert.ok(op.id);assert.equal(body.get('operation_id'),op.id);assert.ok(!b.storage.get(b.prefix+'pending').includes('never store'));
 // While the POST waits for its response, no receipt lookup runs for it (#673 review L10).
 b.api.resolve();assert.equal(b.requests.length,0);assert.doesNotMatch(b.status.textContent,/unknown/i);
 b.api.settled(op);
 assert.equal(b.api.prepare(form,new URL('https://synthetic.example/dashboard/actions/send'),body).blocked,true);assert.equal(b.requests.length,1);assert.equal(b.requests[0].opts.method,undefined);
 b.requests[0].resolve({ok:true,json:async()=>({found:false,authoritative:true})});await settle();
 const retry=b.api.prepare(form,new URL('https://synthetic.example/dashboard/actions/send'),body);assert.equal(retry.id,op.id);
 b.api.settled(retry);b.api.resolve();b.requests[1].resolve({ok:true,json:async()=>({state:'accepted'})});await settle();assert.match(b.status.textContent,/Accepted/);assert.equal(b.requests.filter(r=>r.opts.method==='POST').length,0);
 const rejected=browser(Object.fromEntries(resumed.storage));rejected.api.unauthorized({headers:{get(){return 'device_revoked';}}});assert.equal(rejected.storage.has(rejected.prefix+'drafts'),false);
 const failed=browser({},true);assert.match(failed.status.textContent,/unavailable/);failed.field.value='still usable';failed.events.input({target:failed.field});assert.equal(failed.field.value,'still usable');
 const expired=browser({[b.prefix+'save-drafts']:'true',[b.prefix+'drafts']:JSON.stringify([{text:'old',at:Date.now()-8*86400000}])});assert.equal(expired.buttons.length,0);

 // An expired pending operation remains unknown and cannot turn into a fresh ID.
 const oldPending={id:'aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa',at:Date.now()-8*86400000,hostAt:Date.now()-8*86400000,type:'/dashboard/actions/send',target:'synthetic-peer'};
 const old=browser({[b.prefix+'pending']:JSON.stringify([oldPending])});
 assert.equal(old.api.prepare({dataset:{}},new URL('https://synthetic.example/dashboard/actions/send'),new URLSearchParams({to:'synthetic-peer',body:'retry'})).blocked,true);
 assert.match(old.status.textContent,/Receipt window expired/);
 assert.equal(old.requests.filter(r=>r.opts.method==='POST').length,0);
 // A backwards client-clock jump must retain the pending ID and require reconciliation.
 const futurePending={id:'cccccccc-cccc-cccc-cccc-cccccccccccc',at:Date.now()+3600000,hostAt:Date.now(),type:'/dashboard/actions/send',target:'synthetic-peer'};
 const backwards=browser({[b.prefix+'pending']:JSON.stringify([futurePending])});
 assert.equal(backwards.requests.length,1);
 assert.equal(backwards.api.prepare({dataset:{}},new URL('https://synthetic.example/dashboard/actions/send'),new URLSearchParams({to:'synthetic-peer',body:'retry'})).blocked,true);
 assert.equal(backwards.requests.length,1);
 assert.equal(JSON.parse(backwards.storage.get(b.prefix+'pending'))[0].id,futurePending.id);
 // The posted room_id is the target, so one room's unknown action cannot consume another's ID.
 const rooms=browser(), roomA=new URLSearchParams({room_id:'room-a',body:'a'}), roomB=new URLSearchParams({room_id:'room-b',body:'b'});
 const firstRoom=rooms.api.prepare({dataset:{}},new URL('https://synthetic.example/dashboard/actions/room-post'),roomA);
 const nextRoom=rooms.api.prepare({dataset:{}},new URL('https://synthetic.example/dashboard/actions/room-post'),roomB);
 assert.equal(firstRoom.target,'room-a');assert.equal(nextRoom.target,'room-b');assert.notEqual(firstRoom.id,nextRoom.id);assert.equal(nextRoom.blocked,undefined);
 // A newly authenticated session reconciles the original host operation, then explicitly retries that ID.
 const reauthPending={id:'bbbbbbbb-bbbb-bbbb-bbbb-bbbbbbbbbbbb',at:Date.now()-1000,hostAt:Date.now()-1000,type:'/dashboard/actions/send',target:'synthetic-peer'};
 const restored=browser({[b.prefix+'pending']:JSON.stringify([reauthPending])},false,'new-sign-in');
 assert.equal(restored.requests.length,1);
 assert.equal(restored.api.prepare({dataset:{}},new URL('https://synthetic.example/dashboard/actions/send'),new URLSearchParams({to:'synthetic-peer',body:'retry'})).blocked,true);
 restored.requests[0].resolve({ok:true,json:async()=>({found:false,authoritative:true})});await settle();
 const restoredBody=new URLSearchParams({to:'synthetic-peer',body:'explicit retry'});
 assert.equal(restored.api.prepare({dataset:{}},new URL('https://synthetic.example/dashboard/actions/send'),restoredBody).id,reauthPending.id);
 assert.equal(restoredBody.get('operation_id'),reauthPending.id);
 assert.equal(restored.requests.filter(r=>r.opts.method==='POST').length,0);

 // A changed-payload refusal may look up the original accepted ID; keep the changed draft.
 const disputed=browser({[b.prefix+'save-drafts']:'true'});
 disputed.field.value='changed private draft';disputed.events.input({target:disputed.field});
 const disputedOperation=disputed.api.prepare({dataset:{}},new URL('https://synthetic.example/dashboard/actions/send'),new URLSearchParams({to:'synthetic-peer',body:'changed private draft'}));
 disputed.api.settled(disputedOperation);disputed.api.conflict(disputedOperation);disputed.api.resolve();
 disputed.requests[0].resolve({ok:true,json:async()=>({found:true,receipt:{state:'accepted'}})});await settle();
 assert.match(disputed.storage.get(b.prefix+'drafts'),/changed private draft/);
 // Accepted recovery of an earlier submission must preserve edits made afterwards.
 const earlier={id:'dddddddd-dddd-dddd-dddd-dddddddddddd',at:Date.now()-2000,hostAt:Date.now()-2000,type:'/dashboard/actions/send',target:'synthetic-peer',route:'/dashboard/messages'};
 const laterDraft={key:'newer',session:'old-sign-in',route:'/dashboard/messages',target:'synthetic-peer',name:'body',text:'later private edit',at:Date.now()-1000};
 const edited=browser({[b.prefix+'save-drafts']:'true',[b.prefix+'pending']:JSON.stringify([earlier]),[b.prefix+'drafts']:JSON.stringify([laterDraft])});
 edited.requests[0].resolve({ok:true,json:async()=>({found:true,receipt:{state:'accepted'}})});await settle();
 assert.match(edited.storage.get(b.prefix+'drafts'),/later private edit/);
 // A phone clock days fast or slow never supplies the host receipt timestamp.
 const hostTime=Date.now();
 for (const skew of [-8*86400000,8*86400000]) {
  const clock={hostTime,clientTime:hostTime+skew,elapsed:0};
  const timed=browser({},false,'clock-session',clock);
  clock.elapsed=1250;
  const form={dataset:{}}, body=new URLSearchParams({to:'synthetic-peer',body:'clock-safe action'});
  const original=timed.api.prepare(form,new URL('https://synthetic.example/dashboard/actions/send'),body);
  assert.equal(Number(body.get('operation_created_at')),hostTime+1250);
  assert.equal(original.at,hostTime+skew);
  // A later phone wall-clock jump cannot change the original ID/time on retry.
  clock.clientTime += 20*86400000; clock.elapsed=2000;
  timed.api.settled(original);timed.api.resolve();timed.requests[0].resolve({ok:true,json:async()=>({found:false,authoritative:true,server_time:hostTime+2000})});await settle();
  const retry=timed.api.prepare(form,new URL('https://synthetic.example/dashboard/actions/send'),body);
  assert.equal(retry.id,original.id);assert.equal(Number(body.get('operation_created_at')),hostTime+1250);
  // A new page uses a new host anchor but preserves a pending action's original time.
  const resumed=browser(Object.fromEntries(timed.storage),false,'new-clock-session',{hostTime:hostTime+3000,clientTime:hostTime-skew,elapsed:0});
  resumed.requests[0].resolve({ok:true,json:async()=>({found:false,authoritative:true,server_time:hostTime+3000})});await settle();
  const retryBody=new URLSearchParams({to:'synthetic-peer',body:'same action'});
  assert.equal(resumed.api.prepare({dataset:{}},new URL('https://synthetic.example/dashboard/actions/send'),retryBody).id,original.id);
  assert.equal(Number(retryBody.get('operation_created_at')),hostTime+1250);
  // Host elapsed time, rather than the phone wall clock, ends the retry window.
  clock.elapsed=7*86400000+2000;
  assert.equal(timed.api.prepare(form,new URL('https://synthetic.example/dashboard/actions/send'),body).blocked,true);
 }
 // A normal authenticated refresh renews the host anchor after a phone sleep
 // whose performance clock did not advance; it submits no action by itself.
 const sleepClock={hostTime,clientTime:hostTime+9*86400000,elapsed:0};
 const waking=browser({},false,'waking-session',sleepClock);
 waking.api.authenticated(true);
 waking.requests[0].resolve({ok:true,json:async()=>({csrf:'new-csrf',server_time:hostTime+3600000})});await settle();
 const wakingBody=new URLSearchParams({to:'synthetic-peer',body:'after wake'});
 waking.api.prepare({dataset:{}},new URL('https://synthetic.example/dashboard/actions/send'),wakingBody);
 assert.equal(Number(wakingBody.get('operation_created_at')),hostTime+3600000);
 assert.equal(waking.requests.filter(r=>r.opts.method==='POST').length,0);
 // Old metadata without an original host timestamp stays unknown. A lookup must
 // never turn it into a newly dated operation after receipt reclamation.
 const legacy={id:'eeeeeeee-eeee-eeee-eeee-eeeeeeeeeeee',at:Date.now(),type:'/dashboard/actions/send',target:'synthetic-peer'};
 const legacyBrowser=browser({[b.prefix+'pending']:JSON.stringify([legacy])});
 legacyBrowser.requests[0].resolve({ok:true,json:async()=>({found:false,authoritative:true,server_time:hostTime})});await settle();
 assert.equal(legacyBrowser.api.prepare({dataset:{}},new URL('https://synthetic.example/dashboard/actions/send'),new URLSearchParams({to:'synthetic-peer'})).blocked,true);
 assert.match(legacyBrowser.status.textContent,/Original host time unavailable/);
 assert.equal(JSON.parse(legacyBrowser.storage.get(b.prefix+'pending'))[0].id,legacy.id);
 // Valid JSON with the wrong storage shape is unavailable, rather than breaking forms.
 const malformed=browser({[b.prefix+'save-drafts']:'true',[b.prefix+'drafts']:'{}',[b.prefix+'pending']:'{}'});
 assert.match(malformed.status.textContent,/unavailable/);malformed.field.value='usable';malformed.events.input({target:malformed.field});assert.equal(malformed.field.value,'usable');
 // A phone clock that moved back keeps a draft written before the change (#673 review L11).
 const ahead={key:'ahead',session:'synthetic-session',route:'/dashboard/messages',target:'synthetic-peer',name:'body',text:'draft from a fast clock',at:Date.now()+3600000};
 const slow=browser({[b.prefix+'save-drafts']:'true',[b.prefix+'drafts']:JSON.stringify([ahead])});
 assert.equal(slow.buttons.length,1);assert.match(slow.storage.get(b.prefix+'drafts'),/draft from a fast clock/);
 // A pending entry without a string type or target is dropped, and Attention still renders (#673 review L12).
 const panel={children:[],replaceChildren(){this.children=[]},append(n){this.children.push(n)}};
 const bad={id:'ffffffff-ffff-ffff-ffff-ffffffffffff',at:Date.now(),hostAt:Date.now(),target:'synthetic-peer'};
 const good={id:'abababab-abab-abab-abab-abababababab',at:Date.now(),hostAt:Date.now(),type:'/dashboard/actions/send',target:'synthetic-peer'};
 const attention=browser({[b.prefix+'pending']:JSON.stringify([bad,good])},false,'synthetic-session',{panel});
 assert.equal(attention.requests.length,1);assert.match(attention.requests[0].url,/abababab/);assert.equal(panel.children.length,1);
 console.log('synthetic remote recovery passed');
})().catch(e=>{console.error(e);process.exitCode=1;});
`
