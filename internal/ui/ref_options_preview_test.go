package ui

import (
	"context"
	"encoding/json"
	"net/http"
	"path/filepath"
	"testing"

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

// Реквизит просмотра типа richtext показывается с оформлением, но разметка
// приходит уже вычищенной: значения лежат в базе, и положить их туда мог кто

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
	// План 168, инвариант 5: preview всегда обычный текст — признака разметки
	// в ответе не существует вовсе, клиент рисует textContent.
	if resp.PreviewHTML {
		t.Error("ответ объявляет preview размеченным — контракта previewHtml больше нет")
	}
	if got, _ := resp.Items[0]["Информация"].(string); got != "НЕ ВЫПОЛНЯЕМ: <промышленные> машины" {
		t.Errorf("текст изменён: %q", got)
	}
}

// Тексты, собранные процедурой, показываются с оформлением, если объявленный
// choice_preview — richtext: процедура поставляет значения из того же реквизита,
