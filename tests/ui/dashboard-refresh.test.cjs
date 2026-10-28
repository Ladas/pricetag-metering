'use strict';
const {test}=require('node:test');
const assert=require('node:assert/strict');
const fs=require('node:fs'),path=require('node:path'),vm=require('node:vm');
const html=fs.readFileSync(path.join(__dirname,'../../internal/dashboard/dashboard.html'),'utf8');
const refresh=html.slice(html.indexOf('async function refresh(force)'),html.indexOf('\nfunction timeAgo'));
function setup(isAdmin=false){
 const elements={'refresh-btn':{},'last-refresh':{}},calls=[];
 const ctx={isAdmin,range:'mtd',userFilter:[],groupFilter:'',modelFilter:'',userPieMetric:'cost',savingsRef:'',
  document:{getElementById:id=>elements[id]},calls,
  api:async url=>{calls.push(url);if(url.includes('directory-users'))throw new Error('Forbidden');return url.includes('overview')?{total_requests:3}:[];},
  collapseUsageUsers:x=>x,mergeDirectoryUsers:x=>x};
 for(const name of ['renderKPI','renderGroupFilterBtns','populateFilterDropdowns','renderRecent','renderModelChart','renderToolChart','renderUserCostChart','renderSavings'])ctx[name]=()=>{};
 vm.createContext(ctx);vm.runInContext(refresh,ctx);
 return {ctx,elements,calls};
}
test('personal Usage renders without requesting the administrative directory',async()=>{
 const {ctx,elements,calls}=setup();await ctx.refresh(true);
 assert.equal(ctx.cachedOverview.total_requests,3);assert.equal(calls.some(p=>p.includes('directory-users')),false);
 assert.equal(elements['refresh-btn'].disabled,false);assert.ok(!elements['last-refresh'].textContent.includes('could not'));
 assert.ok(calls.every(p=>!p.includes('&user=')),'server must resolve all logins of the authenticated identity');
});
test('admin directory failure is visible and successful retry restores rendering',async()=>{
 const {ctx,elements,calls}=setup(true);await ctx.refresh(true);
 assert.ok(calls.some(p=>p.includes('directory-users')));assert.match(elements['last-refresh'].textContent,/could not be loaded/);
 assert.equal(elements['refresh-btn'].disabled,false);
 ctx.api=async url=>url.includes('overview')?{total_requests:4}:[];
 await ctx.refresh(true);assert.equal(ctx.cachedOverview.total_requests,4);assert.ok(!elements['last-refresh'].textContent.includes('could not'));
});
