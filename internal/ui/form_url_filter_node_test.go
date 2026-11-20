package ui

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// ui.открытьФорму с отбором и параметрами: список открывается с отбором
// (f.<Поле>=…), отчёт — с параметрами и сразу сформированным (__run=1). Раньше
// formURL строил только путь, и кнопка формы не могла открыть «Жалобы на
// мастере» или «Штрафы на мастере» с отбором, как в 1С.
func TestFormURLCarriesFilterAndReportParams(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node is not installed")
	}
	src := string(uiJS)
	start := strings.Index(src, "  function formURL(link) {")
	if start < 0 {
		t.Fatal("в ui.js нет formURL")
	}
	end := strings.Index(src[start:], "\n  }\n")
	if end < 0 {
		t.Fatal("не найден конец formURL")
	}
	fn := src[start : start+end+4]
	test := fn + `
const assert = require('node:assert');
assert.strictEqual(formURL({'вид': 'document', 'сущность': 'А_Жалоба'}), '/ui/document/' + encodeURIComponent('а_жалоба'));
assert.strictEqual(formURL({'вид': 'document', 'сущность': 'А_Жалоба', 'отбор': {'Мастер': 'm-1'}}),
  '/ui/document/' + encodeURIComponent('а_жалоба') + '?' + encodeURIComponent('f.Мастер') + '=m-1');
assert.strictEqual(formURL({'вид': 'document', 'сущность': 'А_Жалоба', 'id': 'x', 'отбор': {'Мастер': 'm-1'}}),
  '/ui/document/' + encodeURIComponent('а_жалоба') + '/x', 'у формы объекта отбора нет');
assert.strictEqual(formURL({'вид': 'report', 'сущность': 'Штрафы', 'параметры': {'Мастер': 'm-1', 'Пусто': null}, 'сформировать': true}),
  '/ui/report/' + encodeURIComponent('Штрафы') + '?' + encodeURIComponent('Мастер') + '=m-1&__run=1');
assert.strictEqual(formURL({'вид': 'report', 'сущность': 'Штрафы'}), '/ui/report/' + encodeURIComponent('Штрафы'));
assert.strictEqual(formURL({'вид': 'processor', 'сущность': 'Обр', 'параметры': {'А': 1}}), '/ui/processor/' + encodeURIComponent('Обр'));
console.log('ok');
`
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "t.js"), []byte(test), 0o600); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(node, "t.js") //nolint:gosec // test-only executable resolved by exec.LookPath
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("node: %v\n%s", err, out)
	}
}
