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
	"go/importer"
	"go/parser"
	"go/token"
	"go/types"
	"os"
	"path"
	"sort"
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
	called := uiServerMethods(fset, files)

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
type frontendAlias = frontend.Server
type unrelated struct{}

func helper(target *frontend.Server) { target.FromHelper() }
func forward(target *frontend.Server) *frontend.Server { return target }
func typedAlias(target *frontendAlias) { target.FromTypeAlias() }

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
	got := uiServerMethods(fset, []*ast.File{file})
	for _, method := range []string{"FromAlias", "FromHelper", "FromReturningHelper", "FromTypeAlias"} {
		if !got[method] {
			t.Errorf("не найден метод ui.Server через алиас/helper: %s", method)
		}
	}
	if got["NotUI"] {
		t.Error("одно имя переменной uiSrv не должно считаться значением ui.Server")
	}
}

// uiServerMethods находит выбор метода по настоящему типу receiver. Поэтому
// переименование `uiSrv`, алиас `front := s.uiSrv`, helper с параметром
// `*ui.Server` и type alias не обходят бюджет и не требуют эвристик по именам.
func uiServerMethods(fset *token.FileSet, files []*ast.File) map[string]bool {
	info := &types.Info{Types: make(map[ast.Expr]types.TypeAndValue)}
	conf := types.Config{
		Importer: newUISurfaceImporter(),
		// `go test` уже компилирует настоящий пакет. Здесь импортёр намеренно
		// подставляет лёгкие заглушки внутренним зависимостям: для гейта нужен
		// только точный тип ui.Server, ошибки чужих selector-ов несущественны.
		Error: func(error) {},
	}
	_, _ = conf.Check("github.com/ivantit66/onebase/internal/api", fset, files, info)

	called := map[string]bool{}
	for _, file := range files {
		ast.Inspect(file, func(node ast.Node) bool {
			sel, ok := node.(*ast.SelectorExpr)
			if ok && isUIServerGoType(info.TypeOf(sel.X)) {
				called[sel.Sel.Name] = true
			}
			return true
		})
	}
	return called
}

func isUIServerGoType(typ types.Type) bool {
	if typ == nil {
		return false
	}
	typ = types.Unalias(typ)
	for {
		ptr, ok := typ.(*types.Pointer)
		if !ok {
			break
		}
		typ = types.Unalias(ptr.Elem())
	}
	named, ok := typ.(*types.Named)
	if !ok || named.Obj().Pkg() == nil {
		return false
	}
	return named.Obj().Name() == "Server" && named.Obj().Pkg().Path() == uiPackagePath
}

type uiSurfaceImporter struct {
	standard  types.Importer
	uiPackage *types.Package
	stubs     map[string]*types.Package
}

func newUISurfaceImporter() *uiSurfaceImporter {
	return &uiSurfaceImporter{
		standard:  importer.Default(),
		uiPackage: newUISurfacePackage(),
		stubs:     make(map[string]*types.Package),
	}
}

func (imp *uiSurfaceImporter) Import(importPath string) (*types.Package, error) {
	if importPath == uiPackagePath {
		return imp.uiPackage, nil
	}
	if pkg, err := imp.standard.Import(importPath); err == nil {
		return pkg, nil
	}
	if pkg := imp.stubs[importPath]; pkg != nil {
		return pkg, nil
	}
	pkg := types.NewPackage(importPath, path.Base(importPath))
	pkg.MarkComplete()
	imp.stubs[importPath] = pkg
	return pkg, nil
}

func newUISurfacePackage() *types.Package {
	pkg := types.NewPackage(uiPackagePath, "ui")
	serverName := types.NewTypeName(token.NoPos, pkg, "Server", nil)
	serverType := types.NewNamed(serverName, types.NewStruct(nil, nil), nil)
	pkg.Scope().Insert(serverName)

	configName := types.NewTypeName(token.NoPos, pkg, "Config", nil)
	types.NewNamed(configName, types.NewStruct(nil, nil), nil)
	pkg.Scope().Insert(configName)

	anyType := types.Universe.Lookup("any").Type()
	params := types.NewTuple(types.NewParam(token.NoPos, pkg, "args", types.NewSlice(anyType)))
	results := types.NewTuple(types.NewParam(token.NoPos, pkg, "", types.NewPointer(serverType)))
	newSignature := types.NewSignatureType(nil, nil, nil, params, results, true)
	pkg.Scope().Insert(types.NewFunc(token.NoPos, pkg, "New", newSignature))
	pkg.MarkComplete()
	return pkg
}
