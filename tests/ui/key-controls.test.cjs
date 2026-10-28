'use strict';
const {test}=require('node:test');
const assert=require('node:assert/strict');
const fs=require('node:fs'),vm=require('node:vm'),path=require('node:path');
function controls(extra={}){const context=vm.createContext({Date,URL,Blob,setTimeout,clearTimeout,...extra});vm.runInContext(fs.readFileSync(path.join(__dirname,'../../internal/dashboard/management-key-controls.js'),'utf8')+'\nglobalThis.controls=KeyControls;',context);return context.controls;}
test('pending submissions are ignored; success and failure both unlock for a deliberate next request',async()=>{
 const c=controls();let release,calls=0;const button={disabled:false},form={querySelectorAll:()=>[button]};
 const run=c.singleFlight(form,async()=>{calls++;await new Promise(resolve=>release=resolve);});
 const first=run();assert.equal(button.disabled,true);let prevented=false;await run({preventDefault(){prevented=true;}});assert.equal(prevented,true);assert.equal(calls,1);release();await first;assert.equal(button.disabled,false);
 const second=run();assert.equal(calls,2);release();await second;
 const fail=c.singleFlight(form,async()=>{throw new Error('unavailable');});await assert.rejects(fail());assert.equal(button.disabled,false);
});
test('key status preserves revoked and expired records and fails closed for unknown status',()=>{
 const c=controls(),now=Date.parse('2026-10-28T12:00:00Z');
 assert.equal(c.state({status:'active',expirationDate:'2026-10-29T12:00:00Z'},now),'active');
 assert.equal(c.state({status:'active',expirationDate:'2026-10-27T12:00:00Z'},now),'expired');
 assert.equal(c.state({status:'revoked',expirationDate:'2026-10-29T12:00:00Z'},now),'revoked');
 assert.equal(c.state({},now),'unknown');
 assert.equal(c.remaining({expirationDate:'2026-10-29T14:00:00Z'},now),'1d 2h remaining');
 assert.equal(c.remaining({expiresAt:'2026-10-28T12:01:30Z'},now),'1m 30s remaining');
 assert.equal(c.remaining({expirationDate:'2026-10-27T12:00:00Z'},now),'Expired');
 assert.equal(c.remaining({},now),'No expiry reported');
 assert.equal(c.remaining({expirationDate:'broken'},now),'Expiry unavailable');
});
test('secret delivery never puts the key in the DOM and clear disables copy/download',async()=>{
 const c=controls();const buttons=[{disabled:false},{disabled:false}];const panel={dataset:{},hidden:false,querySelectorAll:()=>buttons};
 const secret=c.delivery(panel);secret.set({id:'id',key:'test-fixture-only'});assert.equal(panel.hidden,false);assert.equal(secret.value(),'test-fixture-only');secret.clear();assert.equal(panel.hidden,true);assert.equal(secret.value(),'');assert.equal(buttons[0].disabled,true);
 assert.throws(()=>secret.set({id:'id'}));assert.equal(secret.value(),'');
});

test('copy is explicit and clipboard denial leaves download available',async()=>{
 let copied;const c=controls({navigator:{clipboard:{writeText:async value=>{copied=value;}}}});
 const panel={dataset:{},hidden:false,querySelectorAll:()=>[]};const delivery=c.delivery(panel);
 delivery.set({id:'fixture-id',key:'fixture-key-only'});assert.equal(copied,undefined);await delivery.copy();assert.equal(copied,'fixture-key-only');delivery.clear();await assert.rejects(delivery.copy());
 const denied=controls({navigator:{clipboard:{writeText:async()=>{throw new Error('denied');}}}}).delivery(panel);
 denied.set({id:'fixture-id',key:'fixture-key-only'});await assert.rejects(denied.copy(),/Use Download/);assert.equal(panel.hidden,false);assert.equal(denied.value(),'fixture-key-only');
});
