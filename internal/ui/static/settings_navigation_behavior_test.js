const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const test = require('node:test');
const vm = require('node:vm');
const source = fs.readFileSync(path.join(__dirname, 'settings-navigation.js'), 'utf8');

class Element {
  constructor(tag) {this.tagName = tag; this.children = []; this.dataset = {}; this.events = {}; this.value = ''; this.textContent = ''; this.disabled = false;}
  appendChild(child) {this.children.push(child); child.parent = this; return child;}
  replaceChildren() {this.children = [];}
  addEventListener(name, fn) {(this.events[name] ||= []).push(fn);}
  fire(name, extra = {}) {
    const event = {target:this, defaultPrevented:false, preventDefault(){this.defaultPrevented = true;}, ...extra};
    for (const fn of this.events[name] || []) fn(event);
    return event;
  }
  click() {if(!this.disabled)this.fire('click');}
  focus() {this.focused = true;}
  querySelector(selector) {
    const match = selector.match(/^\[data-node-id="(.+)"\]$/);
    return this.all().find(x => match && x.dataset.nodeId === match[1]) || null;
  }
  all() {return this.children.flatMap(child => [child, ...child.all()]);}
}

function setup(readyState = 'complete') {
  const item = (id) => ({id:'cfg:'+id, target:'catalog:'+id, object:{title:'Metadata '+id}});
  const base = {version:1, context:'subsystem:School', sections:[{id:'cfg:study', title:'Study', titles:{en:'Education'}, items:[item('years')], groups:[{id:'cfg:group', title:'Group', items:[item('a'), item('b')]}]}, {id:'cfg:empty', title:'Empty', items:[],groups:[]}]};
  const ids = ['navigation-editor-data','navigation-tree','navigation-properties','navigation-palette','navigation-preview','navigation-status','navigation-live','navigation-desired','navigation-save','navigation-reset','navigation-filter','navigation-add-section','navigation-add-group'];
  const elements = Object.fromEntries(ids.map(id => [id, new Element('div')]));
  elements['navigation-editor-data'].textContent = JSON.stringify({base, desired:base, revision:'sha256:test', subsystem:'School', icons:['calendar-days','book-open'], preview:[], labels:{up:'Up',down:'Down',in:'In',out:'Out',hide:'Hide',remove:'Remove',restore:'Restore',title:'Title',icon:'Icon',parent:'Parent',changed:'Changed',newSection:'New section',newGroup:'New group',error:'Preview failed',saveError:'Save failed',conflict:'Conflict',reload:'Reload',reloadConfirm:'Discard?',unsaved:'Unsaved',empty:'Empty'}});
  const document = {readyState, events:{}, createElement(tag){return new Element(tag);}, getElementById(id){return elements[id];}, addEventListener(name, fn){this.events[name] = fn;}};
  const requests = [], timers = new Map(), windowEvents = {};
  let timerID = 0;
  const sandbox = {
    document, window:{addEventListener(name, fn){windowEvents[name] = fn;},confirm(){return false;},location:{assign(url){windowEvents.location = url;}}}, URLSearchParams,
    setTimeout(fn){timers.set(++timerID, fn); return timerID;}, clearTimeout(id){timers.delete(id);},
    fetch(url, options){return new Promise((resolve, reject)=>requests.push({url, options, resolve, reject}));},
  };
  vm.runInNewContext(source, sandbox, {filename:'settings-navigation.js'});
  if (readyState === 'loading') document.events.DOMContentLoaded();
  const state = () => JSON.parse(elements['navigation-desired'].value);
  function row(id) {return elements['navigation-tree'].children.find(x => x.children[0]?.dataset.nodeId === id);}
  function choose(id) {row(id).children[0].click();}
  function control(id, action) {return row(id).children.find(x => x.dataset.action === action);}
  function flush() {const current = [...timers.values()];timers.clear(); current.forEach(fn=>fn());}
  return {elements,document,sandbox,requests,windowEvents,state,row,choose,control,flush};
}

for (const ready of ['loading', 'complete']) {
  test('real bootstrap keyboard moves retain focus ('+ready+')', () => {
    const ui = setup(ready);
    ui.choose('cfg:b');
    assert.equal(ui.row('cfg:b').fire('keydown', {altKey:true,key:'ArrowUp'}).defaultPrevented, true);
    assert.deepEqual(ui.state().sections[0].groups[0].items.map(x=>x.id), ['cfg:b','cfg:a']);
    assert.equal(ui.row('cfg:b').children[0].focused, true);
    ui.row('cfg:b').fire('keydown',{altKey:true,key:'ArrowLeft'});
    assert.deepEqual(ui.state().sections[0].items.map(x=>x.id), ['cfg:years','cfg:b']);
    ui.control('cfg:b', 'in').click();
    assert.deepEqual(ui.state().sections[0].groups[0].items.map(x=>x.id), ['cfg:a','cfg:b']);
    assert.equal(ui.elements['navigation-live'].textContent, 'Changed');
  });
}

test('pointer reorder and cross-parent drop use actual controller', () => {
  const ui = setup();
  ui.row('cfg:b').fire('dragstart',{dataTransfer:{setData(){}}});
  ui.row('cfg:a').fire('drop');
  assert.deepEqual(ui.state().sections[0].groups[0].items.map(x=>x.id), ['cfg:b','cfg:a']);
  ui.row('cfg:b').fire('dragstart'); ui.row('cfg:empty').fire('drop');
  assert.deepEqual(ui.state().sections[1].items.map(x=>x.id), ['cfg:b']);
  const before = JSON.stringify(ui.state());
  ui.row('cfg:study').fire('dragstart');ui.row('cfg:a').fire('drop');
  assert.equal(JSON.stringify(ui.state()), before, 'invalid depth cannot move a section into an item');
});

test('new containers are temporary; title edit clears inherited translations without losing input focus', () => {
  const ui = setup();
  ui.choose('cfg:study');
  const title = ui.elements['navigation-properties'].children[0].children[0];
  title.value = 'Study'; title.focus(); title.fire('input');
  assert.equal(ui.state().sections[0].titles, undefined);
  assert.equal(ui.state().sections[0].title_explicit, true);
  assert.equal(title.focused, true);
  assert.equal(ui.elements['navigation-properties'].children[0].children[0], title);
  ui.elements['navigation-add-group'].click();
  assert.equal(ui.state().sections[0].groups[1].id, 'new:1');
  ui.elements['navigation-add-section'].click();
  assert.equal(ui.state().sections[2].id, 'new:2');
});

test('hide and restore an item does not add invalid fields or reveal siblings of hidden parents', () => {
  const ui = setup();
  ui.row('cfg:study').children.at(-1).click();
  assert.equal(ui.state().sections.some(x=>x.id==='cfg:study'), false);
  const restore = ui.elements['navigation-palette'].all().find(x=>x.dataset.restoreId==='cfg:a');
  restore.click();
  const section = ui.state().sections.find(x=>x.id==='cfg:study');
  assert.deepEqual(section.items, []);
  assert.deepEqual(section.groups[0].items.map(x=>x.id), ['cfg:a']);
  assert.equal(section.groups[0].items[0].items, undefined, 'typed item JSON must not get a container field');
  assert.equal(ui.elements['navigation-palette'].all().some(x=>x.dataset.restoreId==='cfg:b'), true);
});

test('server preview rejects stale responses; malicious names remain text', async () => {
  const ui = setup();
  ui.choose('cfg:study');
  let title = ui.elements['navigation-properties'].children[0].children[0];
  title.value = 'First'; title.fire('input'); ui.flush();
  title.value = 'Second'; title.fire('input'); ui.flush();
  assert.equal(ui.requests.length, 2);
  assert.equal(ui.requests[0].url, '/ui/admin/navigation/preview');
  assert.equal(ui.requests[0].options.body.get('subsystem'), 'School');
  assert.equal(ui.requests[0].options.body.get('revision'), 'sha256:test');
  ui.requests[1].resolve({ok:true,json:async()=>({preview:[{title:'<img src=x onerror=alert(1)>',items:[],groups:[]}]})});
  await new Promise(setImmediate);
  assert.equal(ui.elements['navigation-preview'].children[0].textContent, '<img src=x onerror=alert(1)>');
  assert.equal(ui.elements['navigation-preview'].children[0].children.length, 0);
  ui.requests[0].resolve({ok:true,json:async()=>({preview:[{title:'Old preview'}]})});
  await new Promise(setImmediate);
  assert.equal(ui.elements['navigation-preview'].children[0].textContent, '<img src=x onerror=alert(1)>');
  title.value = 'Third';title.fire('input'); ui.flush(); ui.requests[2].reject(new Error('private backend error'));
  await new Promise(setImmediate);
  assert.equal(ui.elements['navigation-status'].textContent, 'Preview failed');
});

test('save serializes current desired tree and freezes edits; canceled reset keeps unsaved guard', () => {
  const ui = setup();
  ui.elements['navigation-add-section'].click();
  const canceled = ui.elements['navigation-reset'].fire('submit',{defaultPrevented:true});
  assert.equal(canceled.defaultPrevented, true);
  const unload = {preventDefault(){this.prevented = true;}};
  ui.windowEvents.beforeunload(unload); assert.equal(unload.prevented, true);
  ui.elements['navigation-save'].fire('submit');
  const before = JSON.stringify(ui.state());
  ui.elements['navigation-add-section'].click();
  assert.equal(JSON.stringify(ui.state()), before);
  const savedUnload = {preventDefault(){this.prevented = true;}};
  ui.windowEvents.beforeunload(savedUnload); assert.equal(savedUnload.prevented, undefined);
});

test('409 keeps draft and old revision, shows winner preview and requires explicit reload', async () => {
  const ui = setup();
  ui.choose('cfg:study');
  const title = ui.elements['navigation-properties'].children[0].children[0];
  title.value = 'Unsaved rename';title.fire('input');
  const draft = JSON.stringify(ui.state());
  ui.elements['navigation-save'].fire('submit');
  assert.equal(ui.requests[0].options.body.get('revision'), 'sha256:test');
  ui.requests[0].resolve({status:409,json:async()=>({revision:'sha256:winner', preview:[{title:'Winner',items:[],groups:[]}]})});
  await new Promise(setImmediate);
  assert.equal(JSON.stringify(ui.state()), draft);
  assert.equal(ui.elements['navigation-status'].textContent, 'Conflict');
  assert.equal(ui.elements['navigation-preview'].children[0].textContent, 'Winner');
  ui.elements['navigation-status'].children[0].click();
  assert.equal(ui.windowEvents.location, undefined);
  ui.elements['navigation-save'].fire('submit');
  assert.equal(ui.requests[1].options.body.get('revision'), 'sha256:test', 'conflict must not silently authorize overwrite');
  ui.requests[1].resolve({status:409,json:async()=>({preview:[]})});
  await new Promise(setImmediate);
  ui.sandbox.window.confirm = () => true;
  ui.elements['navigation-status'].children[0].click();
  assert.equal(ui.windowEvents.location, '/ui/admin/navigation?subsystem=School');
});

test('successful save follows GET, failed save unlocks draft without exposing backend text', async () => {
  const ui = setup();
  ui.elements['navigation-save'].fire('submit');
  ui.requests[0].resolve({status:500,ok:false});
  await new Promise(setImmediate);
  assert.equal(ui.elements['navigation-status'].textContent, 'Save failed');
  ui.elements['navigation-add-section'].click();
  assert.equal(ui.state().sections.at(-1).id, 'new:1');
  ui.elements['navigation-save'].fire('submit');
  ui.requests[1].resolve({status:200,ok:true,url:'http://example.com/ui/admin/navigation?saved=1'});
  await new Promise(setImmediate);
  assert.equal(ui.windowEvents.location, 'http://example.com/ui/admin/navigation?saved=1');
});
