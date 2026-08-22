package ui

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/ivantit66/onebase/internal/dsl/interpreter"
	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/ivantit66/onebase/internal/runtime"
	"github.com/ivantit66/onebase/internal/storage"
)

// newConstantsServer поднимает SQLite-Server с константами разных типов
// (enum required, reference, обычная строка) и одной записью справочника
// «Организации» для проверки ссылочной валидации. Возвращает сервер, ctx и id
// существующей организации.
func newConstantsServer(t *testing.T) (*Server, context.Context, uuid.UUID) {
	t.Helper()
	ctx := context.Background()
	db, err := storage.ConnectSQLite(ctx, filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })

	org := &metadata.Entity{
		Name:   "Организации",
		Kind:   metadata.KindCatalog,
		Fields: []metadata.Field{{Name: "Наименование", Type: metadata.FieldTypeString}},
	}
	consts := []*metadata.Constant{
		{Name: "ТипКооператива", Type: "enum:ТипыКооперативов", EnumName: "ТипыКооперативов", Required: true, Label: "Тип кооператива"},
		{Name: "ГоловнаяОрганизация", Type: "reference:Организации", RefEntity: "Организации", Label: "Головная организация"},
		{Name: "Комментарий", Type: metadata.FieldTypeString, Label: "Комментарий"},
	}
	if err := db.Migrate(ctx, []*metadata.Entity{org}); err != nil {
		t.Fatal(err)
	}
	if err := db.MigrateConstants(ctx, consts); err != nil {
		t.Fatal(err)
	}

	reg := runtime.NewRegistry()
	reg.Load(runtime.LoadOptions{
		Entities:  []*metadata.Entity{org},
		Enums:     []*metadata.Enum{{Name: "ТипыКооперативов", Values: []string{"СТ", "ГК"}}},
		Constants: consts,
	})
	s := &Server{
		store:    db,
		reg:      reg,
		interp:   interpreter.New(),
		lockMgr:  runtime.NewLockManager(),
		messages: NewMessageStore(),
		cfg:      Config{AppName: "test"},
	}
	s.entitySvc = s.newEntityService(nil)

	orgID := uuid.New()
	if err := db.Upsert(ctx, org.Name, orgID, map[string]any{"Наименование": "Головная"}, org); err != nil {
		t.Fatalf("Upsert(Организации): %v", err)
	}
	return s, ctx, orgID
}

func postConstants(t *testing.T, s *Server, form url.Values) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/ui/constants", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec := httptest.NewRecorder()
	s.constantsSave(rec, req)
	return rec
}

func mustNotExist(t *testing.T, s *Server, ctx context.Context, name string) {
	t.Helper()
	if v, err := s.store.GetConstant(ctx, name); err == nil {
		t.Fatalf("константа %s не должна была сохраниться, получили %v", name, v)
	}
}

func TestConstantsSave_RejectsInvalidEnum(t *testing.T) {
	s, ctx, _ := newConstantsServer(t)
	rec := postConstants(t, s, url.Values{"ТипКооператива": {"МУСОР"}})

	if rec.Code != http.StatusOK {
		t.Fatalf("код ответа = %d, ждали 200 (форма с ошибкой, не редирект)", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "недопустимое значение") {
		t.Error("в ответе нет сообщения об ошибке домена enum")
	}
	mustNotExist(t, s, ctx, "ТипКооператива")
}

func TestConstantsSave_RejectsEmptyRequired(t *testing.T) {
	s, ctx, _ := newConstantsServer(t)
	rec := postConstants(t, s, url.Values{"ТипКооператива": {""}})

	if rec.Code != http.StatusOK {
		t.Fatalf("код ответа = %d, ждали 200", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "обязательна для заполнения") {
		t.Error("в ответе нет сообщения об обязательности")
	}
	mustNotExist(t, s, ctx, "ТипКооператива")
}

func TestConstantsSave_RejectsMissingReference(t *testing.T) {
	s, ctx, _ := newConstantsServer(t)
	rec := postConstants(t, s, url.Values{
		"ТипКооператива":      {"СТ"},
		"ГоловнаяОрганизация": {uuid.New().String()}, // валидный UUID, но записи нет
	})

	if rec.Code != http.StatusOK {
		t.Fatalf("код ответа = %d, ждали 200", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "не найден") {
		t.Error("в ответе нет сообщения о ненайденной ссылке")
	}
	// Ни одна константа не должна сохраниться — валидация до записи.
	mustNotExist(t, s, ctx, "ТипКооператива")
	mustNotExist(t, s, ctx, "ГоловнаяОрганизация")
}

func TestConstantsSave_AcceptsValidValues(t *testing.T) {
	s, ctx, orgID := newConstantsServer(t)
	rec := postConstants(t, s, url.Values{
		"ТипКооператива":      {"СТ"},
		"ГоловнаяОрганизация": {orgID.String()},
		"Комментарий":         {"  проверка  "}, // пробелы обрезаются
	})

	if rec.Code != http.StatusSeeOther {
		t.Fatalf("код ответа = %d, ждали 303 (успех)", rec.Code)
	}
	if got, _ := s.store.GetConstant(ctx, "ТипКооператива"); got != "СТ" {
		t.Errorf("ТипКооператива = %v, ждали СТ", got)
	}
	if got, _ := s.store.GetConstant(ctx, "ГоловнаяОрганизация"); got != orgID.String() {
		t.Errorf("ГоловнаяОрганизация = %v, ждали %s", got, orgID)
	}
	if got, _ := s.store.GetConstant(ctx, "Комментарий"); got != "проверка" {
		t.Errorf("Комментарий = %v, ждали \"проверка\" (обрезанные пробелы)", got)
	}
}
