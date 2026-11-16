const test = require('node:test');
const assert = require('node:assert/strict');
const fs = require('node:fs');
const vm = require('node:vm');

function element() {
  return {attrs: {}, style: {}, children: [], textContent: '',
    setAttribute(k,v) { this.attrs[k]=String(v); }, getAttribute(k) { return this.attrs[k] ?? null; },
    addEventListener(k,f) { this[k]=f; }, click() { this.clickHandler ? this.clickHandler() : (this.onclick || this['click'])(); },
    appendChild(e) { this.children.push(e); }, prepend(e) { this.children.unshift(e); },
    querySelectorAll() { return this.children; },
    getBoundingClientRect() { return {height:20, width:100}; }
  };
}
function button() {
  const b=element();
  b.addEventListener=(event,fn)=>{ if(event==='click')b.onclick=fn; };
  b.click=()=>b.onclick();
  return b;
}

test('composition levels hide descendants and manual folds retain nested state', () => {
  const panel=element(), block={querySelector:()=>panel,querySelectorAll:()=>panel.children};
  const paths=['/A','/A/B','/A/B/C'];
  const groups=paths.map((path,i)=>{
    const tr=element(), fold=button(); fold.setAttribute('aria-expanded','true');
    tr.setAttribute('data-level',i);tr.setAttribute('data-group',path);
    tr.querySelector=()=>({querySelector:()=>fold});tr.fold=fold;return tr;
  });
  const detail=element();detail.setAttribute('data-parent',paths[2]);
  const body={querySelectorAll:sel=>sel==='tr.grp'?groups:[detail],querySelector:()=>detail};
  const table=element();table.tBodies=[body];table.closest=()=>block;
  const document={querySelectorAll:()=>[table],createElement:()=>button()};
  const src=fs.readFileSync('static/ui.js','utf8');
  const code=src.slice(src.indexOf('function rcBuildView('),src.indexOf('function obInitReportBlocks('));
  const context={document};vm.createContext(context);vm.runInContext(code,context);
  context.obInitReportCompositionControls();context.obInitReportCompositionControls();
  assert.deepEqual(panel.children.map(b=>b.textContent),['1','2','3','4']);
  for(let n=1;n<=4;n++){
    panel.children[n-1].click();
    groups.forEach((g,i)=>assert.equal(g.style.display==='none',i>=n));
    assert.equal(detail.style.display==='none',n<4);
    assert.equal(panel.children[n-1].getAttribute('aria-pressed'),'true');
  }
  groups[1].fold.click();assert.equal(groups[2].style.display,'none');
  groups[0].fold.click();groups[0].fold.click();
  assert.equal(groups[1].style.display,'');assert.equal(groups[2].style.display,'none');
  assert.equal(groups[0].fold.getAttribute('aria-expanded'),'true');
  assert.equal(panel.children.every(b=>b.getAttribute('aria-pressed')==='false'),true);
});

test('tabular document uses the same levels without export URLs or changing source groups', () => {
  const groups=[{Start:0,HeaderEnd:0,End:3,Level:1,Expanded:true,Title:'A'},
    {Start:1,HeaderEnd:1,End:3,Level:2,Expanded:true,Title:'B'}];
  const rows=Array.from({length:5},()=>{const row=element();row.cells=[element()];row.cells[0].colSpan=1;return row;});
  const toolbar=element(), document={querySelectorAll:()=>rows,querySelector:()=>toolbar,createElement:()=>button()};
  const source=fs.readFileSync('../sheet/view_html.go','utf8');
  const code=source.slice(source.indexOf('<script>')+8,source.indexOf('</script>'))
    .replace('%s',JSON.stringify(groups)).replace(/%d/g,'0').replace('%t','false');
  const context={document,window:{}};context.window.parent=context.window;vm.createContext(context);vm.runInContext(code,context);
  const panel=toolbar.children[0];assert.deepEqual(panel.children.map(b=>b.textContent),['1','2','3']);
  panel.children[0].click();assert.equal(rows[0].hidden,false);assert.equal(rows[1].hidden,true);assert.equal(rows[4].hidden,false);
  panel.children[1].click();assert.equal(rows[1].hidden,false);assert.equal(rows[2].hidden,true);
  panel.children[2].click();assert.equal(rows[2].hidden,false);
  rows[0].cells[0].children[0].click();assert.equal(rows[1].hidden,true);
  assert.equal(groups.every(g=>g.Expanded),true);
});

test('actual cross renderer leaves retain Cyrillic and one-character labels', () => {
  // The Go wrapper supplies actual renderCrossTable HTML. Direct Node runs
  // retain a minimal fixture for the same no-fold DOM branch.
  const fixtures=process.argv[2] ? JSON.parse(fs.readFileSync(process.argv[2],'utf8')) : [
    '<tr class="grp" data-group="/Север" data-level="0"><td>Север</td></tr>' +
    '<tr class="grp" data-group="/Я" data-level="0"><td>Я</td></tr>' +
    '<tr class="grp" data-group="/▶Север" data-level="0"><td>▶Север</td></tr>'
  ];
  const src=fs.readFileSync('static/ui.js','utf8');
  const code=src.slice(src.indexOf('function rcBuildView('),src.indexOf('function obInitReportBlocks('));
  for(const fixture of fixtures){
    const panel=element(),block={querySelector:()=>panel,querySelectorAll:()=>panel.children};
    const groups=Array.from(fixture.matchAll(/<tr class="grp"([^>]*)>([\s\S]*?)<\/tr>/g),match=>{
      const tr=element(),cell=element(),attrs=match[1];
      for(const name of ['data-group','data-level']) tr.setAttribute(name,attrs.match(new RegExp(name+'="([^"]*)"'))[1]);
      const content=match[2].match(/<td[^>]*>([\s\S]*?)<\/td>/)[1];
      cell.textContent=content.replace(/<[^>]*>/g,'');
      const fold=content.includes('class="rc-fold"') ? button() : null;
      if(fold) fold.setAttribute('aria-expanded',content.match(/aria-expanded="([^"]*)"/)[1]);
      cell.querySelector=()=>fold;tr.querySelector=()=>cell;tr.fold=fold;tr.cell=cell;return tr;
    });
    const leaves=groups.filter(g=>!g.fold);
    const labels=leaves.map(g=>g.cell.textContent);
    assert.equal(leaves.length,3);
    assert.deepEqual(new Set(labels),new Set(['Север','Я','▶Север']));
    const body={querySelectorAll:selector=>selector==='tr.grp'?groups:[],querySelector:()=>null};
    const table=element();table.tBodies=[body];table.closest=()=>block;
    const document={querySelectorAll:()=>[table],createElement:()=>button()};
    const context={document};vm.createContext(context);vm.runInContext(code,context);
    context.obInitReportCompositionControls();
    for(let round=0;round<3;round++){
      for(const level of panel.children) level.click();
      for(const group of groups.filter(g=>g.fold)){group.fold.click();group.fold.click();}
      assert.deepEqual(leaves.map(g=>g.cell.textContent),labels);
    }
  }
});
