package api

// Поверхность API → UI (#787 ARCH-01, план 137).
//
// `api.New` — не потребитель UI, а композиционный корень: он сам зовёт
// `ui.New`, монтирует маршруты UI в свой роутер и забирает оттуда прикладные
// сервисы. Из-за этого «развязать интерфейсом» здесь не работает: интерфейс
// вокруг вызовов жизненного цикла импорт `ui` не убирает — останутся `ui.New`
// и `ui.Config`. Настоящая развязка одна: вынести сборку наружу, чтобы `ui` и
// `api` строил кто-то третий. Владелец заявки отложил это до паузы в фичах.
//
// Пока решение ждёт, важно, чтобы поверхность не расползалась: в аудите
// 11.08 насчитали 5 вызовов, при перепроверке 15.08 — 17. Каждый новый метод
// `ui.Server`, позванный из `api`, — ещё одна нитка, которую придётся резать
// потом, и добавляется она незаметно: рядом уже есть шестнадцать таких же.
//
// Тест не запрещает трогать UI из API — он требует делать это осознанно:
// новый метод виден в списке, и его добавление становится решением, а не
// побочным эффектом.

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"sort"
	"strings"
	"testing"
)

// uiSurface — методы ui.Server, которые пакет api зовёт сегодня.
//
// Список заморожен намеренно. Добавляете вызов — впишите метод сюда и объясните
// в PR, почему он нужен именно из api; убрали вызов — уберите строку.
var uiSurface = map[string]bool{
	"BeginShutdown":          true,
	"BuildJobDSLVars":        true,
	"EntitySvc":              true,
	"Incidents":              true,
	"InvalidateServiceCache": true,
	"InvalidateWidgetCache":  true,
	"Mount":                  true,
	"MountDebug":             true,
	"MountExchange":          true,
	"MountPWA":               true,
	"MountServices":          true,
	"MountStatic":            true,
	"PublishDevReload":       true,
	"ResyncWSIntakes":        true,
	"SSESubscriberCount":     true,
	"ServiceCacheStats":      true,
	"Shutdown":               true,
}

// uiReceivers — имена, под которыми ui.Server живёт в коде api: локальная
// переменная в конструкторе и поле сервера.
var uiReceivers = map[string]bool{"uiSrv": true}

func TestUIServerSurfaceIsFrozen(t *testing.T) {
	called := map[string]bool{}

	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatal(err)
	}
	fset := token.NewFileSet()
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		file, err := parser.ParseFile(fset, name, nil, 0)
		if err != nil {
			t.Fatalf("разбор %s: %v", name, err)
		}
		ast.Inspect(file, func(node ast.Node) bool {
			sel, ok := node.(*ast.SelectorExpr)
			if !ok {
				return true
			}
			if uiExpr(sel.X) {
				called[sel.Sel.Name] = true
			}
			return true
		})
	}

	var added, gone []string
	for m := range called {
		if !uiSurface[m] {
			added = append(added, m)
		}
	}
	for m := range uiSurface {
		if !called[m] {
			gone = append(gone, m)
		}
	}
	sort.Strings(added)
	sort.Strings(gone)

	if len(added) > 0 {
		t.Errorf("api зовёт новые методы ui.Server: %s.\n"+
			"Поверхность API → UI заморожена на %d методах (#787, план 137): каждая новая нитка "+
			"удорожает вынос сборки наружу, а добавляется незаметно — рядом уже есть такие же.\n"+
			"Нужен вызов — впишите метод в uiSurface и объясните в PR, почему он нужен именно из api.",
			strings.Join(added, ", "), len(uiSurface))
	}
	if len(gone) > 0 {
		t.Errorf("методы ui.Server больше не зовутся из api: %s — уберите их из uiSurface, "+
			"иначе список перестанет отражать настоящую связь.", strings.Join(gone, ", "))
	}
}

// uiExpr — обращение к ui.Server: локальная переменная `uiSrv` или поле
// `s.uiSrv`. Именно так связь и записана во всём пакете.
func uiExpr(x ast.Expr) bool {
	switch v := x.(type) {
	case *ast.Ident:
		return uiReceivers[v.Name]
	case *ast.SelectorExpr:
		return uiReceivers[v.Sel.Name]
	}
	return false
}
