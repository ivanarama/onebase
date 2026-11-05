// Replay real HTTP metadata/replies through the production renderers/editor.
const assert = require('node:assert/strict');
const fs = require('node:fs');
const vm = require('node:vm');
const data = JSON.parse(fs.readFileSync(0, 'utf8'));
const ui = fs.readFileSync('static/ui.js', 'utf8');
const managed = fs.readFileSync('static/managed.js', 'utf8');
function slice(source, start, end) {
  const a = source.indexOf(start), b = source.indexOf(end, a);
  assert.ok(a >= 0 && b > a, start);
  return source.slice(a, b);
}
function element(tag) {
  return {tagName:tag.toUpperCase(), children:[], attrs:{}, style:{}, value:'', listeners:{},
    setAttribute(k,v){this.attrs[k]=String(v);}, getAttribute(k){return this.attrs[k]??null;},
    appendChild(c){this.children.push(c);c.parentElement=this;return c;},
    addEventListener(k,fn){this.listeners[k]=fn;}, removeEventListener(){},
    focus(){if(this.listeners.focus)this.listeners.focus();}, select(){},
    getBoundingClientRect(){return {left:0,bottom:0,width:200};},
    remove(){if(this.parentElement)this.parentElement.children=this.parentElement.children.filter(c=>c!==this);},
    closest(){return null;},querySelectorAll(){return [];},
    set innerHTML(v){this.children=[];},get rows(){return this.children;},get options(){return this.children;}};
}
function descendants(n){return n.children.flatMap(c=>[c,...descendants(c)]);}
const bodies=data.attrs.filter(a=>!a['data-sg-tp']).map(attrs=>Object.assign(element('tbody'),{attrs}));
const window={_tpRefOpts:data.labels,_tpRefChoices:data.choices,_tpRefMeta:data.meta,_tpEnumLabels:{},_tpEnumOrder:{},obManagedSetTablePartJSON(){}};
const document={body:element('body'),createElement:element,getElementById(){return bodies[0];},activeElement:null,addEventListener(){},removeEventListener(){}};
const env={window,document,console,JSON,Array,Object,String,Map,WeakMap,Set,encodeURIComponent,
  setTimeout(){return 1;},clearTimeout(){},
  obManagedTableBodies(){return bodies;},obManagedVirtualColumnNames(){return [];},obManagedHiddenColumnNames(){return [];},
  obManagedTableReadOnly(){return false;},obFormRowClass(){return '';},obFormCellClass(){return '';}};
vm.createContext(env);
vm.runInContext(slice(ui,'function obTPRefOpts()', 'function recalcTpRow('),env);
vm.runInContext(slice(managed,'function obManagedSyncRefOptionMap(', 'window.obManagedApplyTablePartRefOptions ='),env);
vm.runInContext(slice(managed,'  function applyTableParts(tps){','  // Экспортируем в window'),env);
vm.runInContext(slice(managed,'  function refId(v)', '  // Редактор ячейки-перечисления:'),env);
vm.runInContext(slice(managed,'  function buildColumns(', '  // Serialize ref value'),env);
const queryResults=[];
function hasDeleted(sel){return sel.children.some(o=>o.value===data.selected);}
function expected(attrs){const m=JSON.parse(attrs['data-tp-ref-marked']||'{}');return m.Мастер==='1'||(m.Мастер!=='0'&&data.show);}
function checkBodies(withRows){
  for(const body of bodies){
    const selects=descendants(body).filter(n=>n.tagName==='SELECT');
    assert.equal(selects.length,withRows?3:1);
    selects.forEach((sel,row)=>{
      assert.equal(hasDeleted(sel),expected(body.attrs)||(withRows&&row===0),'foreign selected exception in '+sel.name);
      if(withRows&&row===0){
        const own=sel.children.find(o=>o.value===data.selected);
        assert.ok(own&&own.selected&&own.textContent.includes('Уволенный'),'own selected UUID/label');
      }
    });
  }
}
for(const body of bodies)env.addTpRow('Строки',['Мастер'],[],3,body);
checkBodies(false);
if(data.event.ok){
  env.obManagedApplyTablePartRefOptions(data.event.tpRefOptions,data.event.tpRefChoices);
  env.applyTableParts(data.event.tableparts);checkBodies(true);
  for(const body of bodies){body.innerHTML='';env.addTpRow('Строки',['Мастер'],[],3,body);}
  checkBodies(false);
}
for(const attrs of data.attrs.filter(a=>a['data-sg-tp'])){
  const choices=JSON.parse(attrs['data-sg-choices']);
  assert.ok(JSON.parse(attrs['data-sg-ref']).Мастер.some(o=>o.id===data.selected),'selected label dictionary');
  const columns=env.buildColumns(JSON.parse(attrs['data-sg-cols']),window._tpRefOpts.Строки,{}, {},choices);
  const column=columns[0];
  assert.ok(column.formatter(0,0,data.selected).includes('Уволенный'),'grid selected label');
  for(const selected of [data.selected,'',data.active]){
    const container=element('div');
    let picker;
    window.openRefPicker=sel=>{picker=sel;};
    const editor=new column.editor({column,container,item:{Мастер:selected},row:selected?0:1,commitChanges(){}});
    const input=container.children[0].children[0];
    if(selected===data.selected)assert.ok(input.value.includes('Уволенный'),'editor selected label');
    const button=container.children[0].children[1];
    button.listeners.click({preventDefault(){},stopPropagation(){}});
    assert.ok(picker,'actual picker button');
    assert.equal(hasDeleted(picker),data.show||selected===data.selected,'grid picker selection isolation');
    const marked=picker.getAttribute('data-ref-marked')||'';
    queryResults.push({marked,show:data.show});
    editor.destroy();
  }
}
process.stdout.write(JSON.stringify(queryResults));
