package launcher

import (
	"os/exec"
	"strconv"
	"strings"
	"testing"
)

// Execute the panel code from the rendered editor: a server-only round trip
// cannot catch an operator silently rewritten by the browser before POST.
func TestFormsEditor_ChoiceFilterEqualOrEmptyPanel(t *testing.T) {
	if _, err := exec.LookPath("node"); err != nil {
		t.Skip("node is required for the choice filter panel test")
	}
	page := formsEditorScript(t)
	start := strings.Index(page, "function addChoiceFilterEditor(panel, info) {")
	if start < 0 {
		t.Fatal("choice filter panel was not found in rendered editor")
	}
	end := strings.Index(page[start:], "function addElementActions(panel, info) {")
	if end < 0 {
		t.Fatal("choice filter panel boundary was not found in rendered editor")
	}
	const testScript = `
function equal(got, want, message) {
  if (got !== want) throw new Error(message + ': got ' + JSON.stringify(got) + ', want ' + JSON.stringify(want));
}
class Element {
  constructor(tag) { this.tag = tag; this.children = []; this.handlers = {}; this.value = ''; }
  appendChild(child) { this.children.push(child); return child; }
  addEventListener(name, handler) { this.handlers[name] = handler; }
  set innerHTML(value) { this.children = []; }
}
const edits = [];
const document = { createElement: tag => new Element(tag) };
function Option(label, value) { return { label, value }; }
function mkBtn() { return new Element('button'); }
const _selected = 'elements.0';
function editOp(request) { edits.push(request); }
const panel = new Element('panel');
addChoiceFilterEditor(panel, { choiceFilter: [
  { field: 'Филиал', op: 'eq_or_empty', from: 'Объект.Филиал' }
] });
function all(node) { return [node, ...(node.children || []).flatMap(all)]; }
const nodes = all(panel);
const op = nodes.find(node => node.tag === 'select' &&
  node.children.some(option => option.value === 'eq_or_empty'));
if (!op) throw new Error('the editor must offer eq_or_empty');
equal(op.value, 'eq_or_empty', 'opening the panel must preserve the operator');
const mode = nodes.find(node => node.tag === 'select' &&
  node.children.some(option => option.value === 'value'));
// The boolean literal exists only for eq; from and ref stay selectable (#1820).
equal(mode.children.find(option => option.value === 'value').disabled, true, 'eq_or_empty has no boolean literal');
equal(!!mode.disabled, false, 'eq_or_empty still chooses between from and ref');
const field = nodes.find(node => node.tag === 'input' && node.value === 'Филиал');
field.value = 'ДругойФилиал';
field.handlers.change();
equal(edits.length, 1, 'edit count');
equal(edits[0].choice_filter, JSON.stringify([
  { field: 'ДругойФилиал', op: 'eq_or_empty', from: 'Объект.Филиал' }
]), 'submitted conditions');
const secondPanel = new Element('panel');
addChoiceFilterEditor(secondPanel, { choiceFilter: [
  { field: 'is_folder', op: 'eq', value: true }
] });
const secondOp = all(secondPanel).find(node => node.tag === 'select' &&
  node.children.some(option => option.value === 'eq_or_empty'));
secondOp.value = 'eq_or_empty';
secondOp.handlers.change();
equal(edits[1].choice_filter, JSON.stringify([
  { field: 'is_folder', op: 'eq_or_empty', from: 'Объект.' }
]), 'switching operator must not retain a boolean literal');
const secondMode = all(secondPanel).find(node => node.tag === 'select' &&
  node.children.some(option => option.value === 'value'));
equal(secondMode.children.find(option => option.value === 'value').disabled, true, 'switching to eq_or_empty disables boolean mode');
// ref (#1820): an existing fixed reference opens in ref mode and survives edits.
const folder = 'd4ba641c-70ae-4632-bf99-035f4caaa0af';
const thirdPanel = new Element('panel');
addChoiceFilterEditor(thirdPanel, { choiceFilter: [
  { field: 'parent_id', op: 'in_hierarchy', ref: folder }
] });
const thirdMode = all(thirdPanel).find(node => node.tag === 'select' &&
  node.children.some(option => option.value === 'ref'));
equal(thirdMode.value, 'ref', 'a fixed reference opens in ref mode');
const refInput = all(thirdPanel).find(node => node.tag === 'input' && node.value === folder);
if (!refInput) throw new Error('ref input was not rendered');
const thirdField = all(thirdPanel).find(node => node.tag === 'input' && node.value === 'parent_id');
thirdField.handlers.change();
equal(edits[edits.length - 1].choice_filter, JSON.stringify([
  { field: 'parent_id', op: 'in_hierarchy', ref: folder }
]), 'ref is submitted as the only source');
// Switching to ref with no UUID yet must not write a condition without source.
const fourthPanel = new Element('panel');
addChoiceFilterEditor(fourthPanel, { choiceFilter: [
  { field: 'parent_id', op: 'eq', from: 'Объект.Папка' }
] });
const fourthMode = all(fourthPanel).find(node => node.tag === 'select' &&
  node.children.some(option => option.value === 'ref'));
const before = edits.length;
fourthMode.value = 'ref';
fourthMode.handlers.change();
equal(edits.length, before, 'empty ref is not written');
const emptyRef = all(fourthPanel).find(node => node.tag === 'input' && node.value === '' && node.placeholder);
emptyRef.value = ' ' + folder + ' ';
emptyRef.handlers.change();
equal(edits[edits.length - 1].choice_filter, JSON.stringify([
  { field: 'parent_id', op: 'eq', ref: folder }
]), 'entered UUID is trimmed and written as ref');
// not_in_hierarchy (#1821): offered, opens as is (not rewritten to eq) and is
// submitted unchanged; no boolean literal, like the other reference operators.
const fifthPanel = new Element('panel');
addChoiceFilterEditor(fifthPanel, { choiceFilter: [
  { field: 'parent_id', op: 'not_in_hierarchy', ref: folder }
] });
const fifthOp = all(fifthPanel).find(node => node.tag === 'select' &&
  node.children.some(option => option.value === 'not_in_hierarchy'));
if (!fifthOp) throw new Error('the editor must offer not_in_hierarchy');
equal(fifthOp.value, 'not_in_hierarchy', 'opening the panel must preserve not_in_hierarchy');
const fifthMode = all(fifthPanel).find(node => node.tag === 'select' &&
  node.children.some(option => option.value === 'value'));
equal(fifthMode.children.find(option => option.value === 'value').disabled, true, 'not_in_hierarchy has no boolean literal');
all(fifthPanel).find(node => node.tag === 'input' && node.value === 'parent_id').handlers.change();
equal(edits[edits.length - 1].choice_filter, JSON.stringify([
  { field: 'parent_id', op: 'not_in_hierarchy', ref: folder }
]), 'not_in_hierarchy is submitted unchanged');
`
	// eval the function from the page in the same global scope as its DOM stubs.
	script := testScript[:strings.Index(testScript, "const panel =")] + "\neval(" + strconv.Quote(page[start:start+end]) + ");\n" + testScript[strings.Index(testScript, "const panel ="):]
	// The script goes through stdin: the command line stays constant (gosec
	// G204), and node exits non-zero when the script throws. jsc has no such
	// mode — it reads stdin as a REPL and exits 0 after an exception.
	cmd := exec.CommandContext(t.Context(), "node", "-")
	cmd.Stdin = strings.NewReader(script)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("rendered choice filter panel: %v\n%s", err, out)
	}
}
