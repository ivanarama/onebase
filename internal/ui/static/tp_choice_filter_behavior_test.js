const assert=require('node:assert/strict');
const fs=require('node:fs');
const test=require('node:test');
const vm=require('node:vm');
const ui=fs.readFileSync('static/ui.js','utf8');
const managed=fs.readFileSync('static/managed.js','utf8');
function deferred(){let resolve;const promise=new Promise(r=>resolve=r);return {promise,resolve};}
function response(allowed){return {ok:true,json:async()=>({items:[],selected_allowed:allowed})};}
function runtime(rows,fetchImpl){
 const globalControl={value:'global-a'};
 const form={elements:{namedItem(name){return name==='Направление'?globalControl:null;}}};
 function node(){const attrs={};const el={style:{},children:[],listeners:{},parentElement:null,options:[],value:'',getAttribute(k){return attrs[k]??null;},setAttribute(k,v){attrs[k]=String(v);},removeAttribute(k){delete attrs[k];},closest(){return form;},attrs,appendChild(child){this.children.push(child);this.options.push(child);child.parentElement=this;return child;},remove(){if(this.parentElement)this.parentElement.children=this.parentElement.children.filter(c=>c!==this);},addEventListener(type,fn){this.listeners[type]=fn;},removeEventListener(){},focus(){},select(){},querySelectorAll(){return [];},getBoundingClientRect(){return {left:0,bottom:0,width:120};}};return el;}
 const document={body:node(),removeEventListener(){},createElement:node,getElementsByName(){return [];},querySelectorAll(){return [];},addEventListener(){},getElementById(){return null;}};
 const window={fetch:fetchImpl,_obGridViews:[],obSetManagedFormDirty(){window.dirty=true;}};
 const sandbox={window,document,setTimeout,clearTimeout,fetch:fetchImpl,Promise,WeakMap,JSON,Array,Object,String,Error,encodeURIComponent,obReady(){}};
 vm.createContext(sandbox);
 vm.runInContext(ui.slice(ui.indexOf('function obRefFilterValues(sel)'),ui.indexOf('function openRefPicker(selOrId)')),sandbox);
 vm.runInContext(managed.slice(managed.indexOf('// BEGIN onebase-tp-choice-refresh'),managed.indexOf('// END onebase-tp-choice-refresh')),sandbox);
 const context=JSON.stringify({form_entity:'Заявка',form:'Объекта',element:'Строки.local',table_part:'Строки',sources:{'Строки.Направление':'Направление'}});
 const div=node();
 const grid={dataView:{getItems(){return rows;}},columns:[{field:'Выбор',refEntity:'Цель',choiceContext:context}],grid:{invalidate(){},render(){}},div,tpName:'Строки'};
 window._obGridViews.push(grid);
 return {window,grid,rows,globalControl,form,context,sandbox};
}
test('row-local picker snapshots take each edited row, plus its form row id',()=>{
 const env=runtime([{Направление:'a',Выбор:'x'},{Направление:'b',Выбор:'y'}],()=>{});
 const carrier=env.window.obTPChoiceCarrier(env.context,env.rows[1],1,env.form,'Выбор','Цель','');
 const snapshot=env.window.obRefChoiceSnapshot(carrier),query=new URLSearchParams(snapshot.query);
 assert.equal(query.get('element'),'Строки.local');assert.equal(query.get('row_id'),'1');
 assert.deepEqual(JSON.parse(query.get('sources')),{'Строки.Направление':'b'});assert.equal(query.get('selected_id'),'y');
 const ctx=JSON.parse(env.context);ctx.sources={'Объект.Направление':'Направление'};
 const global=env.window.obTPChoiceCarrier(JSON.stringify(ctx),env.rows[1],1,env.form,'Выбор','Цель','');
 assert.deepEqual(JSON.parse(new URLSearchParams(env.window.obRefChoiceSnapshot(global).query).get('sources')),{'Объект.Направление':'global-a'});
});
test('refresh clears only the rejected row and keeps allowed off-page values',async()=>{
 const calls=[];const env=runtime([{Направление:'a',Выбор:'a-pick'},{Направление:'b',Выбор:'b-pick'}],url=>{calls.push(url);return Promise.resolve(response(url.includes('row_id=1')));});
 await env.window.obRefreshTPChoiceFilters();
 assert.equal(env.rows[0].Выбор,'');assert.equal(env.rows[1].Выбор,'b-pick');assert.equal(env.window.dirty,true);
 assert(calls.some(url=>url.includes('row_id=0'))&&calls.some(url=>url.includes('row_id=1')));
});
test('ABA source changes reject stale replies and removed rows cannot be mutated',async()=>{
 const pending=[],env=runtime([{Направление:'a',Выбор:'keep'}],()=>{const p=deferred();pending.push(p);return p.promise;});
 const a=env.window.obRefreshTPChoiceFilters();env.rows[0].Направление='b';const b=env.window.obRefreshTPChoiceFilters();env.rows[0].Направление='a';const again=env.window.obRefreshTPChoiceFilters();
 pending[2].resolve(response(true));await again;pending[0].resolve(response(false));pending[1].resolve(response(false));await Promise.all([a,b]);assert.equal(env.rows[0].Выбор,'keep');
 env.rows[0].Направление='c';const removed=env.rows[0],job=env.window.obRefreshTPChoiceFilters();env.rows.length=0;pending[3].resolve(response(false));await job;assert.equal(removed.Выбор,'keep');
});
test('failed requests preserve references and show an error without a fallback',async()=>{
 const env=runtime([{Направление:'a',Выбор:'keep'}],async()=>({ok:false,status:500}));await env.window.obRefreshTPChoiceFilters();
 assert.equal(env.rows[0].Выбор,'keep');assert.equal(env.grid.div.attrs['data-ob-choice-error'],'1');
});

test('the grid reference editor sends row context and never falls back to another row cache',async()=>{
 const calls=[],picked=[];
 const env=runtime([{Направление:'a',Выбор:''},{Направление:'b',Выбор:'b-old'}],url=>{calls.push(url);return Promise.resolve({ok:true,json:async()=>({items:[]})});});
 env.window.openRefPicker=select=>picked.push(select);
 const editorStart=managed.indexOf('  function refId('),editorEnd=managed.indexOf('  function ObNumberEditor(',editorStart);
 vm.runInContext(managed.slice(editorStart,editorEnd),env.sandbox);
 const container=env.sandbox.document.createElement('div');
 const column={field:'Выбор',refEntity:'Цель',choiceContext:env.context};
 const args={row:1,item:env.rows[1],column,container,commitChanges(){},cancelChanges(){},grid:{getOptions(){return {};}}};
 const editor=new env.sandbox.ObRefEditor('Выбор',[{id:'foreign',_label:'Чужая строка'}],args);
 editor.loadValue(args.item);await new Promise(setImmediate);
 const query=new URLSearchParams(calls[0].split('?')[1]);
 assert.equal(query.get('row_id'),'1');assert.deepEqual(JSON.parse(query.get('sources')),{'Строки.Направление':'b'});
 const wrapper=container.children[0],input=wrapper.children[0];
 input.listeners.keydown({key:'ArrowDown',preventDefault(){},stopPropagation(){}});
 const list=env.sandbox.document.body.children[0];assert.equal(list.children.length,1);assert.match(list.children[0].textContent,/Ничего не найдено/);
 wrapper.children[1].listeners.click({preventDefault(){},stopPropagation(){}});assert.equal(picked.length,1);
 assert.equal(picked[0].getAttribute('data-ref-choice-context'),env.context);
 assert.equal(picked[0].getAttribute('data-ref-row-id'),'1');assert.strictEqual(picked[0]._obChoiceRow,args.item);
 editor.destroy();
});
