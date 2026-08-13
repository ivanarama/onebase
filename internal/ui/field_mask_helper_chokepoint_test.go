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

// Страж маскирования уровня ХЕЛПЕРА (issue #649).
//
// Соседний field_mask_chokepoint_test проверяет только HTTP-хендлеры с прямым
// store.List/GetByID, а хелперы намеренно пропускает — «их закрывают вызывающие
// хендлеры». Между хендлером и store в реальном коде почти всегда стоит хелпер,
// поэтому правило почти никогда не срабатывало: класс «хендлер → хелпер → store»
// был невидим по построению. Через это слепое пятно и прошёл #609 —
// handleManagedFormEvent сам store не читает, он делегирует чтение
// restoreUnsubmittedFields и refreshFieldsWrittenByHandler.
//
// Транзитивный вариант правила («хендлер читает store, если это делает любой
// достижимый хелпер») не работает: вызов маски где-то в графе засчитывается за
// всю цепочку, и дырявый путь остаётся незамеченным. Работающая формулировка —
// правило применяется К САМОЙ ФУНКЦИИ: читаешь store.List/GetByID — либо
// накладывай маску, либо стой в списке исключений с объяснением, почему
// прочитанное не уходит клиенту. Решение принимается там, где есть контекст.

var maskHelperGateFuncs = map[string]bool{
	"maskRecord": true, "maskRecords": true,
	// maskedRecordLabel зовёт maskRecord и отдаёт наружу только представление
	// (field_access.go:82) — законная точка наложения маски.
	"maskedRecordLabel": true,
	// maskDSLValue — та же полевая политика для одиночного значения в DSL.
	"maskDSLValue": true,
}

// Читающие методы store, отдающие ЗНАЧЕНИЯ реквизитов. GetFieldsByIDs добавлен
// сверх набора хендлерного стража: именно через него читает
// ЗначенияРеквизитовОбъектов, и без него утечка в этой функции была бы
// сканеру не видна.
var maskHelperStoreReads = map[string]bool{
	"List": true, "GetByID": true, "GetFieldsByIDs": true, "ListMarked": true,
}

// maskHelperExemption — почему функция читает store без маски. maskedBy != ""
// означает «гейт стоит не здесь, а в названной функции»: тест следит, что там
// он действительно есть, — иначе снятие того гейта пройдёт незамеченным.
type maskHelperExemption struct {
	reason   string
	maskedBy string
}

var maskHelperExempt = map[string]maskHelperExemption{
	// ── Проверки строкового доступа (RLS): наружу идёт bool, 403 или
	// ErrRowAccessDenied, значения реквизитов никуда не отдаются. Маскировать
	// строку здесь означало бы решать доступ по маске вместо данных.
	"Server.matchRowPredicate": {reason: "RLS: строка нужна для вычисления предиката, наружу идёт bool"},
	"Server.rowAllowedID":      {reason: "RLS: наружу идёт bool"},
	"Server.rowAllowedUpdate":  {reason: "RLS: наружу идёт bool"},
	"Server.rowAllowsID":       {reason: "RLS: наружу идёт bool"},
	"Server.checkDSLRowAccess": {reason: "RLS для DSL: наружу идёт ошибка ErrRowAccessDenied"},
	"changePublisher.canSee":   {reason: "RLS-адресация живого списка: наружу идёт bool"},
	"Server.publishDocChange": {reason: "живой список (план 87): читает after для адресации по правам, " +
		"само событие несёт только действие, строки клиенту не отдаются"},
	"Server.blobReferencedWithPolicy": {reason: "проверка, ссылается ли видимая строка на блоб: наружу идёт bool"},
	"Server.loadAuthorizedRecordHistory": {reason: "история объекта (план 121): строка читается только для проверки " +
		"построчного доступа и наружу не идёт; сами значения истории редактирует redactAuditEntries"},
	"Server.validateConstant": {reason: "валидация ссылки константы: наружу идёт текст ошибки, не значения"},

	// ── Пути записи: значения нужны НАСТОЯЩИМИ. Замаскировать при чтении значило
	// бы записать строку-маску поверх реального значения — это хуже утечки:
	// утечка обратима, испорченные данные нет.
	"Server.protectMaskedFieldsOnWrite": {reason: "защита от записи маски: подставляет реальное значение вместо присланного, к клиенту не идёт"},
	"Server.markForDeletion":            {reason: "путь записи: читает признак проведения, чтобы очистить движения"},
	"docProxy.DeleteRef":                {reason: "путь удаления: pre-образ только для адресации живого списка"},
	"docWriter.writeInContext":          {reason: "путь записи: pre-образ только для адресации живого списка"},

	// ── Отдаются только идентификаторы.
	"Server.deleteMarked": {reason: "групповое удаление помеченных: из строк берётся только id"},

	// ── Дубль хендлерного стража (field_mask_chokepoint_test): те же три
	// функции, те же обоснования.
	"Server.discloseField": {reason: "раскрытие поля под правом disclose + аудитом (план 88, CC-SEC-004)"},
	"Server.postDocument":  {reason: "проведение использует реальные значения, поля клиенту не отдаёт"},
	"Server.deleteRecord":  {reason: "pre-образ только для RLS-адресации live-list удаления"},

	// ── Гейт стоит не здесь, а в названной функции. Тест
	// DelegatedGatesStillMask следит, что он там действительно есть.
	"docWriter.read":                       {reason: "значения кладутся в obj и маскируются на выдаче", maskedBy: "docWriter.Get"},
	"dslRefAttrResolver.preloadIDs":        {reason: "наполняет кэш, значения отдаёт только ResolveRefAttr", maskedBy: "dslRefAttrResolver.ResolveRefAttr"},
	"Server.restoreUnsubmittedFields":      {reason: "дочитывает неприсланные реквизиты ДЛЯ ЗАПИСИ, к клиенту они идут через сериализацию ответа (#609)", maskedBy: "Server.serializeManagedFormEventState"},
	"Server.refreshFieldsWrittenByHandler": {reason: "перечитывает записанное обработчиком ДЛЯ ЗАПИСИ, к клиенту — через сериализацию ответа (#609)", maskedBy: "Server.serializeManagedFormEventState"},

	// ── Особый случай.
	"Server.loadRuntimeObject": {reason: "строит Объект для DSL-обёрток (catWriter/docWriter маскируют в Get) и для " +
		"серверных хуков формы, где действует контракт «this не маскируется»: значение принадлежит текущей операции, " +
		"а не чужой записи (field_access.go, доккомментарий maskDSLValue)"},
}

// maskHelperFuncName — «Тип.Метод» для метода, «Имя» для функции. Квалификация
// обязательна: иначе read/canSee/Get из разных типов схлопнутся в одно имя.
func maskHelperFuncName(fd *ast.FuncDecl) string {
	if fd.Recv == nil || len(fd.Recv.List) == 0 {
		return fd.Name.Name
	}
	t := fd.Recv.List[0].Type
	if star, ok := t.(*ast.StarExpr); ok {
		t = star.X
	}
	if id, ok := t.(*ast.Ident); ok {
		return id.Name + "." + fd.Name.Name
	}
	return fd.Name.Name
}

type maskHelperFact struct {
	file       string
	readsStore bool
	gated      bool
}

// maskHelperScan разбирает пакет и собирает по каждой функции два факта: читает
// ли она store.List/GetByID и зовёт ли гейт маски.
func maskHelperScan(t *testing.T) map[string]maskHelperFact {
	t.Helper()
	fset := token.NewFileSet()
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	facts := map[string]maskHelperFact{}
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
				if maskHelperGateFuncs[sel.Sel.Name] {
					gated = true
				}
				if maskHelperStoreReads[sel.Sel.Name] {
					if inner, ok := sel.X.(*ast.SelectorExpr); ok && inner.Sel.Name == "store" {
						readsStore = true
					}
				}
				return true
			})
			name := maskHelperFuncName(fd)
			prev := facts[name]
			facts[name] = maskHelperFact{
				file:       f,
				readsStore: prev.readsStore || readsStore,
				gated:      prev.gated || gated,
			}
		}
	}
	return facts
}

// Каждая функция, читающая store.List/GetByID, обязана наложить маску либо
// стоять в maskHelperExempt с обоснованием.
func TestFieldMaskHelperChokepoint_NoUngatedStoreReads(t *testing.T) {
	facts := maskHelperScan(t)
	readers := 0
	var violations []string
	for name, f := range facts {
		if !f.readsStore {
			continue
		}
		readers++
		if f.gated {
			continue
		}
		if _, ok := maskHelperExempt[name]; ok {
			continue
		}
		violations = append(violations, name+" ("+f.file+")")
	}
	if readers == 0 {
		t.Fatal("детектор не нашёл ни одной функции с store.List/GetByID — сломан матчинг?")
	}
	sort.Strings(violations)
	if len(violations) > 0 {
		t.Fatalf("функции читают store.List/GetByID, но не маскируют поля и не в maskHelperExempt (%d):\n  %s\n\n"+
			"Наложите маску (maskRecord/maskRecords/maskedRecordLabel/maskDSLValue из field_access.go) "+
			"или внесите функцию в maskHelperExempt с объяснением, почему прочитанное не уходит клиенту.",
			len(violations), strings.Join(violations, "\n  "))
	}
}

// Список исключений не должен превращаться в свалку: запись без функции, под
// функцией, которая уже маскирует, или под той, что перестала читать store, —
// протухла и подлежит снятию.
func TestFieldMaskHelperChokepoint_ExemptionsAreAlive(t *testing.T) {
	facts := maskHelperScan(t)
	var stale []string
	for name, ex := range maskHelperExempt {
		f, ok := facts[name]
		switch {
		case !ok:
			stale = append(stale, name+" — функции больше нет (переименована?)")
		case !f.readsStore:
			stale = append(stale, name+" — больше не читает store")
		case f.gated:
			stale = append(stale, name+" — уже накладывает маску сама")
		}
		if ex.reason == "" {
			stale = append(stale, name+" — исключение без обоснования")
		}
	}
	sort.Strings(stale)
	if len(stale) > 0 {
		t.Fatalf("протухшие записи в maskHelperExempt (%d):\n  %s", len(stale), strings.Join(stale, "\n  "))
	}
}

// Исключения с maskedBy держатся на чужом гейте. Тест требует, чтобы этот гейт
// реально стоял: снятие маски в делегате (как в #609) обязано валить сборку, а
// не проходить незамеченным.
func TestFieldMaskHelperChokepoint_DelegatedGatesStillMask(t *testing.T) {
	facts := maskHelperScan(t)
	var broken []string
	for name, ex := range maskHelperExempt {
		if ex.maskedBy == "" {
			continue
		}
		gate, ok := facts[ex.maskedBy]
		switch {
		case !ok:
			broken = append(broken, name+" → "+ex.maskedBy+": делегат не найден")
		case !gate.gated:
			broken = append(broken, name+" → "+ex.maskedBy+": делегат больше не накладывает маску")
		}
	}
	sort.Strings(broken)
	if len(broken) > 0 {
		t.Fatalf("делегированные гейты маскирования сломаны (%d):\n  %s\n\n"+
			"Либо верните маску в названную функцию, либо наложите её на месте чтения.",
			len(broken), strings.Join(broken, "\n  "))
	}
}
