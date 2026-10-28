'use strict';
const fs = require('node:fs');
const vm = require('node:vm');
const assert = require('node:assert/strict');
const html = fs.readFileSync('internal/dashboard/management.html', 'utf8');
const source = html.slice(html.indexOf('function gatewayRows(){'), html.indexOf('async function loadGateway(){'));
function element() {
  return {children:[], replaceChildren(){this.children=[];}, append(...nodes){this.children.push(...nodes);}};
}
const elements = Object.fromEntries(['providerList','providerNames','gatewayModels','gatewayState'].map(id=>[id,element()]));
elements.gatewayModel={elements:{provider:element()}};
const context={el:id=>elements[id],document:{createElement:element},
 gatewayDraft:{providers:[{name:'one',urls:{openai:'https://example.invalid/v1'}},{name:'two',urls:{openai:'https://example.invalid/v1'}}],
 models:[{id:'two/model',model:'model',provider:'two',apis:['openai'],context:100,output:10}]},
 gatewayCredentials:{two:'synthetic-pending-key'}};
vm.createContext(context);vm.runInContext(source,context);
context.gatewayRows();
const remove=()=>elements.providerList.children[1].children.find(node=>node.textContent==='Remove provider');
assert.equal(remove().disabled,true);
context.gatewayDraft.models=[];context.gatewayRows();
assert.equal(remove().disabled,false);remove().onclick();
assert.equal(context.gatewayDraft.providers.length,1);
assert.equal(context.gatewayCredentials.two,undefined);
assert.equal(elements.providerList.children[0].children.find(node=>node.textContent==='Remove provider').disabled,true);
assert.equal(elements.gatewayState.textContent,'Provider removal staged; not active yet');
console.log('PASS: provider removal requires removing models, clears staged credentials and retains one provider');
