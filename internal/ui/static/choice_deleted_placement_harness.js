// The input comes from a real GET form, not from a hand-written metadata fixture.
const fs = require('node:fs');
const vm = require('node:vm');
const input = JSON.parse(fs.readFileSync(0, 'utf8'));
const ui = fs.readFileSync('static/ui.js', 'utf8');
const managed = fs.readFileSync('static/managed.js', 'utf8');
function slice(source, start, end) {
  const a = source.indexOf(start), b = source.indexOf(end, a);
  if (a < 0 || b < 0) throw new Error('missing production function');
  return source.slice(a, b);
}
function element(tag) {
  return {tagName: tag.toUpperCase(), children: [], attrs: {}, style: {}, value: '',
    getAttribute(k) {return this.attrs[k] ?? null;}, setAttribute(k,v) {this.attrs[k]=String(v);},
    appendChild(c) {this.children.push(c);c.parentElement=this;return c;},
    closest() {return null;}, querySelectorAll() {return [];},
    set innerHTML(v) {this.children=[];}, get rows() {return this.children;}};
}
const tbody=element('tbody');tbody.attrs=input.attrs;
const window={_tpRefOpts:input.options,_tpRefMeta:input.meta,_tpEnumLabels:{},_tpEnumOrder:{},obManagedSetTablePartJSON(){}};
const document={createElement:element,getElementById(){return tbody;},activeElement:null};
const env={window,document,JSON,Array,Object,String,
 obManagedTableBodies(){return [tbody];}, obManagedVirtualColumnNames(){return [];},
 obManagedHiddenColumnNames(){return [];},obManagedTableReadOnly(){return false;},
 obFormRowClass(){return '';},obFormCellClass(){return '';}};
vm.createContext(env);
vm.runInContext(slice(ui,'function obTPRefOpts()', 'function recalcTpRow('),env);
vm.runInContext(slice(ui,'function obRefMarkedParam(', 'window.obRefMarkedParam ='),env);
const results=[];
function capture(selected = false) {
 const controls=[];
 function walk(n){if(n.tagName==='SELECT')controls.push(n);n.children.forEach(walk);}walk(tbody);
 if(controls.length!==1)throw new Error('expected one real select');
 const sel=controls[0];results.push({marked:sel.getAttribute('data-ref-marked')||'',query:env.obRefMarkedParam(sel),context:sel.getAttribute('data-ref-choice-context')||'', ids:sel.children.map(o => o.value), selected});
}
env.addTpRow('Строки',['Мастер'],[],1,tbody);capture();
if(input.managed){
 vm.runInContext(slice(managed,'  function applyTableParts(tps){','  // Экспортируем в window'),env);
 env.applyTableParts({Строки:[{Мастер:''}]});capture();
 // A real event may select a previously unselected deleted reference.
 window._tpRefOpts.Строки.Мастер = [{id:input.selected, _label:'Returned selection'}];
 env.applyTableParts({Строки:[{Мастер:input.selected}]});capture(true);
}
process.stdout.write(JSON.stringify(results));
