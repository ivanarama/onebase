const {test} = require('node:test');
const assert = require('node:assert/strict');
const fs = require('node:fs');
const vm = require('node:vm');
const code = fs.readFileSync(__dirname+'/date-input.js', 'utf8');
function setup(type='date', value='') {
  const handlers={}, events=[], attributes={};
  const labels={invalid:'Invalid date',incompleteDate:'Complete date required',incompleteDatetime:'Complete date and time required'};
  const document={activeElement:null,getElementById(){return {textContent:JSON.stringify(labels)};},addEventListener(k,fn){(handlers[k]??=[]).push(fn);}};
  const window={};
  const input={tagName:'INPUT',name:'Day',type,value,disabled:false,readOnly:false,required:false,error:'',selectionStart:0,selectionEnd:0,
    setCustomValidity(v){this.error=v;},get validationMessage(){return this.error;},reportValidity(){this.reported=true;},
    getAttribute(k){return attributes[k]??null;},setAttribute(k,v){attributes[k]=String(v);},removeAttribute(k){delete attributes[k];},
    setSelectionRange(a,b){this.selectionStart=a;this.selectionEnd=b;},select(){this.setSelectionRange(0,this.value.length);},
    dispatchEvent(e){events.push(e.type);fire(e.type,{target:this});},
    focus(){document.activeElement=this;fire('focus');},blur(){document.activeElement=null;fire('blur');},
    showPicker(){this.pickerCalls=(this.pickerCalls||0)+1;}};
  function fire(k,extra={}) {
    const e={target:input,preventDefault(){this.defaultPrevented=true;},stopPropagation(){this.stopped=true;},stopImmediatePropagation(){this.immediate=true;},...extra};
    for (const f of handlers[k]||[]) f(e);
    return e;
  }
  class LocalDate extends Date {
    constructor(...args) { super(...(args.length ? args : [2026, 10, 19, 12])); }
  }
  vm.runInNewContext(code,{document,window,Date:LocalDate,WeakMap,WeakSet,Event:class {constructor(type){this.type=type;}}});
  function key(key,extra={}) {return fire('keydown',{key,...extra});}
  return {input,events,window,focus(){input.focus();},key,fire,
    type(text){for(const ch of text)key(ch);},
    paste(text){return fire('paste',{clipboardData:{getData(){return text;}}});},
    submit(){const form={querySelectorAll(){return [input];},checkValidity(){return !input.error;},querySelector(){return input;}};const e=fire('submit',{target:form});if(!e.defaultPrevented)input.submitted=true;return e;}};
}
test('focus exposes the full date template without emitting changes',()=>{
  const c=setup();c.focus();assert.equal(c.input.value,'__.__.____');assert.equal(c.input.type,'text');assert.deepEqual(c.events,[]);
  assert.equal(c.input.getAttribute('placeholder'),'__.__.____');
});
test('digits insert dots and advance over separators',()=>{
  const c=setup();c.focus();c.type('16');assert.equal(c.input.value,'16.__.____');assert.equal(c.input.selectionStart,3);
  c.type('11');assert.equal(c.input.value,'16.11.____');assert.equal(c.input.selectionStart,6);
  c.type('2026');const e=c.key('Enter');assert.equal(c.input.value,'2026-11-16');assert.equal(c.input.type,'date');assert.equal(e.defaultPrevented,true);
  assert.equal(c.input.submitted,undefined);assert.equal(c.events.filter(x=>x==='change').length,1);
});
for (const [digits,expected] of [
  ['1','2026-11-01'], ['16','2026-11-16'],
  ['163','2026-03-16'], ['1609','2026-09-16'], ['1611202','2026-11-16'],
  ['1611203','2036-11-16'],
]) test('Enter completes partial date '+digits,()=>{
  const c=setup();c.focus();c.type(digits);c.key('Enter');
  assert.equal(c.input.value,expected);assert.equal(c.input.type,'date');
  assert.equal(c.input.error,'');assert.equal(c.input.submitted,undefined);
});
test('date with only month requires the day, explicitly typed zero is invalid',()=>{
  const c=setup();c.focus();c.input.setSelectionRange(3,3);c.type('11');c.key('Enter');
  assert.equal(c.input.error,'Complete date required');
  const zero=setup();zero.focus();zero.type('160');zero.key('Enter');
  assert.equal(zero.input.error,'Invalid date');
});
function eventData(c,entries=[['Day',c.input.value]]) {
  const data=new FormData();
  for(const [name,value] of entries)data.append(name,value);
  c.fire('formdata',{target:{elements:[c.input],querySelectorAll(){return [c.input];}},formData:data});
  return data;
}
test('FormData completes a valid partial date-time without committing the draft',()=>{
  const c=setup('datetime-local');c.focus();c.type('7');
  const display=c.input.value, events=c.events.slice();
  assert.equal(eventData(c).get('Day'),'2026-11-07T00:00:00');
  assert.equal(c.input.value,display);assert.equal(c.input.type,'text');assert.deepEqual(c.events,events);
});
test('typed separators preserve single-digit components',()=>{
  const c=setup('datetime-local');c.focus();c.type('5.2.2027 8:3');c.key('Enter');
  assert.equal(c.input.value,'2027-02-05T08:03:00');
});
for (const value of ['31022026','29022025','00112026','16132026','16110000']) test('impossible calendar date rejected: '+value,()=>{
  const c=setup();c.focus();c.type(value);c.key('Enter');assert.equal(c.input.error,'Invalid date');
});
for (const text of ['16.11.2026','16112026','2026-11-16']) test('clipboard canonicalizes complete date: '+text,()=>{
  const c=setup('date','2020-01-02');c.focus();const e=c.paste(text);c.key('Enter');assert.equal(e.defaultPrevented,true);assert.equal(c.input.value,'2026-11-16');
});
test('Backspace removes a digit and keeps punctuation and caret',()=>{
  const c=setup();c.focus();c.type('16');c.key('Backspace');assert.equal(c.input.value,'1_.__.____');assert.equal(c.input.selectionStart,1);
  c.type('7112026');c.key('Enter');assert.equal(c.input.value,'2026-11-17');
});
test('Delete edits the current slot without shifting the date',()=>{
  const c=setup('date','2026-11-16');c.focus();c.input.setSelectionRange(3,3);c.key('Delete');assert.equal(c.input.value,'16._1.2026');
  c.type('0');c.key('Enter');assert.equal(c.input.value,'2026-01-16');
});
test('selection replacement and clearing preserve mask positions',()=>{
  const c=setup('date','2026-11-16');c.focus();c.input.setSelectionRange(0,2);c.type('07');c.key('Enter');assert.equal(c.input.value,'2026-11-07');
  c.focus();c.input.select();c.key('Delete');c.key('Enter');assert.equal(c.input.value,'');assert.equal(c.input.type,'date');
});
test('date-time digits insert space and colons including seconds',()=>{
  const c=setup('datetime-local');c.focus();assert.equal(c.input.value,'__.__.____ __:__:__');
  c.type('16112026235958');c.key('Enter');assert.equal(c.input.value,'2026-11-16T23:59:58');
});
for (const [digits,expected] of [
  ['16','2026-11-16T00:00:00'], ['16092025','2025-09-16T00:00:00'],
  ['1609202508','2025-09-16T08:00:00'], ['16092025083','2025-09-16T08:03:00'],
  ['1609202508304','2025-09-16T08:30:04'],
]) test('date-time completes missing time components '+digits,()=>{
  const c=setup('datetime-local');c.focus();c.type(digits);c.key('Enter');
  assert.equal(c.input.value,expected);
});
test('paste can skip the month and preserve an entered year and time',()=>{
  const c=setup('datetime-local');c.focus();c.paste('5..2028 9:05');c.key('Enter');
  assert.equal(c.input.value,'2028-11-05T09:05:00');
});
test('date-only paste preserves existing time',()=>{
  const c=setup('datetime-local','2020-01-02T12:34:56');c.focus();c.paste('16.11.2026');c.key('Enter');assert.equal(c.input.value,'2026-11-16T12:34:56');
});
test('invalid time rejected',()=>{
  const c=setup('datetime-local');c.focus();c.type('16112026256000');c.key('Enter');assert.equal(c.input.error,'Invalid date');
});
test('letters are not inserted into the fixed template',()=>{
  const c=setup();c.focus();assert.equal(c.key('x').defaultPrevented,true);assert.equal(c.input.value,'__.__.____');
});
test('Escape restores the original canonical field',()=>{
  const c=setup('date','2020-01-02');c.focus();c.input.select();c.type('16');c.key('Escape');assert.equal(c.input.value,'2020-01-02');assert.equal(c.input.type,'date');assert.equal(c.input.error,'');
});

for (const modifier of ['ctrlKey','metaKey']) test('paste shortcut reopens mask after Escape: '+modifier,()=>{
  const c=setup('date','2020-01-02');c.focus();c.key('Escape');
  const e=c.key('v',{[modifier]:true});assert.equal(e.defaultPrevented,undefined);assert.equal(c.input.type,'text');
  c.paste('16112026');c.key('Enter');assert.equal(c.input.value,'2026-11-16');
});
test('readonly Enter neither submits nor changes the value',()=>{
  const c=setup('date','2026-11-16');c.input.readOnly=true;c.focus();const e=c.key('Enter');assert.equal(e.defaultPrevented,true);assert.equal(c.input.type,'date');assert.equal(c.input.value,'2026-11-16');assert.deepEqual(c.events,[]);
});
test('submit completes a partial active editor',()=>{
  const c=setup();c.focus();c.type('16');c.submit();assert.equal(c.input.value,'2026-11-16');assert.equal(c.input.submitted,true);
});
test('blur completes a date just like Enter',()=>{
  const c=setup();c.focus();c.type('1609');c.input.blur();assert.equal(c.input.value,'2026-09-16');
  assert.equal(c.events.filter(event=>event==='change').length,1);
});
test('submit serializes a complete active editor as ISO',()=>{
  const c=setup();c.focus();c.type('16112026');c.submit();assert.equal(c.input.value,'2026-11-16');assert.equal(c.input.submitted,true);
});
test('programmatic managed-form result replaces an active draft',()=>{
  const c=setup();c.focus();c.type('1');assert.equal(c.window.obSetDateInputValue(c.input,'2027-02-03T00:00:00'),true);
  assert.equal(c.input.value,'03.02.2027');c.key('Enter');assert.equal(c.input.value,'2027-02-03');
});
test('Alt+Down retains the native picker and field identity',()=>{
  const c=setup('date','2026-11-16');c.focus();const e=c.key('ArrowDown',{altKey:true});assert.equal(e.defaultPrevented,true);assert.equal(c.input.pickerCalls,1);assert.equal(c.input.type,'date');assert.equal(c.input.value,'2026-11-16');
});
