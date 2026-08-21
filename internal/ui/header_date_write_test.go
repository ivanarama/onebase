package ui

import (
	"context"
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/ivantit66/onebase/internal/dbtest"
	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/ivantit66/onebase/internal/storage"
)

// Регрессия #1085 идёт публичным путём POST формы и одной матрицей держит
// SQLite/PostgreSQL. До фикса SQLite подтверждал запись произвольного текста в
// колонку даты ответом 303, а PostgreSQL падал с 500.
func TestHeaderDateWrite_ПриводитсяКТипуДоЗаписи_1085(t *testing.T) {
	dbtest.ForEachDialect(t, func(t *testing.T, db *storage.DB) {
		entity := &metadata.Entity{
			Name: "ДатаШапки" + strings.ReplaceAll(uuid.NewString()[:8], "-", ""),
			Kind: metadata.KindCatalog,
			Fields: []metadata.Field{
				{Name: "Наименование", Type: metadata.FieldTypeString},
				{Name: "ДатаПоставки", Type: metadata.FieldTypeDate},
			},
		}
		ts := tpw1074Server(t, db, []*metadata.Entity{entity})
		client := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		}}
		post := func(value string) (int, string) {
			t.Helper()
			resp, err := client.PostForm(ts.URL+"/ui/catalog/"+url.PathEscape(entity.Name)+"/new", url.Values{
				"Наименование": {"Проба"},
				"ДатаПоставки": {value},
			}) //nolint:noctx // локальный httptest.Server
			if err != nil {
				t.Fatalf("POST формы: %v", err)
			}
			defer resp.Body.Close() //nolint:errcheck // тест читает тело сразу
			body, _ := io.ReadAll(resp.Body)
			return resp.StatusCode, string(body)
		}

		status, body := post("не дата")
		if status != http.StatusBadRequest {
			t.Fatalf("неверная дата: статус=%d, ожидался 400; body=%s", status, body)
		}
		if !strings.Contains(body, "не дата") || !strings.Contains(body, "<form") {
			t.Fatalf("ошибка показана не в форме: %s", body)
		}
		rows, err := db.List(context.Background(), entity.Name, entity, storage.ListParams{})
		if err != nil {
			t.Fatal(err)
		}
		if len(rows) != 0 {
			t.Fatalf("неверная дата попала в базу: %#v", rows)
		}

		status, body = post("14.03.1985")
		if status != http.StatusSeeOther {
			t.Fatalf("допустимая дата: статус=%d, ожидался 303; body=%s", status, body)
		}
		rows, err = db.List(context.Background(), entity.Name, entity, storage.ListParams{})
		if err != nil {
			t.Fatal(err)
		}
		if len(rows) != 1 {
			t.Fatalf("записей=%d, ожидалась одна", len(rows))
		}
		if got := tpw1074Day(t, rows[0]["ДатаПоставки"]); got != "1985-03-14" {
			t.Fatalf("ДатаПоставки=%v (%s), ожидался день 1985-03-14", rows[0]["ДатаПоставки"], got)
		}
	})
}
