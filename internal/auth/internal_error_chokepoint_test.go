package auth

// Немой отказ не должен вернуться (#1053).
//
// Тест на один хендлер закрывает один хендлер. Заявку же завело не единичное
// упущение, а привычка: `http.Error(w, "internal error", 500)` писался
// двенадцать раз подряд, и ни разу рядом не оказалось строки журнала — каждый
// следующий автор просто повторял соседний код. Тринадцатый раз обойдётся
// пользователю в ту же переписку, поэтому проверяется не поведение одного
// места, а отсутствие такой формы записи во всём пакете.

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"strings"
	"testing"
)

// internalErrorHelper — единственный файл, которому положено отвечать 500
// напрямую: он же и пишет причину в журнал.
const internalErrorHelper = "internal_error.go"

func TestNoSilentInternalError(t *testing.T) {
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatal(err)
	}
	fset := token.NewFileSet()
	var offenders []string
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") || name == internalErrorHelper {
			continue
		}
		file, err := parser.ParseFile(fset, name, nil, 0)
		if err != nil {
			t.Fatalf("разбор %s: %v", name, err)
		}
		ast.Inspect(file, func(node ast.Node) bool {
			call, ok := node.(*ast.CallExpr)
			if !ok || !isHTTPError(call) {
				return true
			}
			if len(call.Args) >= 3 && isInternalErrorStatus(call.Args[2]) {
				pos := fset.Position(call.Pos())
				offenders = append(offenders, name+":"+itoa(pos.Line))
			}
			return true
		})
	}
	if len(offenders) > 0 {
		t.Errorf("прямой ответ 500 мимо h.internalError: %s.\n"+
			"Такой отказ не оставляет следа в журнале — ровно то, из-за чего вход в #1053 "+
			"разбирали перепиской, а не по логу.\n"+
			"Отвечайте через h.internalError(w, r, \"что делали\", err): пользователю останется "+
			"та же общая фраза, администратору — причина.", strings.Join(offenders, ", "))
	}
}

// isHTTPError — вызов вида http.Error(...).
func isHTTPError(call *ast.CallExpr) bool {
	sel, ok := call.Fun.(*ast.SelectorExpr)
	if !ok || sel.Sel.Name != "Error" {
		return false
	}
	pkg, ok := sel.X.(*ast.Ident)
	return ok && pkg.Name == "http"
}

// isInternalErrorStatus — третий аргумент http.Error равен 500 в любой из
// принятых записей: http.StatusInternalServerError или литерал.
func isInternalErrorStatus(arg ast.Expr) bool {
	switch v := arg.(type) {
	case *ast.SelectorExpr:
		return v.Sel.Name == "StatusInternalServerError"
	case *ast.BasicLit:
		return v.Value == "500"
	}
	return false
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b []byte
	for n > 0 {
		b = append([]byte{byte('0' + n%10)}, b...)
		n /= 10
	}
	return string(b)
}
