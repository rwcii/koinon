package core

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// Synthetic restart/form recovery evidence; no daemon or browser account is touched.
func TestDeviceBrowserExpiredFormRecovery(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node not installed")
	}
	script := filepath.Join(t.TempDir(), "devices.cjs")
	if err = os.WriteFile(script, []byte(deviceBrowserScript), 0600); err != nil {
		t.Fatal(err)
	}
	source, err := filepath.Abs("web/static/devices.js")
	if err != nil {
		t.Fatal(err)
	}
	if out, err := exec.Command(node, script, source).CombinedOutput(); err != nil {
		t.Fatalf("device browser contract: %v\n%s", err, out)
	}
}

const deviceBrowserScript = `
const fs=require('fs'),vm=require('vm'),assert=require('assert/strict');
const requests=[], nodes=[],status={textContent:''};
function node(tag){return {tag,children:[],append(...items){this.children.push(...items)},setAttribute(){},addEventListener(name,fn){this[name]=fn},replaceChildren(){this.children=[]}}}
const container=node('div'),revoke=node('button');
const document={body:{dataset:{remote:'true'}},getElementById(id){return {'remote-devices':container,'device-status':status,'revoke-all-devices':revoke}[id]},createElement(tag){const n=node(tag);nodes.push(n);return n}};
const navigations=[],window={location:{assign(url){navigations.push(url)}},koinonRecovery:{unauthorized(){}}};
vm.runInNewContext(fs.readFileSync(process.argv[2],'utf8'),{document,window,URLSearchParams,Date,fetch(url,options){return new Promise(resolve=>requests.push({url,options,resolve}))}});
const settle=()=>new Promise(r=>setImmediate(r));
function deviceData(csrf){return {csrf,current:{id:'this-browser'},devices:[{id:'other-browser',label:'Other',created_at:1,last_observed:1,expires_at:9999999}]};}
function read(request,csrf){request.resolve({ok:true,status:200,json:async()=>deviceData(csrf)});}
(async()=>{
 assert.equal(requests.length,1);assert.equal(requests[0].options.method,undefined);
 read(requests[0],'csrf-before-restart');await settle();
 const rename=nodes.find(n=>n.textContent==='Rename'),label=nodes.find(n=>n.tag==='input'),row=container.children[0];
 label.value='My edited label';rename.click();
 assert.equal(requests[1].options.body.get('csrf'),'csrf-before-restart');
 requests[1].resolve({ok:false,status:403,text:async()=> 'form expired'});await settle();
 assert.equal(requests.length,3);assert.equal(requests[2].options.method,undefined);assert.equal(requests[2].options.cache,'no-store');
 assert.equal(requests.filter(r=>r.options.method==='POST').length,1);
 read(requests[2],'csrf-after-restart');await settle();
 assert.equal(container.children[0],row);assert.equal(label.value,'My edited label');
 assert.match(status.textContent,/Form expired.*label text is kept.*explicitly/);
 assert.equal(requests.length,3);assert.equal(navigations.length,0);
 rename.click();assert.equal(requests.length,4);assert.equal(requests[3].options.method,'POST');
 assert.equal(requests[3].options.body.get('csrf'),'csrf-after-restart');assert.equal(requests[3].options.body.get('label'),'My edited label');
 // A failed metadata read reports failure and leaves the pending edit usable.
 requests[3].resolve({ok:false,status:403,text:async()=> 'form expired'});await settle();
 requests[4].resolve({ok:false,status:503});await settle();
 assert.match(status.textContent,/unavailable.*503/);assert.equal(label.value,'My edited label');
 assert.equal(requests.filter(r=>r.options.method==='POST').length,2);assert.equal(navigations.length,0);
 console.log('device CSRF refresh preserves labels and requires explicit resubmit');
})().catch(error=>{console.error(error);process.exitCode=1});
`
