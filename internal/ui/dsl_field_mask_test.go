package ui

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/ivantit66/onebase/internal/auth"
	"github.com/ivantit66/onebase/internal/dsl/interpreter"
	"github.com/ivantit66/onebase/internal/metadata"
)

// ctxSource — минимальный docsCtxSource для DSL-прокси в тестах.
type ctxSource struct{ ctx context.Context }

func (c ctxSource) Ctx() context.Context { return c.ctx }

// errFromPanic превращает RaiseUserError интерпретатора в обычную ошибку.
func errFromPanic(r any) error {
	if err, ok := r.(error); ok {
		return err
	}
	return fmt.Errorf("%v", r)
}

func dslMaskEntities() (*metadata.Entity, *metadata.Entity) {
	client := &metadata.Entity{
		Name: "Клиент",
		Kind: metadata.KindCatalog,
		Fields: []metadata.Field{
			{Name: "Наименование", Type: metadata.FieldTypeString},
			{Name: "Телефон", Type: metadata.FieldTypeString},
		},
	}
	order := &metadata.Entity{
		Name: "Заявка",
		Kind: metadata.KindDocument,
		Fields: []metadata.Field{
			{Name: "Номер", Type: metadata.FieldTypeString},
			{Name: "Телефон", Type: metadata.FieldTypeString},
			{Name: "Клиент", Type: "reference:Клиент", RefEntity: "Клиент"},
		},
	}
	return client, order
}

// Объект, прочитанный из БД через DSL (Ссылка.ПолучитьОбъект), обязан отдавать
// защищённый реквизит замаскированным — иначе обработка становится обходом
// полевой политики (план 88E).
func TestDSL_CatalogObjectGetIsMasked(t *testing.T) {
	client, order := dslMaskEntities()
	s, ctx := newSubmitTestServer(t, []*metadata.Entity{client, order})
	id := uuid.New()
	if err := s.store.Upsert(ctx, "Клиент", id, map[string]any{
		"Наименование": "Иванов", "Телефон": "+79161234455",
	}, client); err != nil {
		t.Fatal(err)
	}
	user := uiMaskUser([]string{"read", "write"}, auth.FieldPolicies{"Телефон": {Read: "mask_tail", Keep: 4}})
	uctx := auth.ContextWithUser(ctx, user)

	obj, err := s.catObjectFactory(ctxSource{uctx}).LoadCatalogObject(client, id.String())
	if err != nil {
		t.Fatal(err)
	}
	w := obj.(*catWriter)
	if got := w.Get("Телефон"); got != "••••••••4455" {
		t.Fatalf("реквизит прочитанного объекта не замаскирован: %v", got)
	}
	if got := w.Get("Наименование"); got != "Иванов" {
		t.Fatalf("незащищённый реквизит изменён: %v", got)
	}

	// Значение, присвоенное самим модулем, читается как есть.
	w.Set("Телефон", "+79990001122")
	if got := w.Get("Телефон"); got != "+79990001122" {
		t.Fatalf("присвоенное модулем значение маскировать нельзя: %v", got)
	}
	// …но записью реальное значение не подменяется: видно только маску —
	// изменить нельзя (тот же контракт, что у формы и REST).
	if _, err := func() (res any, err error) {
		defer func() {
			if r := recover(); r != nil {
				err = errFromPanic(r)
			}
		}()
		return w.CallMethod("записать", nil), nil
	}(); err != nil {
		t.Fatal(err)
	}
	row, err := s.store.GetByID(ctx, "Клиент", id, client)
	if err != nil {
		t.Fatal(err)
	}
	if row["Телефон"] != "+79161234455" {
		t.Fatalf("защищённый реквизит перезаписан из DSL: %v", row["Телефон"])
	}
}

// Объект, созданный самим модулем, не маскируется: значение принадлежит текущей
// операции, а не чужой записи.
func TestDSL_NewCatalogObjectNotMasked(t *testing.T) {
	client, order := dslMaskEntities()
	s, ctx := newSubmitTestServer(t, []*metadata.Entity{client, order})
	user := uiMaskUser([]string{"read", "write"}, auth.FieldPolicies{"Телефон": {Read: "mask_all"}})
	uctx := auth.ContextWithUser(ctx, user)

	obj := s.catObjectFactory(ctxSource{uctx}).NewCatalogObject(client)
	w := obj.(*catWriter)
	w.Set("Телефон", "+79161234455")
	if got := w.Get("Телефон"); got != "+79161234455" {
		t.Fatalf("новый объект маскировать нельзя: %v", got)
	}
}

// Разыменование ссылки на чужую запись (this.Клиент.Телефон) — такой же путь
// чтения, как форма или REST.
func TestDSL_RefAttrDereferenceIsMasked(t *testing.T) {
	client, order := dslMaskEntities()
	s, ctx := newSubmitTestServer(t, []*metadata.Entity{client, order})
	id := uuid.New()
	if err := s.store.Upsert(ctx, "Клиент", id, map[string]any{
		"Наименование": "Иванов", "Телефон": "+79161234455",
	}, client); err != nil {
		t.Fatal(err)
	}
	user := uiMaskUser([]string{"read"}, auth.FieldPolicies{"Телефон": {Read: "mask_tail", Keep: 4}})
	uctx := auth.ContextWithUser(ctx, user)

	resolver := s.newDSLRefAttrResolver(uctx)
	ref := &interpreter.Ref{UUID: id.String(), Type: "Клиент"}
	v, ok := resolver.ResolveRefAttr(ref, "Телефон")
	if !ok {
		t.Fatal("реквизит по ссылке не разрешён")
	}
	if v != "••••••••4455" {
		t.Fatalf("разыменование отдало реальное значение: %v", v)
	}
}

// Поиск по защищённому реквизиту восстанавливает скрытое значение перебором —
// закрыт целиком, как отбор ГДЕ в запросе.
func TestDSL_FindByMaskedFieldDenied(t *testing.T) {
	client, order := dslMaskEntities()
	s, ctx := newSubmitTestServer(t, []*metadata.Entity{client, order})
	user := uiMaskUser([]string{"read"}, auth.FieldPolicies{"Телефон": {Read: "mask_all"}})
	uctx := auth.ContextWithUser(ctx, user)
	if !s.dslFieldSearchDenied(uctx, client, "Телефон") {
		t.Fatal("поиск по защищённому реквизиту должен быть запрещён")
	}
	if s.dslFieldSearchDenied(uctx, client, "Наименование") {
		t.Fatal("поиск по обычному реквизиту запрещать нельзя")
	}

	docUser := &auth.User{ID: "op", Login: "operator", Roles: []*auth.Role{{
		Name: "Оператор",
		Permissions: auth.Permission{
			Documents: map[string][]string{"Заявка": {"read"}},
			FieldAccess: auth.FieldAccess{
				Documents: map[string]auth.FieldPolicies{"Заявка": {"Телефон": {Read: "mask_all"}}},
			},
		},
	}}}
	docs := newDocsRoot(s, ctxSource{auth.ContextWithUser(ctx, docUser)})
	p, ok := docs.Get("Заявка").(*docProxy)
	if !ok {
		t.Fatalf("Документы.Заявка → %T", docs.Get("Заявка"))
	}
	err := func() (err error) {
		defer func() {
			if r := recover(); r != nil {
				err = errFromPanic(r)
			}
		}()
		p.CallMethod("найтипореквизиту", []any{"Телефон", "+79161234455"})
		return nil
	}()
	if err == nil || !strings.Contains(err.Error(), "защищён") {
		t.Fatalf("НайтиПоРеквизиту по защищённому полю должен падать, получено: %v", err)
	}
}

// Запрос из модуля не должен быть обходом маски: колонка приходит
// замаскированной, отбор по защищённому полю отклоняется.
func TestDSL_QueryGuardMasksAndDenies(t *testing.T) {
	client, order := dslMaskEntities()
	s, ctx := newSubmitTestServer(t, []*metadata.Entity{client, order})
	if err := s.store.Upsert(ctx, "Клиент", uuid.New(), map[string]any{
		"Наименование": "Иванов", "Телефон": "+79161234455",
	}, client); err != nil {
		t.Fatal(err)
	}
	user := uiMaskUser([]string{"read"}, auth.FieldPolicies{"Телефон": {Read: "mask_tail", Keep: 4}})
	uctx := auth.ContextWithUser(ctx, user)

	res, err := s.compileQueryWithRowAccess(uctx, `ВЫБРАТЬ Телефон ИЗ Справочник.Клиент`, nil)
	if err != nil {
		t.Fatal(err)
	}
	rows, err := s.store.QueryAll(uctx, res.SQL, res.Args...)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.dslQueryGuard(uctx, res, rows); err != nil {
		t.Fatal(err)
	}
	if rows[0]["телефон"] != "••••••••4455" {
		t.Fatalf("запрос из модуля отдал реальное значение: %v", rows[0])
	}

	denied, err := s.compileQueryWithRowAccess(uctx, `ВЫБРАТЬ Наименование ИЗ Справочник.Клиент ГДЕ Телефон = "+79161234455"`, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.dslQueryGuard(uctx, denied, nil); err == nil {
		t.Fatal("отбор по защищённому полю из модуля должен отклоняться")
	}
}

// Поиск по защищённому реквизиту СПРАВОЧНИКА обязан отклоняться так же, как у
// документов: попадание подтверждает скрытое маской значение, то есть даёт
// оракул перебора, а представление найденной ссылки печатается Сообщить().
// Проверяется реальный путь исполнения — объект «Справочники» из buildDSLVars,
// а не сам хелпер: гейт был написан, но к прокси справочников не подключён.
func TestDSL_ПоискПоЗащищённомуРеквизитуСправочникаОтклоняется(t *testing.T) {
	client, order := dslMaskEntities()
	s, ctx := newSubmitTestServer(t, []*metadata.Entity{client, order})
	if err := s.store.Upsert(ctx, "Клиент", uuid.New(), map[string]any{
		"Наименование": "Иванов", "Телефон": "+79161234455",
	}, client); err != nil {
		t.Fatal(err)
	}

	catalogProxy := func(policies auth.FieldPolicies) *interpreter.CatalogProxy {
		t.Helper()
		uctx := auth.ContextWithUser(ctx, uiMaskUser([]string{"read"}, policies))
		vars, _ := s.buildDSLVarsTx(uctx, nil)
		root, ok := vars["Справочники"].(*interpreter.CatalogsRoot)
		if !ok {
			t.Fatalf("Справочники → %T", vars["Справочники"])
		}
		p, ok := root.Get("Клиент").(*interpreter.CatalogProxy)
		if !ok {
			t.Fatalf("Справочники.Клиент → %T", root.Get("Клиент"))
		}
		return p
	}
	call := func(p *interpreter.CatalogProxy, method string, args ...any) (res any, err error) {
		defer func() {
			if r := recover(); r != nil {
				err = errFromPanic(r)
			}
		}()
		return p.CallMethod(method, args), nil
	}

	byPhone := catalogProxy(auth.FieldPolicies{"Телефон": {Read: "mask_all"}})
	for _, tc := range []struct {
		method string
		args   []any
	}{
		{"найтипореквизиту", []any{"Телефон", "+79161234455"}},
		{"проверитьсовпадениепореквизиту", []any{"Телефон", "+79161234455"}},
	} {
		res, err := call(byPhone, tc.method, tc.args...)
		if err == nil || !strings.Contains(err.Error(), "защищён") {
			t.Fatalf("%s по защищённому реквизиту: err=%v, результат=%v", tc.method, err, res)
		}
	}
	// Незащищённый реквизит ищется как прежде.
	if res, err := call(byPhone, "найтипонаименованию", "Иванов"); err != nil || res == nil {
		t.Fatalf("поиск по обычному реквизиту сломан: err=%v res=%v", err, res)
	}

	// Маска на Наименование закрывает и НайтиПоНаименованию: метод — тот же
	// поиск по реквизиту, только имя поля зашито в вызове.
	byName := catalogProxy(auth.FieldPolicies{"Наименование": {Read: "mask_all"}})
	if res, err := call(byName, "найтипонаименованию", "Иванов"); err == nil ||
		!strings.Contains(err.Error(), "защищён") {
		t.Fatalf("НайтиПоНаименованию при маске на Наименование: err=%v, результат=%v", err, res)
	}
}

// Прочитать() и Записать() не должны снимать маску с реквизита. Оба пути ведут
// в одно и то же место: признак «присвоено модулем» ставится при Set и раньше
// не снимался никогда, а обе операции кладут в тот же набор реальные значения
// из БД — модуль читал то, чего не видел.
func TestDSL_ОперацииНеСнимаютМаскуСРеквизита(t *testing.T) {
	client, order := dslMaskEntities()
	s, ctx := newSubmitTestServer(t, []*metadata.Entity{client, order})
	id := uuid.New()
	if err := s.store.Upsert(ctx, "Клиент", id, map[string]any{
		"Наименование": "Иванов", "Телефон": "+79161234455",
	}, client); err != nil {
		t.Fatal(err)
	}
	uctx := auth.ContextWithUser(ctx,
		uiMaskUser([]string{"read", "write"}, auth.FieldPolicies{"Телефон": {Read: "mask_all"}}))

	load := func() *catWriter {
		t.Helper()
		obj, err := s.catObjectFactory(ctxSource{uctx}).LoadCatalogObject(client, id.String())
		if err != nil {
			t.Fatal(err)
		}
		w, ok := obj.(*catWriter)
		if !ok {
			t.Fatalf("ПолучитьОбъект → %T", obj)
		}
		return w
	}
	call := func(w *catWriter, method string) error {
		var err error
		func() {
			defer func() {
				if r := recover(); r != nil {
					err = errFromPanic(r)
				}
			}()
			w.CallMethod(method, nil)
		}()
		return err
	}

	// Об.Телефон = ""; Об.Прочитать(); Сообщить(Об.Телефон)
	w := load()
	if got := w.Get("Телефон"); got != "••••••" {
		t.Fatalf("сразу после ПолучитьОбъект: %v", got)
	}
	w.Set("Телефон", "0000")
	if err := call(w, "прочитать"); err != nil {
		t.Fatal(err)
	}
	if got := w.Get("Телефон"); got != "••••••" {
		t.Fatalf("после Прочитать() маска снята: %v", got)
	}

	// Об.Телефон = "0000"; Об.Записать(); Сообщить(Об.Телефон)
	w = load()
	w.Set("Телефон", "0000")
	if err := call(w, "записать"); err != nil {
		t.Fatal(err)
	}
	if got := w.Get("Телефон"); got != "••••••" {
		t.Fatalf("после Записать() маска снята: %v", got)
	}
	// В самой базе значение при этом не перезаписано.
	row, err := s.store.GetByID(ctx, "Клиент", id, client)
	if err != nil {
		t.Fatal(err)
	}
	if row["Телефон"] != "+79161234455" {
		t.Fatalf("защищённый реквизит перезаписан: %v", row["Телефон"])
	}
}

// ЗначениеРеквизитаОбъекта читает ЧУЖУЮ сохранённую запись — тот же путь, что
// разыменование this.Клиент.Телефон, и обязан подчиняться полевой политике.
// Без маски обработка обходила её одной строкой (issue #649, находка #615).
func TestDSL_ЗначениеРеквизитаОбъектаМаскируется(t *testing.T) {
	client, order := dslMaskEntities()
	s, ctx := newSubmitTestServer(t, []*metadata.Entity{client, order})
	id := uuid.New()
	if err := s.store.Upsert(ctx, "Клиент", id, map[string]any{
		"Наименование": "Иванов", "Телефон": "+79161234455",
	}, client); err != nil {
		t.Fatal(err)
	}
	uctx := auth.ContextWithUser(ctx, uiMaskUser([]string{"read"},
		auth.FieldPolicies{"Телефон": {Read: "mask_tail", Keep: 4}}))
	vars, _ := s.buildDSLVarsTx(uctx, nil)

	fn, ok := vars["ЗначениеРеквизитаОбъекта"].(interpreter.BuiltinFunc)
	if !ok {
		t.Fatalf("ЗначениеРеквизитаОбъекта → %T", vars["ЗначениеРеквизитаОбъекта"])
	}
	got, err := fn([]any{&interpreter.Ref{UUID: id.String(), Type: "Клиент"}, "Телефон"}, "", 0)
	if err != nil {
		t.Fatalf("ЗначениеРеквизитаОбъекта: %v", err)
	}
	if s, _ := got.(string); strings.Contains(s, "9161234") {
		t.Errorf("реальный телефон утёк через ЗначениеРеквизитаОбъекта: %q", got)
	}
}

// ЗначенияРеквизитовОбъектов маскировала ПОБОЧНО — только через
// maskedRecordLabel, который правит строку на месте и вызывается лишь когда у
// ссылки ещё нет наименования. Ссылка с уже заполненным именем (обычный случай
// из формы или запроса) уносила реальные значения. Подслучай с именем
// обязателен: без него тест был бы зелёным на дырявом коде.
func TestDSL_ЗначенияРеквизитовОбъектовМаскируются(t *testing.T) {
	client, order := dslMaskEntities()
	s, ctx := newSubmitTestServer(t, []*metadata.Entity{client, order})
	id := uuid.New()
	if err := s.store.Upsert(ctx, "Клиент", id, map[string]any{
		"Наименование": "Иванов", "Телефон": "+79161234455",
	}, client); err != nil {
		t.Fatal(err)
	}
	uctx := auth.ContextWithUser(ctx, uiMaskUser([]string{"read"},
		auth.FieldPolicies{"Телефон": {Read: "mask_tail", Keep: 4}}))
	vars, _ := s.buildDSLVarsTx(uctx, nil)
	fn, ok := vars["ЗначенияРеквизитовОбъектов"].(interpreter.BuiltinFunc)
	if !ok {
		t.Fatalf("ЗначенияРеквизитовОбъектов → %T", vars["ЗначенияРеквизитовОбъектов"])
	}

	for _, tc := range []struct {
		name string
		ref  *interpreter.Ref
	}{
		{"ссылка без наименования", &interpreter.Ref{UUID: id.String(), Type: "Клиент"}},
		{"ссылка с наименованием", &interpreter.Ref{UUID: id.String(), Type: "Клиент", Name: "Иванов"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			list := &interpreter.Array{}
			list.CallMethod("добавить", []any{tc.ref})
			res, err := fn([]any{list, "Клиент", []any{"Телефон"}}, "", 0)
			if err != nil {
				t.Fatalf("ЗначенияРеквизитовОбъектов: %v", err)
			}
			m, ok := res.(*interpreter.Map)
			if !ok {
				t.Fatalf("результат → %T", res)
			}
			st, ok := m.CallMethod("получить", []any{tc.ref}).(*interpreter.Struct)
			if !ok {
				t.Fatalf("значение по ссылке → %T, ожидалась Структура", m.CallMethod("получить", []any{tc.ref}))
			}
			got := fmt.Sprint(st.Get("Телефон"))
			if got == "" || got == "<nil>" {
				t.Fatalf("реквизит Телефон не вернулся — тест проверял бы пустоту")
			}
			if strings.Contains(got, "9161234") {
				t.Errorf("реальный телефон утёк через ЗначенияРеквизитовОбъектов: %s", got)
			}
		})
	}
}
