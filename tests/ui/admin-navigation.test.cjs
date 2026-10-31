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

test('admin editors share card headers, tables remain expanded and status is not printed',()=>{
 assert.ok(html.includes('<p role="status" id="status" hidden>'));
 assert.ok(html.indexOf('<div class="card">\n<!-- MANAGED_USERS -->')>=0);
 const budgets=dashboard('management-admin-budgets.html'),prices=dashboard('management-admin-prices.html');
 assert.ok(!budgets.includes('<details'));
 assert.ok(!prices.includes('<details'));
 for(const content of [budgets,prices,dashboard('management-admin-users.html')])assert.ok(content.includes('class="card-hdr"'));
});

test('personal page uses cards and a searchable status-filtered table without redundant identity text',()=>{
 const page=dashboard('management.html');
 assert.ok(!page.includes('List my keys'));assert.ok(!page.includes('Signed in as'));
 for(const id of ['keySearch','keyState','keyPageSize','keyPages','refreshKeys'])assert.ok(page.includes('id="'+id+'"'));
 for(const status of ['active','revoked','expired','all'])assert.ok(page.includes('value="'+status+'"'));
 assert.ok(page.includes('class="card-hdr"'));assert.ok(page.includes('class="card-title"'));
 assert.ok(page.includes('Key management is unavailable while viewing as another user.'));
});


test('My keys sits immediately before Admin on every shared navigation bar',()=>{
 for(const name of ['management.html','admin.html','dashboard.html','manager.html']){
  const nav=dashboard(name).match(/<nav>[\s\S]*?<\/nav>/)[0];
  assert.match(nav, /href="\/management"[^>]*>My keys<\/a>\s*<a href="\/admin"/);
 }
});

test('personal key workspace has consistently named list and creation tabs',()=>{
 const page=dashboard('management.html');
 assert.ok(!page.includes('Your keys'));assert.ok(!page.includes('Create a key</h2>'));
 for(const id of ['vtab-my-keys','vtab-create-key','tab-my-keys','tab-create-key'])assert.ok(page.includes('id="'+id+'"'));
 assert.match(page, /role="tablist"/);
 assert.match(page, /id="vtab-my-keys"[^>]*aria-selected="true"/);
 assert.match(page, /id="tab-create-key"[^>]*hidden/);
});
