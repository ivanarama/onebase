package ui

import (
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// План 88 (defense-in-depth): маскирование ПДн держится на дисциплине — новый
// list/get-хендлер, отдающий строки клиенту и забывший maskRecord/maskRecords,
// тихо сольёт чувствительные поля. Сканер повторяет идею
// row_access_chokepoint_test: каждый HTTP-хендлер, читающий store.List/GetByID
// напрямую, ОБЯЗАН либо позвать maskRecord/maskRecords, либо стоять в
// maskChokepointExempt с обоснованием — когда он читает строку НЕ для отдачи её
// полей клиенту (проведение, удаление, обновление, проверка доступа, отдача
// бинарника, серверные события). Хелперы (иные сигнатуры) здесь не
// проверяются — за них отвечает страж уровня хелпера в
// field_mask_helper_chokepoint_test.go: правило применяется к самой функции,
// читающей store. Прежняя формулировка «хелперы закрывают вызывающие хендлеры»
// оказалась неверной — через это слепое пятно прошёл #609 (issue #649).
//
// Добавляя новый хендлер с прямым store.List/GetByID, который РЕНДЕРИТ строки:
// вызовите maskRecord/maskRecords (field_access.go). Если строка читается не для
// показа полей — внесите хендлер в maskChokepointExempt с причиной.

var maskGateFuncs = map[string]bool{
	"maskRecord": true, "maskRecords": true,
}

var maskChokepointExempt = map[string]string{
	// Раскрытие: намеренно возвращает полное значение под object-level правом
	// `disclose` и аудитом (план 88, CC-SEC-004) — маскировать было бы абсурдом.
	"discloseField": "раскрытие поля под правом disclose + аудитом",
	// Проведение: читает реальные значения документа для расчёта движений
	// (серверная бизнес-логика), поля клиенту не отдаёт — редиректит.
	"postDocument": "проведение использует реальные значения, не рендерит поля клиенту",
	// Удаление: pre-образ читается только для RLS-адресации служебного события
	// живого списка (план 87) — кому строка была видна, тот уберёт её из списка.
	// Поля клиенту не отдаются (сигнал несёт лишь {действие:"удалён"}), редиректит.
	"deleteRecord": "pre-образ только для RLS-адресации live-list удаления, поля клиенту не отдаёт",
	// Переключение активности проходит через полный entityservice.Save, чтобы
	// исполнить OnWrite. Маскировать снимок нельзя: маска записалась бы поверх
	// настоящего значения; клиент получает только ошибку или редирект.
	"setRecordActivity": "путь записи: полный снимок нужен OnWrite/Save, поля клиенту не отдаёт",
}

func TestFieldMaskChokepoint_NoUngatedStoreReads(t *testing.T) {
	fset := token.NewFileSet()
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	type finding struct {
		fn     string
		file   string
		gated  bool
		exempt bool
	}
	var findings []finding
	for _, f := range files {
		if strings.HasSuffix(f, "_test.go") {
			continue
		}
		af, err := parser.ParseFile(fset, f, nil, 0)
		if err != nil {
			t.Fatalf("parse %s: %v", f, err)
		}
		for _, decl := range af.Decls {
			fd, ok := decl.(*ast.FuncDecl)
			if !ok || fd.Body == nil {
				continue
			}
			if !isHTTPHandler(fd) {
				continue // хелперы вне области — их закрывают вызывающие хендлеры
			}
			readsStore, gated := false, false
			ast.Inspect(fd.Body, func(n ast.Node) bool {
				call, ok := n.(*ast.CallExpr)
				if !ok {
					return true
				}
				sel, ok := call.Fun.(*ast.SelectorExpr)
				if !ok {
					return true
				}
				if maskGateFuncs[sel.Sel.Name] {
					gated = true
				}
				if chokepointStoreReads[sel.Sel.Name] {
					if inner, ok := sel.X.(*ast.SelectorExpr); ok && inner.Sel.Name == "store" {
						readsStore = true
					}
				}
				return true
			})
			if !readsStore {
				continue
			}
			name := fd.Name.Name
			_, isExempt := maskChokepointExempt[name]
			findings = append(findings, finding{fn: name, file: f, gated: gated, exempt: isExempt})
		}
	}
	if len(findings) == 0 {
		t.Fatal("детектор не нашёл ни одного хендлера с store.List/GetByID — сломан матчинг?")
	}
	var violations []string
	for _, fnd := range findings {
		if !fnd.gated && !fnd.exempt {
			violations = append(violations, fnd.fn+" ("+fnd.file+")")
		}
	}
	sort.Strings(violations)
	if len(violations) > 0 {
		t.Fatalf("HTTP-хендлеры читают store.List/GetByID, но не маскируют поля и не в maskChokepointExempt (%d):\n  %s\n\n"+
			"Примените maskRecord/maskRecords (field_access.go) или внесите в maskChokepointExempt с обоснованием.",
			len(violations), strings.Join(violations, "\n  "))
	}
}
