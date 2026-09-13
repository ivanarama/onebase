package ui

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ivantit66/onebase/internal/dsl/ast"
	"github.com/ivantit66/onebase/internal/dsl/interpreter"
	"github.com/ivantit66/onebase/internal/dsl/lexer"
	"github.com/ivantit66/onebase/internal/dsl/parser"

	"github.com/google/uuid"
	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/ivantit66/onebase/internal/runtime"
	"github.com/ivantit66/onebase/internal/storage"
)

// Форма выбора показывает текст реквизита choice_preview для строки под
// курсором. Имя реквизита едет вместе со строками: значения уже в items и уже
// прошли права, строковые политики и маску ПДн — второй путь чтения пришлось бы
// проводить через все три гейта заново.
func TestRefOptionsCarriesChoicePreviewField(t *testing.T) {
	ctx := context.Background()
	db, err := storage.ConnectSQLite(ctx, filepath.Join(t.TempDir(), "preview.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	ent := &metadata.Entity{
		Name: "НаправлениеОбслуживания", Kind: metadata.KindCatalog,
		Fields: []metadata.Field{
			{Name: "Наименование", Type: metadata.FieldTypeString},
			{Name: "Информация", Type: metadata.FieldTypeString},
		},
		ChoicePreview: "Информация",
	}
	if err := db.Migrate(ctx, []*metadata.Entity{ent}); err != nil {
		t.Fatal(err)
	}
	if err := db.Upsert(ctx, ent.Name, uuid.New(), map[string]any{
		"наименование": "Ремонт бытовой техники",
		"информация":   "НЕ ВЫПОЛНЯЕМ: промышленные машины",
	}, ent); err != nil {
		t.Fatal(err)
	}
	reg := runtime.NewRegistry()
	reg.Load(runtime.LoadOptions{Entities: []*metadata.Entity{ent}})
	s := &Server{reg: reg, store: db}

	rec := serveRefOptions(t, s, ent.Name, "limit=10", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("code = %d, body = %s", rec.Code, rec.Body.String())
	}
	var resp struct {
		Preview string           `json:"preview"`
		Items   []map[string]any `json:"items"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if resp.Preview != "Информация" {
		t.Errorf("preview = %q, want «Информация»", resp.Preview)
	}
	if len(resp.Items) != 1 {
		t.Fatalf("строк = %d, ожидалась одна: %s", len(resp.Items), rec.Body.String())
	}
	if got := resp.Items[0]["Информация"]; got != "НЕ ВЫПОЛНЯЕМ: промышленные машины" {
		t.Errorf("текст просмотра не доехал до формы выбора: %v", got)
	}
}

// Имена реквизитов метаданных регистронезависимы. API должен вернуть клиенту
// каноническое имя поля: строки результата используют именно его как ключ.
func TestRefOptionsCanonicalizesChoicePreviewField(t *testing.T) {
	ctx := context.Background()
	db, err := storage.ConnectSQLite(ctx, filepath.Join(t.TempDir(), "preview-case.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	ent := &metadata.Entity{
		Name: "Направление", Kind: metadata.KindCatalog,
		Fields: []metadata.Field{
			{Name: "Наименование", Type: metadata.FieldTypeString},
			{Name: "Информация", Type: metadata.FieldTypeString},
		},
		ChoicePreview: "информация",
	}
	if err := db.Migrate(ctx, []*metadata.Entity{ent}); err != nil {
		t.Fatal(err)
	}
	if err := db.Upsert(ctx, ent.Name, uuid.New(), map[string]any{
		"наименование": "Ремонт", "информация": "Канонический текст",
	}, ent); err != nil {
		t.Fatal(err)
	}
	reg := runtime.NewRegistry()
	reg.Load(runtime.LoadOptions{Entities: []*metadata.Entity{ent}})
	s := &Server{reg: reg, store: db}

	rec := serveRefOptions(t, s, ent.Name, "limit=10", nil)
	var resp struct {
		Preview string           `json:"preview"`
		Items   []map[string]any `json:"items"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if resp.Preview != "Информация" {
		t.Fatalf("preview = %q, want canonical field name Информация", resp.Preview)
	}
	if got := resp.Items[0][resp.Preview]; got != "Канонический текст" {
		t.Fatalf("preview value by canonical key = %v", got)
	}
}

// Сущность без choice_preview остаётся как была: области просмотра нет, и
// клиент не должен догадываться, что показывать.
func TestRefOptionsWithoutChoicePreviewSendsEmpty(t *testing.T) {
	ctx := context.Background()
	db, err := storage.ConnectSQLite(ctx, filepath.Join(t.TempDir(), "nopreview.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	ent := &metadata.Entity{
		Name: "Бренд", Kind: metadata.KindCatalog,
		Fields: []metadata.Field{{Name: "Наименование", Type: metadata.FieldTypeString}},
	}
	if err := db.Migrate(ctx, []*metadata.Entity{ent}); err != nil {
		t.Fatal(err)
	}
	reg := runtime.NewRegistry()
	reg.Load(runtime.LoadOptions{Entities: []*metadata.Entity{ent}})
	s := &Server{reg: reg, store: db}

	rec := serveRefOptions(t, s, ent.Name, "limit=10", nil)
	var resp struct {
		Preview string `json:"preview"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if resp.Preview != "" {
		t.Errorf("preview = %q, ожидалась пустая строка", resp.Preview)
	}
}

// Контекст подбора: вызывающая форма присылает, ДЛЯ ЧЕГО выбирают (филиал
// звонка), и текст просмотра собирает процедура конфигурации, а не реквизит
// строки. Без этого памятка в подборе была бы одна на всю сеть, хотя
// ограничения территориальные.
func TestRefOptionsPreviewProcSeesChoiceContext(t *testing.T) {
	ctx := context.Background()
	db, err := storage.ConnectSQLite(ctx, filepath.Join(t.TempDir(), "ctx.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	ent := &metadata.Entity{
		Name: "НаправлениеОбслуживания", Kind: metadata.KindCatalog,
		Fields: []metadata.Field{
			{Name: "Наименование", Type: metadata.FieldTypeString},
			{Name: "Информация", Type: metadata.FieldTypeString},
		},
		ChoicePreview:     "Информация",
		ChoicePreviewProc: "Памятки.ДляПодбора",
	}
	if err := db.Migrate(ctx, []*metadata.Entity{ent}); err != nil {
		t.Fatal(err)
	}
	id := uuid.New()
	if err := db.Upsert(ctx, ent.Name, id, map[string]any{
		"наименование": "Ремонт", "информация": "общая памятка",
	}, ent); err != nil {
		t.Fatal(err)
	}

	src := `
Функция ДляПодбора(Ссылки, Контекст) Экспорт
    Рез = Новый Соответствие;
    Фил = Строка(Контекст.Получить("Филиал"));
    Для Каждого Ид Из Ссылки Цикл
        Рез.Вставить(Строка(Ид), "памятка филиала " + Фил);
    КонецЦикла;
    Возврат Рез;
КонецФункции`
	prog, err := parser.New(lexer.New(src, "памятки.module.os")).ParseProgram()
	if err != nil {
		t.Fatalf("parse module: %v", err)
	}
	reg := runtime.NewRegistry()
	reg.Load(runtime.LoadOptions{Entities: []*metadata.Entity{ent}})
	reg.LoadModules(map[string]*ast.Program{"Памятки": prog})
	interp := interpreter.New()
	interp.LookupProc = reg.GetModuleProc
	interp.LookupModuleProc = reg.GetModuleNamespacedProc
	s := &Server{reg: reg, store: db, interp: interp}

	ctxParam := url.QueryEscape(`{"Филиал":"МСК"}`)
	rec := serveRefOptions(t, s, ent.Name, "limit=10&ctx="+ctxParam, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("code = %d, body = %s", rec.Code, rec.Body.String())
	}
	var resp struct {
		Preview string           `json:"preview"`
		Items   []map[string]any `json:"items"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if resp.Preview != "_preview" {
		t.Fatalf("preview = %q, ожидался служебный ключ _preview: %s", resp.Preview, rec.Body.String())
	}
	if len(resp.Items) != 1 {
		t.Fatalf("строк = %d: %s", len(resp.Items), rec.Body.String())
	}
	if got := resp.Items[0]["_preview"]; got != "памятка филиала МСК" {
		t.Errorf("текст просмотра = %v, ожидался собранный процедурой с учётом контекста", got)
	}
}

// Реквизит просмотра типа richtext показывается с оформлением, но разметка
// приходит уже вычищенной: значения лежат в базе, и положить их туда мог кто
// угодно с правом записи в справочник.
func TestRefOptionsRichTextPreviewIsSanitized(t *testing.T) {
	ctx := context.Background()
	db, err := storage.ConnectSQLite(ctx, filepath.Join(t.TempDir(), "rich.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	ent := &metadata.Entity{
		Name: "Тариф", Kind: metadata.KindCatalog,
		Fields: []metadata.Field{
			{Name: "Наименование", Type: metadata.FieldTypeString},
			{Name: "Условия", Type: metadata.FieldTypeRichText},
		},
		ChoicePreview: "Условия",
	}
	if err := db.Migrate(ctx, []*metadata.Entity{ent}); err != nil {
		t.Fatal(err)
	}
	if err := db.Upsert(ctx, ent.Name, uuid.New(), map[string]any{
		"наименование": "Базовый",
		"условия":      `<p><b>Выезд платный</b></p><script>alert(1)</script>`,
	}, ent); err != nil {
		t.Fatal(err)
	}
	reg := runtime.NewRegistry()
	reg.Load(runtime.LoadOptions{Entities: []*metadata.Entity{ent}})
	s := &Server{reg: reg, store: db}

	rec := serveRefOptions(t, s, ent.Name, "limit=10", nil)
	var resp struct {
		Preview     string           `json:"preview"`
		PreviewHTML bool             `json:"previewHtml"`
		Items       []map[string]any `json:"items"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if !resp.PreviewHTML {
		t.Error("richtext-реквизит просмотра не объявлен размеченным")
	}
	got, _ := resp.Items[0]["Условия"].(string)
	if !strings.Contains(got, "<b>Выезд платный</b>") {
		t.Errorf("оформление потеряно: %q", got)
	}
	if strings.Contains(got, "<script") {
		t.Errorf("скрипт не вычищен: %q", got)
	}
}

// Обычный строковый реквизит просмотра размеченным не объявляется: иначе текст
// с угловой скобкой браузер принял бы за разметку.
func TestRefOptionsPlainPreviewIsNotHTML(t *testing.T) {
	ctx := context.Background()
	db, err := storage.ConnectSQLite(ctx, filepath.Join(t.TempDir(), "plain.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	ent := &metadata.Entity{
		Name: "Направление", Kind: metadata.KindCatalog,
		Fields: []metadata.Field{
			{Name: "Наименование", Type: metadata.FieldTypeString},
			{Name: "Информация", Type: metadata.FieldTypeString},
		},
		ChoicePreview: "Информация",
	}
	if err := db.Migrate(ctx, []*metadata.Entity{ent}); err != nil {
		t.Fatal(err)
	}
	if err := db.Upsert(ctx, ent.Name, uuid.New(), map[string]any{
		"наименование": "Ремонт", "информация": "НЕ ВЫПОЛНЯЕМ: <промышленные> машины",
	}, ent); err != nil {
		t.Fatal(err)
	}
	reg := runtime.NewRegistry()
	reg.Load(runtime.LoadOptions{Entities: []*metadata.Entity{ent}})
	s := &Server{reg: reg, store: db}

	rec := serveRefOptions(t, s, ent.Name, "limit=10", nil)
	var resp struct {
		PreviewHTML bool             `json:"previewHtml"`
		Items       []map[string]any `json:"items"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if resp.PreviewHTML {
		t.Error("обычный строковый реквизит объявлен размеченным")
	}
	if got, _ := resp.Items[0]["Информация"].(string); got != "НЕ ВЫПОЛНЯЕМ: <промышленные> машины" {
		t.Errorf("текст изменён: %q", got)
	}
}

// Тексты, собранные процедурой, показываются с оформлением, если объявленный
// choice_preview — richtext: процедура поставляет значения из того же реквизита,
// только выбирая нужное по контексту. И чистятся так же — источник тот же.
func TestRefOptionsPreviewProcInheritsRichTextFormat(t *testing.T) {
	ctx := context.Background()
	db, err := storage.ConnectSQLite(ctx, filepath.Join(t.TempDir(), "procrich.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	ent := &metadata.Entity{
		Name: "Направление", Kind: metadata.KindCatalog,
		Fields: []metadata.Field{
			{Name: "Наименование", Type: metadata.FieldTypeString},
			{Name: "Информация", Type: metadata.FieldTypeRichText},
		},
		ChoicePreview:     "Информация",
		ChoicePreviewProc: "Памятки.ДляПодбора",
	}
	if err := db.Migrate(ctx, []*metadata.Entity{ent}); err != nil {
		t.Fatal(err)
	}
	if err := db.Upsert(ctx, ent.Name, uuid.New(), map[string]any{"наименование": "Ремонт"}, ent); err != nil {
		t.Fatal(err)
	}
	src := `
Функция ДляПодбора(Ссылки, Контекст) Экспорт
    Рез = Новый Соответствие;
    Для Каждого Ид Из Ссылки Цикл
        Рез.Вставить(Строка(Ид), "<p><b>Филиал МСК</b></p><script>alert(1)</script>");
    КонецЦикла;
    Возврат Рез;
КонецФункции`
	prog, err := parser.New(lexer.New(src, "памятки.module.os")).ParseProgram()
	if err != nil {
		t.Fatalf("parse module: %v", err)
	}
	reg := runtime.NewRegistry()
	reg.Load(runtime.LoadOptions{Entities: []*metadata.Entity{ent}})
	reg.LoadModules(map[string]*ast.Program{"Памятки": prog})
	interp := interpreter.New()
	interp.LookupProc = reg.GetModuleProc
	interp.LookupModuleProc = reg.GetModuleNamespacedProc
	s := &Server{reg: reg, store: db, interp: interp}

	rec := serveRefOptions(t, s, ent.Name, "limit=10", nil)
	var resp struct {
		Preview     string           `json:"preview"`
		PreviewHTML bool             `json:"previewHtml"`
		Items       []map[string]any `json:"items"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if !resp.PreviewHTML {
		t.Fatalf("текст процедуры не унаследовал формат richtext: %s", rec.Body.String())
	}
	got, _ := resp.Items[0][choicePreviewKey].(string)
	if !strings.Contains(got, "<b>Филиал МСК</b>") {
		t.Errorf("оформление потеряно: %q", got)
	}
	if strings.Contains(got, "<script") {
		t.Errorf("скрипт не вычищен: %q", got)
	}
}
