package ui

import (
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/ivantit66/onebase/internal/auth"
	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/ivantit66/onebase/internal/runtime"
	"github.com/ivantit66/onebase/internal/storage"
	"github.com/ivantit66/onebase/internal/widget"
)

func TestAsString(t *testing.T) {
	if asString("привет") != "привет" {
		t.Error("string не прошёл")
	}
	if asString([]byte("байты")) != "байты" {
		t.Error("[]byte не сконвертировался")
	}
	if asString(nil) != "" {
		t.Error("nil → не пустая строка")
	}
	if asString(42) != "" {
		t.Error("число → должна быть пустая строка")
	}
}

// resolveRegisterRows: UUID в измерении (reference) и атрибуте → имя,
// причём целевым поиском по RefEntity, и поддержка []byte от SQLite-драйвера.
func TestResolveRegisterRows_RefAndBytes(t *testing.T) {
	ctx := context.Background()
	db, err := storage.ConnectSQLite(ctx, filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	nom := &metadata.Entity{
		Name:   "Номенклатура",
		Kind:   metadata.KindCatalog,
		Fields: []metadata.Field{{Name: "Наименование", Type: metadata.FieldTypeString}},
	}
	org := &metadata.Entity{
		Name:   "Организация",
		Kind:   metadata.KindCatalog,
		Fields: []metadata.Field{{Name: "Наименование", Type: metadata.FieldTypeString}},
	}
	if err := db.Migrate(ctx, []*metadata.Entity{nom, org}); err != nil {
		t.Fatal(err)
	}
	nomID := uuid.New()
	orgID := uuid.New()
	if err := db.Upsert(ctx, "Номенклатура", nomID, map[string]any{"Наименование": "Тумбочка"}, nom); err != nil {
		t.Fatal(err)
	}
	if err := db.Upsert(ctx, "Организация", orgID, map[string]any{"Наименование": "ООО Ромашка"}, org); err != nil {
		t.Fatal(err)
	}

	registry := runtime.NewRegistry()
	registry.Load(runtime.LoadOptions{Entities: []*metadata.Entity{nom, org}})
	s := &Server{store: db, reg: registry}

	reg := &metadata.Register{
		Name: "ОстаткиТоваров",
		Dimensions: []metadata.Field{
			{Name: "Номенклатура", Type: "reference:Номенклатура", RefEntity: "Номенклатура"},
		},
		Attributes: []metadata.Field{
			{Name: "Организация", Type: "reference:Организация", RefEntity: "Организация"},
		},
	}

	rows := []map[string]any{
		// измерение — строка-UUID, атрибут — []byte-UUID (как может вернуть SQLite)
		{"Номенклатура": nomID.String(), "Организация": []byte(orgID.String())},
	}
	s.resolveRegisterRows(ctx, rows, reg)

	if rows[0]["Номенклатура"] != "Тумбочка" {
		t.Errorf("измерение не резолвнулось: %v", rows[0]["Номенклатура"])
	}
	if rows[0]["Организация"] != "ООО Ромашка" {
		t.Errorf("атрибут ([]byte UUID) не резолвнулся: %v", rows[0]["Организация"])
	}
}

// Legacy string-измерение, хранящее UUID без RefEntity, тоже резолвится
// (через скан всех сущностей как fallback).
func TestResolveRegisterRows_LegacyStringUUID(t *testing.T) {
	ctx := context.Background()
	db, err := storage.ConnectSQLite(ctx, filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	skl := &metadata.Entity{
		Name:   "Склад",
		Kind:   metadata.KindCatalog,
		Fields: []metadata.Field{{Name: "Наименование", Type: metadata.FieldTypeString}},
	}
	if err := db.Migrate(ctx, []*metadata.Entity{skl}); err != nil {
		t.Fatal(err)
	}
	sklID := uuid.New()
	if err := db.Upsert(ctx, "Склад", sklID, map[string]any{"Наименование": "Основной"}, skl); err != nil {
		t.Fatal(err)
	}
	registry := runtime.NewRegistry()
	registry.Load(runtime.LoadOptions{Entities: []*metadata.Entity{skl}})
	s := &Server{store: db, reg: registry}

	reg := &metadata.Register{
		Name: "ОстаткиТоваров",
		// Склад как string (workaround П.17), но хранит UUID
		Dimensions: []metadata.Field{{Name: "Склад", Type: metadata.FieldTypeString}},
	}
	rows := []map[string]any{{"Склад": sklID.String()}}
	s.resolveRegisterRows(ctx, rows, reg)

	if rows[0]["Склад"] != "Основной" {
		t.Errorf("legacy string-UUID не резолвнулся: %v", rows[0]["Склад"])
	}
}

// Представление регистратора в списке движений собиралось из НЕзамаскированных
// Номер/Дата документа: роль с маской на «Номер» видела в движениях реальный
// номер (issue #649). Тест идёт через HTTP-обработчик registerMovements —
// приватный resolveRegisterRows не зовём.
func TestRegisterMovements_RecorderLabelIsMasked(t *testing.T) {
	ctx := context.Background()
	db, err := storage.ConnectSQLite(ctx, filepath.Join(t.TempDir(), "reg.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })

	doc := &metadata.Entity{
		Name:    "Заявка",
		Kind:    metadata.KindDocument,
		Posting: true,
		Fields: []metadata.Field{
			{Name: "Номер", Type: metadata.FieldTypeString},
		},
	}
	reg := &metadata.Register{
		Name:       "Продажи",
		Dimensions: []metadata.Field{{Name: "Склад", Type: metadata.FieldTypeString}},
		Resources:  []metadata.Field{{Name: "Количество", Type: metadata.FieldTypeNumber}},
	}
	if err := db.Migrate(ctx, []*metadata.Entity{doc}); err != nil {
		t.Fatal(err)
	}
	if err := db.MigrateRegisters(ctx, []*metadata.Register{reg}); err != nil {
		t.Fatal(err)
	}
	registry := runtime.NewRegistry()
	registry.Load(runtime.LoadOptions{Entities: []*metadata.Entity{doc}, Registers: []*metadata.Register{reg}})

	docID := uuid.New()
	if err := db.Upsert(ctx, doc.Name, docID, map[string]any{"Номер": "СЕКРЕТ-0001"}, doc); err != nil {
		t.Fatal(err)
	}
	if err := db.WriteMovements(ctx, reg.Name, doc.Name, docID,
		[]map[string]any{{"Склад": "Основной", "Количество": 1}}, reg, nil); err != nil {
		t.Fatal(err)
	}

	s := &Server{
		store:       db,
		reg:         registry,
		messages:    NewMessageStore(),
		widgetCache: widget.NewCache(60 * time.Second),
	}
	user := &auth.User{
		ID: "op", Login: "operator",
		Roles: []*auth.Role{{
			Name: "Оператор",
			Permissions: auth.Permission{
				Documents: map[string][]string{"Заявка": {"read"}},
				Registers: map[string][]string{"Продажи": {"read"}},
				FieldAccess: auth.FieldAccess{
					Documents: map[string]auth.FieldPolicies{
						"Заявка": {"Номер": {Read: "mask_tail", Keep: 2}},
					},
				},
			},
		}},
	}
	req := httptest.NewRequest(http.MethodGet, "/ui/register/Продажи", nil)
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("name", "Продажи")
	req = req.WithContext(context.WithValue(auth.ContextWithUser(ctx, user), chi.RouteCtxKey, rctx))
	rec := httptest.NewRecorder()
	s.registerMovements(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("статус = %d, тело: %s", rec.Code, rec.Body.String())
	}
	if strings.Contains(rec.Body.String(), "СЕКРЕТ-0001") {
		t.Errorf("реальный номер регистратора утёк в список движений")
	}
}
