const {test}=require('node:test');
const assert=require('node:assert/strict');
const vm=require('node:vm');
const fs=require('node:fs');
const code=fs.readFileSync(__dirname+'/report-controls.js','utf8');
function fixture(compact=true){
 const listeners={},preset={value:'previous'},calls=[];
 const fields=[{tagName:'INPUT',type:'text',disabled:false,readOnly:false,getClientRects:()=>[{}],focus(){this.focused=true;},blur(){this.blurred=true;}},{tagName:'INPUT',type:'number',disabled:false,readOnly:false,getClientRects:()=>[{}],focus(){this.focused=true;},blur(){this.blurred=true;}}];
 const form={querySelectorAll:()=>fields,querySelector:()=>preset};
 for(const el of fields){el.form=form;el.closest=selector=>selector==='main.ob-report-compact'?(compact?{}:null):selector==='form[data-ob-report-params]'?form:null;}
 vm.runInNewContext(code,{document:{addEventListener(name,fn){listeners[name]=fn;}},window:{obReportRemoveSettingsInput(f){calls.push(f);}},Array});
 function dispatch(name,target,extra={}){const e={target,key:'Enter',preventDefault(){this.defaultPrevented=true;},stopPropagation(){this.stopped=true;},...extra};listeners[name](e);return e;}
 return {fields,preset,form,calls,dispatch};
}
test('Enter commits a parameter without submitting',()=>{const c=fixture();const e=c.dispatch('keydown',c.fields[0]);assert.equal(e.defaultPrevented,true);assert.equal(e.stopped,true);assert.equal(c.fields[1].focused,true);});
test('Enter at last parameter blurs without submitting',()=>{const c=fixture();c.dispatch('keydown',c.fields[1]);assert.equal(c.fields[1].blurred,true);});
test('ordinary operator page and modifiers stay unchanged',()=>{let c=fixture(false);assert.equal(c.dispatch('keydown',c.fields[0]).defaultPrevented,undefined);c=fixture();assert.equal(c.dispatch('keydown',c.fields[0],{ctrlKey:true}).defaultPrevented,undefined);});
test('Generate button keeps native keyboard activation',()=>{const c=fixture();const e=c.dispatch('keydown',{...c.fields[0],tagName:'BUTTON',type:'submit'});assert.equal(e.defaultPrevented,undefined);});
test('variant change clears stale settings but waits for Generate',()=>{const c=fixture();const el=c.fields[0];el.closest=selector=>selector==='main.ob-report-compact'?{}:selector==='form[data-ob-report-params]'?c.form:selector==='[data-ob-report-variant-submit]'?el:null;const e=c.dispatch('change',el);assert.equal(e.stopped,true);assert.equal(c.calls.length,1);assert.equal(c.preset.value,'__standard');});
