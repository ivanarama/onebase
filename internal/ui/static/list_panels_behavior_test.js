const {test}=require('node:test');
const assert=require('node:assert/strict');
const vm=require('node:vm');
const fs=require('node:fs');
const src=fs.readFileSync(__dirname+'/list-controls.js','utf8');
const fn=src.slice(src.indexOf('function obInitListPanels()'),src.indexOf('\n  obReady(obInitListPanels)', src.indexOf('function obInitListPanels()')));
test('list panels: collapsed defaults, per-user and per-list restore, safe unavailable storage',()=>{
  const saved=new Map();
  function load(user,path='/ui/document/task',broken=false){
    const panels=['filters','settings'].map(name=>({open:false,getAttribute:()=>name,addEventListener(_,fn){this.toggle=fn;}}));
    const root={getAttribute:()=>user,querySelectorAll:()=>panels};
    const c={document:{querySelector:()=>root},location:{pathname:path},localStorage:{getItem(k){if(broken)throw Error();return saved.get(k);},setItem(k,v){if(broken)throw Error();saved.set(k,v);}}};
    vm.runInNewContext(fn+';obInitListPanels();',c);
    return panels;
  }
  let a=load('anna');assert.deepEqual(a.map(p=>p.open),[false,false]);
  a.forEach(p=>{p.open=true;p.toggle();});
  assert.deepEqual(load('anna').map(p=>p.open),[true,true]);
  assert.deepEqual(load('boris').map(p=>p.open),[false,false]);
  assert.deepEqual(load('anna','/ui/catalog/items').map(p=>p.open),[false,false]);
  a=load('anna');a[0].open=false;a[0].toggle();
  assert.deepEqual(load('anna').map(p=>p.open),[false,true]);
  assert.doesNotThrow(()=>load('anna',undefined,true));
});

test('list field Enter completes filters, preserves Search and real button submits',()=>{
  const handlers={};
  const document={addEventListener(name,fn){(handlers[name]||=[]).push(fn);},querySelectorAll(){return [];}};
  vm.runInNewContext(src,{document,window:{addEventListener(){}},obReady(){}});
  function press(name,tag='input',root=true,search=false){
    const el={name,blur(){this.blurred=true;},closest(selector){return selector==='main.ob-list-panels'?(root?{}:null):(search?{}:null);},matches(selector){return selector==='input,select'?tag!=='button':tag==='submit';}};
    const event={target:el,key:'Enter',preventDefault(){this.defaultPrevented=true;},stopPropagation(){}};
    for(const fn of handlers.keydown||[])fn(event);
    return {event,el};
  }
  let c=press('flt_Branch');assert.equal(c.event.defaultPrevented,true);assert.equal(c.el.blurred,true);
  assert.equal(press('q').event.defaultPrevented,undefined);
  assert.equal(press('other','input',true,true).event.defaultPrevented,undefined);
  assert.equal(press('filter','input',false).event.defaultPrevented,undefined);
  assert.equal(press('apply','submit').event.defaultPrevented,undefined);
  assert.equal(press('apply','button').event.defaultPrevented,undefined);
});

test('home list fills the viewport even when a standalone main is naturally short',()=>{
 const a=src.indexOf('function obFillDashboardLists()');const b=src.indexOf("document.addEventListener('dblclick'",a);const fn=src.slice(a,b);
 function height(container){
  const card={style:{},getClientRects(){return [1];},closest(){return container;},getBoundingClientRect(){return {top:200};},classList:{add(){}}};
  const row={querySelectorAll(){return [card];}},dash={querySelectorAll(){return [row];}};
  vm.runInNewContext(fn+';obFillDashboardLists();',{document:{querySelectorAll(){return [dash];}},window:{},innerHeight:750});
  return card.style.height;
 }
 assert.equal(height({id:'',scrollTop:0,getBoundingClientRect(){return {bottom:300};}}),'542px');
 assert.equal(height({id:'ob-tabhome',scrollTop:0,getBoundingClientRect(){return {bottom:700};}}),'492px');
});
