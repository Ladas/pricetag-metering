'use strict';
const {test}=require('node:test');
const assert=require('node:assert/strict');
const fs=require('node:fs'),path=require('node:path');
const dashboard=name=>fs.readFileSync(path.join(__dirname,'../../internal/dashboard',name),'utf8');
const html=dashboard('admin.html');
test('management tasks have separate navigation panels',()=>{
 for(const id of ['user-keys','user-quota','model-pricing']){
  assert.ok(html.includes('id="vtab-'+id+'"'));
  assert.ok(html.includes('id="tab-'+id+'"'));
 }
 assert.ok(html.indexOf('<!-- MANAGED_USERS -->')<html.indexOf('id="partner-users-list"'));
 assert.match(html, /option value="5" selected/);
 assert.match(html, /id="partner-users-state"/);
});
test('user table displays login and offers editing without opening detail accidentally',()=>{
 assert.match(html, /<th>Login<\/th>/);
 assert.match(html, /editManagedUser/);
 assert.match(html, /event\.stopPropagation\(\)/);
});

test('usage identifies impersonation before showing the budget',()=>{
 const usage=dashboard('dashboard.html');
 assert.ok(usage.indexOf('id="impersonation-banner"')<usage.indexOf('id="standalone-budget"'));
});

test('role and manager clicks and key presses stop row activation',()=>{
 const vm=require('node:vm'),elements={'partner-users-list':{}};
 const ctx={partnerUsersAll:[{user_id:'id',logins:['alice'],tags:{first_name:'Alice'},role:'user',active:true}],partnerManagersAll:[],
 document:{getElementById:id=>elements[id]},esc:String,countryFlag:()=>'',renderPartnerUsersPager:()=>{}};
 vm.createContext(ctx);
 vm.runInContext(html.slice(html.indexOf('function renderPartnerUsers()'),html.indexOf('function openPartnerDetails(')),ctx);
 ctx.renderPartnerUsers();
 for(const role of ['partner-manager','partner-role']){
  const tag=elements['partner-users-list'].innerHTML.match(new RegExp('<select[^>]*'+role+'[^>]*>'))[0];
  for(const eventName of ['onclick','onkeydown']){
   let stopped=0;new Function('event',tag.match(new RegExp(eventName+'="([^"]+)"'))[1])({stopPropagation(){stopped++;}});
   assert.equal(stopped,1,role+' '+eventName);
  }
 }
});

test('paged directory defaults to five, includes state, and discards stale responses',async()=>{
 const vm=require('node:vm'),requests=[],elements={'partner-users-state':{value:'inactive'},'partner-users-count':{},'partner-users-list':{}};
 let rendered=0;
 const ctx={document:{getElementById:id=>elements[id]},encodeURIComponent,
 fetchJSON:q=>new Promise(resolve=>requests.push({q,resolve})),loadRoles:()=>{},renderPartnerUsers:()=>{rendered++;}};
 vm.createContext(ctx);
 const start=html.indexOf('var partnerUsersAll =');
 vm.runInContext(html.slice(start,html.indexOf('function renderPartnerUsersPager()',start)),ctx);
 ctx.loadPartnerUsers(0);ctx.loadPartnerUsers(1);
 assert.match(requests[0].q,/limit=5&offset=0&state=inactive/);
 assert.match(requests[1].q,/offset=5/);
 requests[1].resolve({users:[{user_id:'new'}],total:6});await new Promise(resolve=>setImmediate(resolve));
 requests[0].resolve({users:[{user_id:'old'}],total:6});await new Promise(resolve=>setImmediate(resolve));
 assert.equal(rendered,1);assert.equal(ctx.partnerUsersAll[0].user_id,'new');
});

test('editable tabs do not auto-refresh away unsaved input and disabled features stay hidden',()=>{
 assert.ok(html.includes("['people','safety-net','user-keys','user-quota','model-pricing','pricing-discounts'].includes(activeTab)"));
 assert.ok(html.includes('.vtab[hidden]{display:none}'));
});
