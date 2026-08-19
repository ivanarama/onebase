package entityservice_test

// Страж отказов записи (#962, находка Н3).
//
// Save сообщает о пользовательском отказе НЕ ошибкой, а полем результата
// DSLError: err при этом nil. Значит вызывающий, который смотрит только на err,
// молча примет отказ за успех — документ не записан, а пользователю сказано
// «готово». Компилятор такую ошибку не ловит, тест конкретного хендлера тоже:
// он обычно проверяет удачный путь.
//
// Поэтому правило проверяется сканером, как маскирование полей и строковый
// доступ: каждая функция, зовущая Save, ОБЯЗАНА либо сама посмотреть на
// DSLError, либо передать результат в известный хелпер, который это делает
// (saveResultGates), либо стоять в saveRejectionExempt с обоснованием.
//
// Добавляете новый вызов Save — обработайте отказ. Если отказ обрабатывает
// новый общий хелпер, внесите его в saveResultGates: список должен пополняться
// осознанно, иначе страж со временем начнёт пропускать всё подряд.

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// saveResultGates — хелперы, которые сами разбирают SaveResult и отвечают
// клиенту при отказе. Проверено чтением: каждый смотрит result.DSLError.
var saveResultGates = map[string]bool{
	"writeSaveResultV2": true,
}

// saveRejectionExempt — вызовы Save, которым отказ разбирать незачем, с
// причиной. Пусто не значит «не бывает»: значит, сейчас таких нет.
var saveRejectionExempt = map[string]string{}

func TestSaveRejection_EveryCallerHandlesDSLError(t *testing.T) {
	root := ".." // internal/
	fset := token.NewFileSet()

	type finding struct {
		fn    string
		file  string
		gated bool
	}
	var findings []finding

	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		raw, err := os.ReadFile(path) //nolint:gosec // G304: путь из обхода собственного дерева пакета
		if err != nil {
			return err
		}
		if !strings.Contains(string(raw), "entityservice.SaveRequest") {
			return nil
		}
		af, err := parser.ParseFile(fset, path, raw, 0)
		if err != nil {
			return err
		}
		for _, decl := range af.Decls {
			fd, ok := decl.(*ast.FuncDecl)
			if !ok || fd.Body == nil {
				continue
			}
			callsSave, gated := false, false
			ast.Inspect(fd.Body, func(n ast.Node) bool {
				switch node := n.(type) {
				case *ast.CallExpr:
					// Хелпер-страж зовётся и как пакетная функция
					// (writeSaveResultV2(...)), и как метод — учитываем оба вида.
					switch fun := node.Fun.(type) {
					case *ast.SelectorExpr:
						if fun.Sel.Name == "Save" && hasSaveRequestArg(node) {
							callsSave = true
						}
						if saveResultGates[fun.Sel.Name] {
							gated = true
						}
					case *ast.Ident:
						if saveResultGates[fun.Name] {
							gated = true
						}
					}
				case *ast.SelectorExpr:
					if node.Sel.Name == "DSLError" {
						gated = true
					}
				}
				return true
			})
			if !callsSave {
				continue
			}
			findings = append(findings, finding{fn: fd.Name.Name, file: filepath.ToSlash(path), gated: gated})
		}
		return nil
	})
	if err != nil {
		t.Fatalf("обход дерева: %v", err)
	}

	if len(findings) == 0 {
		t.Fatal("сканер не нашёл ни одного вызова Save — сломан матчинг?")
	}

	var violations []string
	for _, f := range findings {
		if f.gated {
			continue
		}
		if _, ok := saveRejectionExempt[f.fn]; ok {
			continue
		}
		violations = append(violations, f.fn+" ("+f.file+")")
	}
	sort.Strings(violations)
	if len(violations) > 0 {
		t.Fatalf("вызовы Save не разбирают отказ (%d):\n  %s\n\n"+
			"Save возвращает пользовательский отказ полем DSLError, а не ошибкой: err при этом nil, "+
			"и вызывающий сообщит пользователю об успехе там, где записи не было.\n"+
			"Посмотрите на result.DSLError, передайте результат в хелпер из saveResultGates "+
			"или внесите функцию в saveRejectionExempt с обоснованием.",
			len(violations), strings.Join(violations, "\n  "))
	}
}

// hasSaveRequestArg — среди аргументов вызова есть литерал entityservice.SaveRequest
// (или SaveRequest внутри самого пакета).
func hasSaveRequestArg(call *ast.CallExpr) bool {
	for _, arg := range call.Args {
		lit, ok := arg.(*ast.CompositeLit)
		if !ok {
			continue
		}
		switch t := lit.Type.(type) {
		case *ast.SelectorExpr:
			if t.Sel.Name == "SaveRequest" {
				return true
			}
		case *ast.Ident:
			if t.Name == "SaveRequest" {
				return true
			}
		}
	}
	return false
}
