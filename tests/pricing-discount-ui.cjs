'use strict';
// Exercise the shipped save handler without a browser or external price fetch.
const fs = require('node:fs');
const vm = require('node:vm');
const assert = require('node:assert/strict');
const html = fs.readFileSync('internal/dashboard/admin.html', 'utf8');
const start = html.indexOf('function savePricingDiscounts()');
const end = html.indexOf('\nfunction ', start + 1);
const source = html.slice(start, end);
async function check(response, fails) {
  const errors = [];
  let reloads = 0;
  const context = {
    pricingDiscountDraft: [], syncPricingDiscountDraft() {},
    updateJSON: async () => ({}), fetch: async () => response,
    loadPricingDiscounts() { reloads++; }, esc: String,
    document: {getElementById(id) {
      return id === 'pricing-discount-version' ? {value: 'test'} :
        {insertAdjacentHTML(_where, value) { errors.push(value); }};
    }},
  };
  vm.createContext(context); vm.runInContext(source, context);
  context.savePricingDiscounts();
  await new Promise(resolve => setImmediate(resolve));
  assert.equal(errors.length, fails ? 1 : 0);
  assert.equal(reloads, fails ? 0 : 1);
}
(async () => {
  await check({ok:false,status:503,json:async()=>({})}, true);
  await check({ok:true,status:200,json:async()=>({error:'upstream unavailable'})}, true);
  await check({ok:true,status:200,json:async()=>({updated:3})}, false);
  console.log('PASS: discount UI reports HTTP/application refresh errors and reloads only after success');
})().catch(error => { console.error(error.message); process.exitCode=1; });
