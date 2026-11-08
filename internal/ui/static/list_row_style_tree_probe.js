const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const vm = require('node:vm');

const input = JSON.parse(fs.readFileSync(0, 'utf8'));
const source = fs.readFileSync(path.join(__dirname, 'ui.js'), 'utf8');
const start = source.indexOf('function makeTreeRow');
const end = source.indexOf('\nfunction listBasedOnItems', start);
assert.ok(start >= 0 && end > start, 'production tree renderer must exist');
global.document = {
  createElement() {
    const classes = new Set();
    return {
      style: {}, dataset: {}, children: [],
      classList: {add(value) { classes.add(value); }, contains(value) { return classes.has(value); }},
      appendChild(child) { this.children.push(child); return child; },
      setAttribute() {}
    };
  },
  createTextNode(text) { return {textContent: text}; }
};
global.obListConfig = () => ({canDelete: false});
global.obListLabel = (_key, fallback) => fallback;
global.initTreeToggle = () => {};
vm.runInThisContext(source.slice(start, end), {filename: 'production-makeTreeRow.js'});
assert.equal(input.response.rows.length, Object.keys(input.want).length);
for (const row of input.response.rows) {
  const want = input.want[row.id];
  assert.ok(want, 'unexpected row ' + row.id);
  const tr = makeTreeRow(row);
  assert.equal(tr.style.backgroundColor || '', want[0], 'background of ' + row.id);
  assert.equal(tr.style.color || '', want[1], 'contrast of ' + row.id);
  assert.equal(tr.style.fontWeight || '', want[2], 'weight of ' + row.id);
  assert.equal(tr.classList.contains('ob-row-styled'), want.some(Boolean), 'styled class of ' + row.id);
  assert.equal(tr.style.cursor, 'pointer');
  assert.equal(tr.dataset.treeId, row.id);
}
