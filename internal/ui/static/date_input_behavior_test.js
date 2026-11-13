const {test}=require('node:test');
const assert=require('node:assert/strict');
const fs=require('node:fs');
const vm=require('node:vm');
const code=fs.readFileSync(__dirname+'/date-input.js','utf8');
class Clock extends Date {constructor(...args){super(...(args.length?args:[2026,10,13,15,0,0]));}}
function setup(type='date',value=''){
 const handlers={},events=[];
 const document={getElementById(){return {textContent:'{"invalid":"Invalid date"}'};},addEventListener(name,fn){(handlers[name]??=[]).push(fn);}};
 const input={tagName:'INPUT',type,value,disabled:false,readOnly:false,error:'',blurred:0,trustedInputs:0,
   setCustomValidity(v){this.error=v;},reportValidity(){this.reported=true;},select(){this.selected=true;},
   dispatchEvent(ev){events.push(ev.type);fire(ev.type,{target:this,isTrusted:false});},
   blur(){this.blurred++;fire('blur',{target:this});}};
 function fire(name,extra={}){const ev={target:input,preventDefault(){this.defaultPrevented=true;},stopPropagation(){this.cancelBubble=true;},...extra};for(const fn of handlers[name]||[])fn(ev);return ev;}
 vm.runInNewContext(code,{document,window:{},Date:Clock,WeakMap,Event:class{constructor(type){this.type=type;}}});
 function key(key){const ev=fire('keydown',{key});if(!ev.defaultPrevented){if(key==='Enter')input.submitted=true;else if(input.type==='text' && (/^.$/.test(key)||key==='Backspace'||key==='Delete')){if(input.selected){input.value='';input.selected=false;}if(key!=='Backspace'&&key!=='Delete')input.value+=key;else input.value=input.value.slice(0,-1);input.trustedInputs++;fire('input',{isTrusted:true});}}return ev;}
 return {input,key,fire,events,type(text){for(const ch of text)key(ch);}};
}
for(const [typed,expected] of [['7','2026-11-07'],['07','2026-11-07'],['7.2','2026-02-07'],['07/02','2026-02-07'],['0702','2026-02-07'],['29.02.2024','2024-02-29'],['29022024','2024-02-29'],['2024-02-29','2024-02-29']]){
 test('public date keyboard: '+typed,()=>{const c=setup();c.type(typed);const enter=c.key('Enter');assert.equal(c.input.value,expected);assert.equal(c.input.type,'date');assert.equal(enter.defaultPrevented,true);assert.equal(c.input.submitted,undefined);assert.ok(c.input.trustedInputs>0);assert.deepEqual(c.events,['input','change']);});
}
for(const typed of ['31.02','29.02.2025','00','32','7.13','7.2.0000'])test('invalid date stays editable: '+typed,()=>{const c=setup();c.type(typed);c.key('Enter');assert.equal(c.input.value,typed);assert.equal(c.input.type,'text');assert.ok(c.input.error);assert.equal(c.input.reported,true);assert.equal(c.input.submitted,undefined);});
test('blur completes calendar date, without creating a submit',()=>{const c=setup();c.type('3.4');c.input.blur();assert.equal(c.input.value,'2026-04-03');assert.equal(c.input.submitted,undefined);});
test('datetime preserves clock when only the date is entered',()=>{const c=setup('datetime-local','2021-03-06T12:34:56');c.type('7');c.key('Enter');assert.equal(c.input.value,'2026-11-07T12:34:56');});
test('datetime accepts explicit local clock',()=>{const c=setup('datetime-local');c.type('7.2 09:08');c.key('Enter');assert.equal(c.input.value,'2026-02-07T09:08');});
test('datetime rejects invalid clock',()=>{const c=setup('datetime-local');c.type('7.2 25:08');c.key('Enter');assert.ok(c.input.error);});
test('Escape restores the previous native date',()=>{const c=setup('date','2020-01-02');c.type('7');c.key('Escape');assert.equal(c.input.value,'2020-01-02');assert.equal(c.input.type,'date');assert.equal(c.input.submitted,undefined);});
test('Delete remains a real edit before committing empty',()=>{const c=setup('date','2020-01-02');c.key('Delete');c.key('Enter');assert.equal(c.input.value,'');assert.equal(c.input.type,'date');assert.equal(c.input.trustedInputs,1);});
test('unmodified native date Enter does not submit',()=>{const c=setup('date','2026-11-13');c.key('Enter');assert.equal(c.input.type,'date');assert.equal(c.input.value,'2026-11-13');assert.equal(c.input.submitted,undefined);assert.equal(c.input.blurred,1);});
test('readonly and modifier behavior stays native',()=>{const c=setup('date','2026-11-13');c.input.readOnly=true;c.type('7');assert.equal(c.input.type,'date');assert.equal(c.input.value,'2026-11-13');const ev=c.fire('keydown',{key:'Enter',ctrlKey:true});assert.equal(ev.defaultPrevented,undefined);});
