package ui

// Разворот ссылок после перевода с N+1 GetByID на батч GetFieldsByIDs
// (план 111, P1-1): корректность для плоских списков (resolveRefs), колонок
// регистра с известным и неизвестным типом ссылки (resolveRefColumns), а также
// разбиения на батчи при числе id больше refLabelBatchSize.

import (
	"testing"

	"github.com/google/uuid"
	"github.com/ivantit66/onebase/internal/metadata"
)

func refTestCatalog() *metadata.Entity {
	return &metadata.Entity{
		Name: "Контрагент",
		Kind: metadata.KindCatalog,
		Fields: []metadata.Field{
			{Name: "Наименование", Type: metadata.FieldTypeString},
		},
	}
}

func TestResolveRefs_Batched(t *testing.T) {
	client := refTestCatalog()
	doc := &metadata.Entity{
		Name: "Заказ",
		Kind: metadata.KindDocument,
		Fields: []metadata.Field{
			{Name: "Контрагент", Type: "reference:Контрагент", RefEntity: client.Name},
		},
	}
	s, ctx := newSubmitTestServer(t, []*metadata.Entity{client, doc})

	id1, id2 := uuid.New(), uuid.New()
	if err := s.store.Upsert(ctx, client.Name, id1, map[string]any{"Наименование": "Ромашка"}, client); err != nil {
		t.Fatal(err)
	}
	if err := s.store.Upsert(ctx, client.Name, id2, map[string]any{"Наименование": "Лютик"}, client); err != nil {
		t.Fatal(err)
	}

	rows := []map[string]any{
		{"Контрагент": id1.String()},
		{"Контрагент": id2.String()},
		{"Контрагент": id1.String()}, // дубль того же уникального id
		{"Контрагент": nil},          // пустая ссылка не должна ломать разворот
	}
	s.resolveRefs(ctx, doc, rows)

	if rows[0]["Контрагент"] != "Ромашка" {
		t.Errorf("row0 = %v, want Ромашка", rows[0]["Контрагент"])
	}
	if rows[1]["Контрагент"] != "Лютик" {
		t.Errorf("row1 = %v, want Лютик", rows[1]["Контрагент"])
	}
	if rows[2]["Контрагент"] != "Ромашка" {
		t.Errorf("row2 (дубль) = %v, want Ромашка", rows[2]["Контрагент"])
	}
}

func TestResolveRefs_PresentationFallbackLoadsEveryCandidate(t *testing.T) {
	client := &metadata.Entity{
		Name: "Номенклатура", Kind: metadata.KindCatalog,
		Fields: []metadata.Field{
			{Name: "Артикул", Type: metadata.FieldTypeString},
			{Name: "Наименование", Type: metadata.FieldTypeString},
		},
		Presentation: []string{"Артикул", "Наименование"},
	}
	doc := &metadata.Entity{
		Name: "Заказ", Kind: metadata.KindDocument,
		Fields: []metadata.Field{{Name: "Товар", Type: "reference:Номенклатура", RefEntity: client.Name}},
	}
	s, ctx := newSubmitTestServer(t, []*metadata.Entity{client, doc})
	id := uuid.New()
	if err := s.store.Upsert(ctx, client.Name, id, map[string]any{
		"Артикул": "", "Наименование": "Стул",
	}, client); err != nil {
		t.Fatal(err)
	}
	rows := []map[string]any{{"Товар": id.String()}}
	s.resolveRefs(ctx, doc, rows)
	if got := rows[0]["Товар"]; got != "Стул" {
		t.Fatalf("fallback presentation = %v, ожидалось Стул", got)
	}
}

func TestResolveRefColumns_KnownAndUnknownType(t *testing.T) {
	client := refTestCatalog()
	s, ctx := newSubmitTestServer(t, []*metadata.Entity{client})

	id := uuid.New()
	if err := s.store.Upsert(ctx, client.Name, id, map[string]any{"Наименование": "Ромашка"}, client); err != nil {
		t.Fatal(err)
	}

	// Известный тип → адресный батч, замена in-place.
	rowsKnown := []map[string]any{{"Контрагент": id.String()}}
	s.resolveRefColumns(ctx, rowsKnown, []refCol{{Key: "Контрагент", RefEntity: client.Name}}, "")
	if rowsKnown[0]["Контрагент"] != "Ромашка" {
		t.Errorf("known: %v, want Ромашка", rowsKnown[0]["Контрагент"])
	}

	// Неизвестный тип ("") → перебор сущностей всё равно находит запись; подпись
	// пишется в колонку с суффиксом, исходный UUID сохраняется.
	rowsUnknown := []map[string]any{{"Субконто": id.String()}}
	s.resolveRefColumns(ctx, rowsUnknown, []refCol{{Key: "Субконто", RefEntity: ""}}, "_label")
	if rowsUnknown[0]["Субконто_label"] != "Ромашка" {
		t.Errorf("unknown: %v, want Ромашка", rowsUnknown[0]["Субконто_label"])
	}
	if rowsUnknown[0]["Субконто"] != id.String() {
		t.Errorf("unknown: исходный UUID должен сохраниться, got %v", rowsUnknown[0]["Субконто"])
	}
}

// Число уникальных ссылок больше refLabelBatchSize → GetFieldsByIDs зовётся
// несколькими батчами; все подписи должны развернуться (граница батча и защита
// от лимита параметров SQLite на широком экспорте).
func TestResolveRefs_ChunksBeyondBatchSize(t *testing.T) {
	client := refTestCatalog()
	doc := &metadata.Entity{
		Name: "Заказ",
		Kind: metadata.KindDocument,
		Fields: []metadata.Field{
			{Name: "Контрагент", Type: "reference:Контрагент", RefEntity: client.Name},
		},
	}
	s, ctx := newSubmitTestServer(t, []*metadata.Entity{client, doc})

	n := refLabelBatchSize + 5
	rows := make([]map[string]any, 0, n)
	want := make(map[string]string, n)
	for i := 0; i < n; i++ {
		id := uuid.New()
		name := "К" + uuid.NewString()[:8]
		if err := s.store.Upsert(ctx, client.Name, id, map[string]any{"Наименование": name}, client); err != nil {
			t.Fatal(err)
		}
		rows = append(rows, map[string]any{"Контрагент": id.String()})
		want[id.String()] = name
	}
	// снимок ожидаемого по позициям до мутации строк
	expect := make([]string, n)
	for i, r := range rows {
		expect[i] = want[r["Контрагент"].(string)]
	}

	s.resolveRefs(ctx, doc, rows)

	for i, r := range rows {
		if r["Контрагент"] != expect[i] {
			t.Fatalf("row %d = %v, want %s (батч не развернул все id)", i, r["Контрагент"], expect[i])
		}
	}
}
