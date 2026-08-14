package ui

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/xuri/excelize/v2"
)

// Выгрузка журнала в Excel объявляла колонки «Дата» и «Вид» и заполняла их из
// ключей `date`/`doc_type`, которых в строках журнала нет: JournalQuery отдаёт
// `_doc_kind`, `id` и поля колонок журнала. Пользователь получал файл с двумя
// мёртвыми колонками (#886).
//
// Проверка идёт через публичный вход `/ui/journal/{name}/excel` и читает
// получившуюся книгу: сверять внутренние ключи между собой бессмысленно —
// именно расхождение ключа с фактической строкой и было дефектом.
func TestJournalExcel_ЛевыеКолонкиНеПустые(t *testing.T) {
	doc := &metadata.Entity{
		Name: "РасходныйОрдер",
		Kind: metadata.KindDocument,
		Fields: []metadata.Field{
			{Name: "Дата", Type: metadata.FieldTypeDate},
			{Name: "Номер", Type: metadata.FieldTypeString},
			{Name: "Сумма", Type: metadata.FieldTypeNumber},
		},
	}
	j := &metadata.Journal{
		Name:      "ЖурналОрдеров",
		Documents: []string{doc.Name},
		Columns: []metadata.JournalColumn{
			{Field: "Дата", Label: "Дата"},
			{Field: "Номер", Label: "Номер"},
			{Field: "Сумма", Label: "Сумма"},
		},
	}

	s, ctx := newSubmitTestServer(t, []*metadata.Entity{doc})
	s.reg.LoadJournals([]*metadata.Journal{j})
	if err := s.store.Upsert(ctx, doc.Name, uuid.New(), map[string]any{
		"Дата":  time.Date(2026, 8, 14, 0, 0, 0, 0, time.UTC),
		"Номер": "РО-000042",
		"Сумма": 150.5,
	}, doc); err != nil {
		t.Fatal(err)
	}

	req := reqWithChi(http.MethodGet, "/ui/journal/"+j.Name+"/excel", nil, map[string]string{"name": j.Name})
	w := httptest.NewRecorder()
	s.journalExcel(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("journalExcel: %d %s", w.Code, w.Body.String())
	}

	book, err := excelize.OpenReader(bytes.NewReader(w.Body.Bytes()))
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := book.Close(); err != nil {
			t.Errorf("закрытие книги: %v", err)
		}
	}()
	sheets := book.GetSheetList()
	if len(sheets) == 0 {
		t.Fatal("в книге нет листов")
	}
	rows, err := book.GetRows(sheets[0])
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) < 2 {
		t.Fatalf("в книге нет строк данных: %#v", rows)
	}

	// Шапка повторяет таблицу на экране: вид документа + видимые колонки.
	wantHeader := []string{"Документ", "Дата", "Номер", "Сумма"}
	if len(rows[0]) != len(wantHeader) {
		t.Fatalf("шапка %#v, ожидалась %#v", rows[0], wantHeader)
	}
	for i, want := range wantHeader {
		if rows[0][i] != want {
			t.Errorf("колонка %d: %q, ожидалось %q", i, rows[0][i], want)
		}
	}

	// Главное: ни одна объявленная колонка не приезжает пустой.
	data := rows[1]
	for i, head := range wantHeader {
		if i >= len(data) || data[i] == "" {
			t.Errorf("колонка «%s» пуста — объявлена в шапке, но не заполнена", head)
		}
	}
	if len(data) > 0 && data[0] != doc.Name {
		t.Errorf("вид документа %q, ожидался %q", data[0], doc.Name)
	}
	if len(data) > 2 && data[2] != "РО-000042" {
		t.Errorf("номер %q, ожидался «РО-000042»", data[2])
	}
}
