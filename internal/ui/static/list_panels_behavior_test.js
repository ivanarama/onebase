const {test}=require('node:test');
const assert=require('node:assert/strict');
const vm=require('node:vm');
const fs=require('node:fs');
const src=fs.readFileSync(__dirname+'/ui.js','utf8');
const fn=src.slice(src.indexOf('function obInitListPanels()'),src.indexOf('function obInitReportBlocks()'));
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
