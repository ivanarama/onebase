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
	"path"
	"sort"
	"strconv"
	"strings"
	"testing"
)

const uiPackagePath = "github.com/ivantit66/onebase/internal/ui"

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

func TestUIServerSurfaceIsFrozen(t *testing.T) {
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatal(err)
	}
	fset := token.NewFileSet()
	var files []*ast.File
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		file, err := parser.ParseFile(fset, name, nil, 0)
		if err != nil {
			t.Fatalf("разбор %s: %v", name, err)
		}
		files = append(files, file)
	}
	called := uiServerMethods(files)

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

func TestUIServerMethodsTrackAliasesAndHelperParameters(t *testing.T) {
	const source = `package api
import frontend "github.com/ivantit66/onebase/internal/ui"

type Server struct { frontend *frontend.Server }
type unrelated struct{}

func helper(target *frontend.Server) { target.FromHelper() }
func forward(target *frontend.Server) *frontend.Server { return target }

func use(s *Server) {
	direct := s.frontend
	alias := direct
	alias.FromAlias()
	forward(alias).FromReturningHelper()
}

func sameOldName(uiSrv unrelated) { uiSrv.NotUI() }
`
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "alias_fixture.go", source, 0)
	if err != nil {
		t.Fatal(err)
	}
	got := uiServerMethods([]*ast.File{file})
	for _, method := range []string{"FromAlias", "FromHelper", "FromReturningHelper"} {
		if !got[method] {
			t.Errorf("не найден метод ui.Server через алиас/helper: %s", method)
		}
	}
	if got["NotUI"] {
		t.Error("одно имя переменной uiSrv не должно считаться значением ui.Server")
	}
}

// uiServerMethods находит выбор метода у значения ui.Server по его источнику и
// типу, а не по имени переменной. Поэтому переименование `uiSrv`, алиас
// `front := s.uiSrv` и helper с параметром `*ui.Server` не обходят бюджет.
func uiServerMethods(files []*ast.File) map[string]bool {
	type fileInfo struct {
		file    *ast.File
		aliases map[string]bool
	}

	infos := make([]fileInfo, 0, len(files))
	uiFields := map[string]bool{}
	uiReturnFuncs := map[string]bool{}
	uiObjects := map[*ast.Object]bool{}

	for _, file := range files {
		aliases := uiImportAliases(file)
		infos = append(infos, fileInfo{file: file, aliases: aliases})
		ast.Inspect(file, func(node ast.Node) bool {
			switch n := node.(type) {
			case *ast.StructType:
				for _, field := range n.Fields.List {
					if isUIServerType(field.Type, aliases) {
						for _, name := range field.Names {
							uiFields[name.Name] = true
						}
					}
				}
			case *ast.FuncDecl:
				if fieldListHasUIServer(n.Type.Results, aliases) {
					uiReturnFuncs[n.Name.Name] = true
				}
				markUIFieldObjects(n.Type.Params, aliases, uiObjects)
				markUIFieldObjects(n.Type.Results, aliases, uiObjects)
			case *ast.FuncLit:
				markUIFieldObjects(n.Type.Params, aliases, uiObjects)
				markUIFieldObjects(n.Type.Results, aliases, uiObjects)
			case *ast.ValueSpec:
				if n.Type != nil && isUIServerType(n.Type, aliases) {
					for _, name := range n.Names {
						markUIObject(name, uiObjects)
					}
				}
			}
			return true
		})
	}

	// Алиасы могут образовывать цепочки (`a := s.uiSrv; b := a`), поэтому
	// распространяем признак до неподвижной точки. ast.Object сохраняет области
	// видимости и не путает одноимённые переменные в разных блоках.
	for changed := true; changed; {
		changed = false
		for _, info := range infos {
			ast.Inspect(info.file, func(node ast.Node) bool {
				switch n := node.(type) {
				case *ast.AssignStmt:
					for i := 0; i < len(n.Lhs) && i < len(n.Rhs); i++ {
						if uiServerExpr(n.Rhs[i], info.aliases, uiFields, uiReturnFuncs, uiObjects) {
							if id, ok := n.Lhs[i].(*ast.Ident); ok && markUIObject(id, uiObjects) {
								changed = true
							}
						}
					}
				case *ast.ValueSpec:
					for i := 0; i < len(n.Names) && i < len(n.Values); i++ {
						if uiServerExpr(n.Values[i], info.aliases, uiFields, uiReturnFuncs, uiObjects) &&
							markUIObject(n.Names[i], uiObjects) {
							changed = true
						}
					}
				}
				return true
			})
		}
	}

	called := map[string]bool{}
	for _, info := range infos {
		ast.Inspect(info.file, func(node ast.Node) bool {
			sel, ok := node.(*ast.SelectorExpr)
			if ok && uiServerExpr(sel.X, info.aliases, uiFields, uiReturnFuncs, uiObjects) {
				called[sel.Sel.Name] = true
			}
			return true
		})
	}
	return called
}

func uiImportAliases(file *ast.File) map[string]bool {
	aliases := map[string]bool{}
	for _, spec := range file.Imports {
		importPath, err := strconv.Unquote(spec.Path.Value)
		if err != nil || importPath != uiPackagePath {
			continue
		}
		name := path.Base(importPath)
		if spec.Name != nil {
			name = spec.Name.Name
		}
		aliases[name] = true
	}
	return aliases
}

func fieldListHasUIServer(fields *ast.FieldList, aliases map[string]bool) bool {
	if fields == nil {
		return false
	}
	for _, field := range fields.List {
		if isUIServerType(field.Type, aliases) {
			return true
		}
	}
	return false
}

func markUIFieldObjects(fields *ast.FieldList, aliases map[string]bool, objects map[*ast.Object]bool) {
	if fields == nil {
		return
	}
	for _, field := range fields.List {
		if !isUIServerType(field.Type, aliases) {
			continue
		}
		for _, name := range field.Names {
			markUIObject(name, objects)
		}
	}
}

func markUIObject(id *ast.Ident, objects map[*ast.Object]bool) bool {
	if id == nil || id.Obj == nil || objects[id.Obj] {
		return false
	}
	objects[id.Obj] = true
	return true
}

func isUIServerType(expr ast.Expr, aliases map[string]bool) bool {
	switch n := expr.(type) {
	case *ast.ParenExpr:
		return isUIServerType(n.X, aliases)
	case *ast.StarExpr:
		return isUIServerType(n.X, aliases)
	case *ast.SelectorExpr:
		pkg, ok := n.X.(*ast.Ident)
		return ok && aliases[pkg.Name] && n.Sel.Name == "Server"
	case *ast.Ident:
		return aliases["."] && n.Name == "Server"
	}
	return false
}

func uiServerExpr(expr ast.Expr, aliases, fields, returnFuncs map[string]bool, objects map[*ast.Object]bool) bool {
	switch n := expr.(type) {
	case *ast.Ident:
		return n.Obj != nil && objects[n.Obj]
	case *ast.ParenExpr:
		return uiServerExpr(n.X, aliases, fields, returnFuncs, objects)
	case *ast.StarExpr:
		return uiServerExpr(n.X, aliases, fields, returnFuncs, objects)
	case *ast.UnaryExpr:
		return uiServerExpr(n.X, aliases, fields, returnFuncs, objects)
	case *ast.SelectorExpr:
		return fields[n.Sel.Name]
	case *ast.TypeAssertExpr:
		return n.Type != nil && isUIServerType(n.Type, aliases)
	case *ast.CallExpr:
		if isUIServerType(n.Fun, aliases) {
			return true
		}
		switch fun := n.Fun.(type) {
		case *ast.Ident:
			if aliases["."] && fun.Name == "New" {
				return true
			}
			return returnFuncs[fun.Name]
		case *ast.SelectorExpr:
			if pkg, ok := fun.X.(*ast.Ident); ok && aliases[pkg.Name] && fun.Sel.Name == "New" {
				return true
			}
			return returnFuncs[fun.Sel.Name]
		}
	}
	return false
}
