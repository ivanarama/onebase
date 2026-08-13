package interpreter

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/ivantit66/onebase/internal/metadata"
)

// fakeCatalogsDB stubs storage for catalog/predefined lookups in tests.
type fakeCatalogsDB struct {
	predefinedID map[string]string // "Entity/Name" → uuid
	byField      map[string]map[string]struct{ ID, Display string }
	// matchRows — все совпадения по реквизиту для safe-match: "Entity/Field" →
	// значение → список найденных записей (0/1/несколько).
	matchRows map[string]map[string][]struct{ ID, Display string }
	stored    map[string]map[string]any // "Entity/uuid" → шапка для GetByID
	written   []map[string]any          // запись через WriteCatalogRecord
	deleted   []string                  // UUID, удалённые через Delete
}

func (f *fakeCatalogsDB) GetByID(_ context.Context, entityName string, id uuid.UUID, _ *metadata.Entity) (map[string]any, error) {
	if f.stored == nil {
		return nil, errors.New("not found")
	}
	row, ok := f.stored[entityName+"/"+id.String()]
	if !ok {
		return nil, errors.New("not found")
	}
	out := make(map[string]any, len(row))
	for k, v := range row {
		out[k] = v
	}
	return out, nil
}

// fakeDeleter — тестовый CatalogDeleter: в проде здесь entityservice.Delete
// (хуки, CheckRefs, обмен), в юните достаточно зафиксировать факт удаления.
type fakeDeleter struct{ db *fakeCatalogsDB }

func (d fakeDeleter) DeleteCatalogRef(_ context.Context, _ *metadata.Entity, id uuid.UUID) error {
	d.db.deleted = append(d.db.deleted, id.String())
	return nil
}

func (f *fakeCatalogsDB) GetPredefinedIDStr(_ context.Context, entityName, name string) (string, error) {
	if id, ok := f.predefinedID[entityName+"/"+name]; ok {
		return id, nil
	}
	return "", errors.New("not found")
}

func (f *fakeCatalogsDB) FindCatalogByField(_ context.Context, entity *metadata.Entity, fieldName, value string) (string, string, bool, error) {
	key := entity.Name + "/" + fieldName
	if rows, ok := f.byField[key]; ok {
		if hit, ok := rows[value]; ok {
			return hit.ID, hit.Display, true, nil
		}
	}
	return "", "", false, nil
}

func (f *fakeCatalogsDB) ListCatalogMatchesByField(_ context.Context, entity *metadata.Entity, fieldName, value string) ([]string, []string, error) {
	hits := f.matchRows[entity.Name+"/"+fieldName][value]
	ids := make([]string, 0, len(hits))
	displays := make([]string, 0, len(hits))
	for _, hit := range hits {
		ids = append(ids, hit.ID)
		displays = append(displays, hit.Display)
	}
	if len(hits) == 0 {
		if rows, ok := f.byField[entity.Name+"/"+fieldName]; ok {
			if hit, ok := rows[value]; ok {
				ids = append(ids, hit.ID)
				displays = append(displays, hit.Display)
			}
		}
	}
	return ids, displays, nil
}

func (f *fakeCatalogsDB) MatchCatalogByField(_ context.Context, entity *metadata.Entity, fieldName, value string) (string, string, int, error) {
	hits := f.matchRows[entity.Name+"/"+fieldName][value]
	switch len(hits) {
	case 0:
		return "", "", 0, nil
	case 1:
		return hits[0].ID, hits[0].Display, 1, nil
	default:
		return "", "", len(hits), nil
	}
}

func (f *fakeCatalogsDB) WriteCatalogRecord(_ context.Context, entity *metadata.Entity, idStr string, fields map[string]any) (string, error) {
	rec := map[string]any{"_entity": entity.Name, "_id": idStr}
	for k, v := range fields {
		rec[k] = v
	}
	f.written = append(f.written, rec)
	if idStr == "" {
		return "99999999-9999-9999-9999-999999999999", nil
	}
	return idStr, nil
}

type fakeEntityLookup struct{ m map[string]*metadata.Entity }

func (f *fakeEntityLookup) GetEntity(name string) *metadata.Entity {
	if e, ok := f.m[name]; ok {
		return e
	}
	return nil
}

func newCatalogsTestEnv() (*CatalogsRoot, *fakeCatalogsDB, *fakeEntityLookup) {
	entity := &metadata.Entity{
		Name:   "ТипЦен",
		Kind:   metadata.KindCatalog,
		Fields: []metadata.Field{{Name: "Наименование", Type: metadata.FieldTypeString}},
		Predefined: []*metadata.PredefinedItem{
			{Name: "Закупочная"},
		},
	}
	db := &fakeCatalogsDB{
		predefinedID: map[string]string{
			"ТипЦен/Закупочная": "11111111-1111-1111-1111-111111111111",
		},
		byField: map[string]map[string]struct{ ID, Display string }{
			"ТипЦен/Наименование": {
				"Розничная": {ID: "22222222-2222-2222-2222-222222222222", Display: "Розничная"},
			},
		},
	}
	lookup := &fakeEntityLookup{m: map[string]*metadata.Entity{"ТипЦен": entity}}
	root := NewCatalogsRoot(NewStaticCtx(context.Background()), db, lookup).
		WithDeleter(fakeDeleter{db: db})
	return root, db, lookup
}

// Справочники.X.Создать().Записать() должно персистить.
func TestCatalogProxy_CreateAndWrite(t *testing.T) {
	root, db, _ := newCatalogsTestEnv()
	cp := root.Get("ТипЦен").(*CatalogProxy)

	rec := cp.CallMethod("создать", nil)
	w, ok := rec.(*CatalogRecordWriter)
	if !ok {
		t.Fatalf("Создать вернул %T, ожидался *CatalogRecordWriter", rec)
	}
	// поле через Set (Зап.Наименование = ...)
	w.Set("Наименование", "Спеццена")
	// поле через УстановитьЗначение
	w.CallMethod("установитьзначение", []any{"Код", "СЦ-001"})

	res := w.CallMethod("записать", nil)
	ref, ok := res.(*Ref)
	if !ok {
		t.Fatalf("Записать вернул %T, ожидался *Ref", res)
	}
	if ref.Name != "Спеццена" {
		t.Errorf("Ref.Name = %q, ожидалось Спеццена", ref.Name)
	}
	// проверим что запись реально дошла до db
	if len(db.written) != 1 {
		t.Fatalf("ожидалась 1 запись в db, получили %d", len(db.written))
	}
	if db.written[0]["наименование"] != "Спеццена" {
		t.Errorf("в записи нет наименования: %v", db.written[0])
	}
	if db.written[0]["код"] != "СЦ-001" {
		t.Errorf("в записи нет кода: %v", db.written[0])
	}
}

// Создать() для несуществующего справочника — Get вернёт nil.
func TestCatalogProxy_CreateUnknownEntity(t *testing.T) {
	root, _, _ := newCatalogsTestEnv()
	if v := root.Get("НетТакого"); v != nil {
		t.Errorf("Справочники.НетТакого → %v, ожидался nil", v)
	}
}

func TestCatalogsRoot_RejectsNonCatalogEntity(t *testing.T) {
	root, _, lookup := newCatalogsTestEnv()
	lookup.m["Заказ"] = &metadata.Entity{Name: "Заказ", Kind: metadata.KindDocument}
	if v := root.Get("Заказ"); v != nil {
		t.Fatalf("Справочники.Заказ → %T, want nil for document entity", v)
	}
}

// Справочники.X.ИмяПредопределённой должно возвращать Ref.
func TestCatalogProxy_PredefinedAccess(t *testing.T) {
	root, _, _ := newCatalogsTestEnv()
	proxy := root.Get("ТипЦен")
	if proxy == nil {
		t.Fatal("Справочники.ТипЦен → nil, ожидался proxy")
	}
	cp, ok := proxy.(*CatalogProxy)
	if !ok {
		t.Fatalf("ожидался *CatalogProxy, получили %T", proxy)
	}
	v := cp.Get("Закупочная")
	ref, ok := v.(*Ref)
	if !ok {
		t.Fatalf("Закупочная → %T, ожидался *Ref", v)
	}
	if ref.UUID != "11111111-1111-1111-1111-111111111111" {
		t.Errorf("неверный UUID: %s", ref.UUID)
	}
	if ref.Name != "Закупочная" {
		t.Errorf("неверное имя: %s", ref.Name)
	}
}

func TestCatalogProxy_PredefinedCaseInsensitive(t *testing.T) {
	root, _, _ := newCatalogsTestEnv()
	cp := root.Get("ТипЦен").(*CatalogProxy)
	if v := cp.Get("закупочная"); v == nil {
		t.Errorf("lowercase предопределённого не нашёлся")
	}
}

func TestCatalogProxy_PredefinedMissing(t *testing.T) {
	root, _, _ := newCatalogsTestEnv()
	cp := root.Get("ТипЦен").(*CatalogProxy)
	if v := cp.Get("НетТакого"); v != nil {
		t.Errorf("ожидался nil, получили %v", v)
	}
}

// НайтиПоНаименованию должно искать в catalog по полю Наименование.
func TestCatalogProxy_FindByName_Found(t *testing.T) {
	root, _, _ := newCatalogsTestEnv()
	cp := root.Get("ТипЦен").(*CatalogProxy)
	v := cp.CallMethod("найтипонаименованию", []any{"Розничная"})
	ref, ok := v.(*Ref)
	if !ok {
		t.Fatalf("ожидался *Ref, получили %T", v)
	}
	if ref.UUID != "22222222-2222-2222-2222-222222222222" {
		t.Errorf("неверный UUID: %s", ref.UUID)
	}
}

func TestCatalogProxy_FindByName_NotFound(t *testing.T) {
	root, _, _ := newCatalogsTestEnv()
	cp := root.Get("ТипЦен").(*CatalogProxy)
	if v := cp.CallMethod("найтипонаименованию", []any{"НетТакого"}); v != nil {
		t.Errorf("ожидался nil, получили %v", v)
	}
}

func TestCatalogProxy_FindByID_ReturnsManagedRef(t *testing.T) {
	root, db, _ := newCatalogsTestEnv()
	cp := root.Get("ТипЦен").(*CatalogProxy)
	id := "22222222-2222-2222-2222-222222222222"
	db.stored = map[string]map[string]any{
		"ТипЦен/" + id: {"Наименование": "Розничная"},
	}

	v := cp.CallMethod("найтипоидентификатору", []any{id})
	ref, ok := v.(*Ref)
	if !ok {
		t.Fatalf("ожидался *Ref, получили %T", v)
	}
	if ref.UUID != id || ref.Type != "ТипЦен" || ref.Manager == nil {
		t.Fatalf("неполная ссылка: %#v", ref)
	}
	if got := ref.CallMethod("получитьобъект", nil); got == nil {
		t.Fatal("ПолучитьОбъект вернул nil")
	}
}

func TestCatalogProxy_FindByID_InvalidUUID(t *testing.T) {
	root, _, _ := newCatalogsTestEnv()
	cp := root.Get("ТипЦен").(*CatalogProxy)
	defer func() {
		if recover() == nil {
			t.Error("ожидалась ошибка: неверный UUID")
		}
	}()
	cp.CallMethod("найтипоидентификатору", []any{"not-a-uuid"})
}

func TestCatalogProxy_UnknownEntity(t *testing.T) {
	root, _, _ := newCatalogsTestEnv()
	if v := root.Get("НетТакогоСправочника"); v != nil {
		t.Errorf("Справочники.НетТакого → %v, ожидался nil", v)
	}
}

// Ссылка, найденная через НайтиПоНаименованию, привязана к менеджеру —
// Ссылка.Удалить() удаляет запись.
func TestRef_DeleteViaManager(t *testing.T) {
	root, db, _ := newCatalogsTestEnv()
	cp := root.Get("ТипЦен").(*CatalogProxy)
	ref := cp.CallMethod("найтипонаименованию", []any{"Розничная"}).(*Ref)
	ref.CallMethod("удалить", nil)
	if len(db.deleted) != 1 || db.deleted[0] != "22222222-2222-2222-2222-222222222222" {
		t.Errorf("ожидалось удаление одной записи, deleted=%v", db.deleted)
	}
}

// Менеджерный вариант: Справочники.X.Удалить(Ссылка).
func TestCatalogProxy_DeleteByRef(t *testing.T) {
	root, db, _ := newCatalogsTestEnv()
	cp := root.Get("ТипЦен").(*CatalogProxy)
	ref := cp.Get("Закупочная").(*Ref)
	cp.CallMethod("удалить", []any{ref})
	if len(db.deleted) != 1 || db.deleted[0] != "11111111-1111-1111-1111-111111111111" {
		t.Errorf("ожидалось удаление, deleted=%v", db.deleted)
	}
}

// Без подключённого делетера удаление отказывает fail-closed, а не падает в
// обходной db.Delete: путь мимо entityservice обходил бы хуки удаления и
// проверку ссылок (issue #854).
func TestCatalogProxy_DeleteWithoutDeleterFailsClosed(t *testing.T) {
	root, db, _ := newCatalogsTestEnv()
	root.deleter = nil
	cp := root.Get("ТипЦен").(*CatalogProxy)
	ref := cp.Get("Закупочная").(*Ref)
	var raised any
	func() {
		defer func() { raised = recover() }()
		cp.CallMethod("удалить", []any{ref})
	}()
	if raised == nil {
		t.Error("ожидался отказ: делетер не подключён")
	}
	if len(db.deleted) != 0 {
		t.Errorf("запись удалена в обход хост-пути: %v", db.deleted)
	}
}

// ПолучитьОбъект() возвращает рабочий дескриптор (саму ссылку).
// Ссылка.ПолучитьОбъект() для существующей записи возвращает
// CatalogRecordWriter с предзаполненными полями: можно прочитать
// текущие значения, изменить и записать обратно по тому же UUID.
func TestRef_GetObject_LoadsExisting(t *testing.T) {
	root, db, _ := newCatalogsTestEnv()
	cp := root.Get("ТипЦен").(*CatalogProxy)

	id := "22222222-2222-2222-2222-222222222222"
	db.stored = map[string]map[string]any{
		"ТипЦен/" + id: {"Наименование": "Розничная"},
	}
	ref := &Ref{UUID: id, Name: "Розничная", Type: "ТипЦен", Manager: cp}

	got := ref.CallMethod("получитьобъект", nil)
	w, ok := got.(*CatalogRecordWriter)
	if !ok {
		t.Fatalf("ПолучитьОбъект вернул %T, ожидался *CatalogRecordWriter", got)
	}
	if w.idStr != id {
		t.Errorf("writer.idStr = %q, want %q", w.idStr, id)
	}
	if v := w.Get("Наименование"); v != "Розничная" {
		t.Errorf("Get(Наименование) = %v, want \"Розничная\"", v)
	}

	// Изменение поля и Записать() → WriteCatalogRecord с тем же idStr.
	w.Set("Наименование", "Розничная (изменено)")
	w.CallMethod("записать", nil)

	if len(db.written) != 1 {
		t.Fatalf("written = %d, want 1", len(db.written))
	}
	if got := db.written[0]["_id"]; got != id {
		t.Errorf("WriteCatalogRecord idStr = %v, want %q (UPDATE существующей записи, а не INSERT)", got, id)
	}
}

// Ссылка без менеджера → понятная ошибка вместо тихого nil или паники.
func TestRef_GetObject_NoManager(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Error("ожидалась ошибка: ссылка без менеджера")
		}
	}()
	(&Ref{UUID: "x", Name: "Тест"}).CallMethod("получитьобъект", nil)
}

// Пустая ссылка (Создать().Ссылка до Записи) → понятная ошибка.
func TestRef_GetObject_EmptyUUID(t *testing.T) {
	root, _, _ := newCatalogsTestEnv()
	cp := root.Get("ТипЦен").(*CatalogProxy)
	defer func() {
		if recover() == nil {
			t.Error("ожидалась ошибка: пустая ссылка")
		}
	}()
	(&Ref{UUID: "", Name: "", Manager: cp}).CallMethod("получитьобъект", nil)
}

// Вызов неизвестного метода на ссылке поднимает ошибку, а не молча nil.
func TestRef_UnknownMethodRaises(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Error("ожидалась ошибка для неизвестного метода ссылки")
		}
	}()
	(&Ref{UUID: "x", Name: "Тест"}).CallMethod("чегоизволите", nil)
}

// Удалить() на ссылке без менеджера — понятная ошибка, а не паника nil.
func TestRef_DeleteWithoutManager(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Error("ожидалась ошибка: ссылка без менеджера")
		}
	}()
	(&Ref{UUID: "x", Name: "Тест"}).CallMethod("удалить", nil)
}

// ─── ПроверитьСовпадениеПоРеквизиту (safe-match 0/1/несколько) ───────────────

// matchEnv готовит окружение с заданными совпадениями по реквизиту ИНН.
func matchEnv(t *testing.T, hits []struct{ ID, Display string }) *CatalogProxy {
	t.Helper()
	root, db, _ := newCatalogsTestEnv()
	db.matchRows = map[string]map[string][]struct{ ID, Display string }{
		"ТипЦен/ИНН": {"7701234567": hits},
	}
	return root.Get("ТипЦен").(*CatalogProxy)
}

func matchResult(t *testing.T, cp *CatalogProxy) *Struct {
	t.Helper()
	v := cp.CallMethod("проверитьсовпадениепореквизиту", []any{"ИНН", "7701234567"})
	s, ok := v.(*Struct)
	if !ok {
		t.Fatalf("ожидалась *Struct, получили %T", v)
	}
	return s
}

func TestMatchByAttribute_NotFound(t *testing.T) {
	s := matchResult(t, matchEnv(t, nil))
	if got := s.Get("Статус"); got != MatchStatusNone {
		t.Errorf("Статус = %v, want %q", got, MatchStatusNone)
	}
	if got := s.Get("Количество"); got != float64(0) {
		t.Errorf("Количество = %v, want 0", got)
	}
	if got := s.Get("Ссылка"); got != nil {
		t.Errorf("Ссылка = %v, want nil", got)
	}
}

func TestMatchByAttribute_One(t *testing.T) {
	cp := matchEnv(t, []struct{ ID, Display string }{
		{ID: "22222222-2222-2222-2222-222222222222", Display: "ООО Ромашка"},
	})
	s := matchResult(t, cp)
	if got := s.Get("Статус"); got != MatchStatusOne {
		t.Errorf("Статус = %v, want %q", got, MatchStatusOne)
	}
	if got := s.Get("Количество"); got != float64(1) {
		t.Errorf("Количество = %v, want 1", got)
	}
	ref, ok := s.Get("Ссылка").(*Ref)
	if !ok {
		t.Fatalf("Ссылка = %T, want *Ref", s.Get("Ссылка"))
	}
	if ref.UUID != "22222222-2222-2222-2222-222222222222" {
		t.Errorf("Ссылка.UUID = %q", ref.UUID)
	}
	// Ссылка несёт менеджера → ПолучитьОбъект() работает для обновления записи.
	if ref.Manager == nil {
		t.Error("Ссылка без менеджера — ПолучитьОбъект() не сработает")
	}
}

func TestMatchByAttribute_Multiple(t *testing.T) {
	cp := matchEnv(t, []struct{ ID, Display string }{
		{ID: "aaaa", Display: "Дубль 1"},
		{ID: "bbbb", Display: "Дубль 2"},
		{ID: "cccc", Display: "Дубль 3"},
	})
	s := matchResult(t, cp)
	if got := s.Get("Статус"); got != MatchStatusMultiple {
		t.Errorf("Статус = %v, want %q", got, MatchStatusMultiple)
	}
	if got := s.Get("Количество"); got != float64(3) {
		t.Errorf("Количество = %v, want 3 (точное число дублей)", got)
	}
	if got := s.Get("Ссылка"); got != nil {
		t.Errorf("Ссылка = %v, want nil при неоднозначности", got)
	}
}

// Без значения — понятная ошибка, а не тихий nil.
func TestMatchByAttribute_MissingArg(t *testing.T) {
	root, _, _ := newCatalogsTestEnv()
	cp := root.Get("ТипЦен").(*CatalogProxy)
	defer func() {
		if recover() == nil {
			t.Error("ожидалась ошибка: не передано значение реквизита")
		}
	}()
	cp.CallMethod("проверитьсовпадениепореквизиту", []any{"ИНН"})
}
