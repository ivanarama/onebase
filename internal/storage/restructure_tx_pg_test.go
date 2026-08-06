//go:build integration

package storage

import (
	"context"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/ivantit66/onebase/internal/metadata"
)

// На PostgreSQL смена типа — настоящий ALTER … TYPE … USING, который переписывает
// таблицу под ACCESS EXCLUSIVE. Сбой посреди плана (второй ретайп на
// непреобразуемых данных) обязан откатить уже выполненный первый ретайп — иначе
// колонка сменила бы тип, а карта полей (её пишет saveSchemaMap последней) об
// этом бы не знала (issue #588). Проверяет транзакционность именно PG-пути,
// которого нет в юнит-тестах на SQLite. Требует TEST_DATABASE_URL.
func TestRestructureRollsBackAlterTypeOnPG(t *testing.T) {
	ctx := context.Background()
	db := connectPGForSchemaTest(t)
	resetSchemaTestTable(ctx, t, db)

	before := &metadata.Entity{
		Name: "СхемаТест",
		Kind: metadata.KindCatalog,
		Fields: []metadata.Field{
			{ID: "f_name", Name: "Наименование", Type: metadata.FieldTypeString},
			{ID: "f_a", Name: "СуммаА", Type: metadata.FieldTypeString},
			{ID: "f_b", Name: "СуммаБ", Type: metadata.FieldTypeString},
		},
	}
	if err := db.Migrate(ctx, []*metadata.Entity{before}); err != nil {
		t.Fatal(err)
	}
	id := uuid.New()
	if err := db.Upsert(ctx, before.Name, id, map[string]any{
		"Наименование": "Гвозди", "СуммаА": "100", "СуммаБ": "abc", // «abc» не число
	}, before); err != nil {
		t.Fatal(err)
	}

	// План: ретайп СуммаА string→number (пройдёт, "100" преобразуется) и ретайп
	// СуммаБ string→number (упрётся в «abc»). Оба — Retype, стабильная сортировка
	// сохраняет порядок полей: СуммаА применяется первой.
	after := &metadata.Entity{
		Name: "СхемаТест",
		Kind: metadata.KindCatalog,
		Fields: []metadata.Field{
			{ID: "f_name", Name: "Наименование", Type: metadata.FieldTypeString},
			{ID: "f_a", Name: "СуммаА", Type: metadata.FieldTypeNumber},
			{ID: "f_b", Name: "СуммаБ", Type: metadata.FieldTypeNumber},
		},
	}
	if err := db.Migrate(ctx, []*metadata.Entity{after}); err == nil {
		t.Fatal("ожидалась ошибка: «abc» не преобразуется в число")
	}

	// Первый ретайп (СуммаА → NUMERIC) откатился: колонка осталась текстовой.
	// Без транзакции ALTER TYPE зафиксировался бы, а карта считала бы СуммаА
	// по-прежнему строкой — рассогласование.
	cols, err := db.tableColumns(ctx, metadata.TableName("СхемаТест"))
	if err != nil {
		t.Fatal(err)
	}
	typ := strings.ToLower(cols["суммаа"])
	if typ == "" {
		t.Fatalf("колонка суммаа пропала: %v", cols)
	}
	if strings.Contains(typ, "numeric") || strings.Contains(typ, "double") || strings.Contains(typ, "int") {
		t.Fatalf("ALTER TYPE не откатился после сбоя посреди плана: суммаа = %q", typ)
	}

	// Данные тоже на месте (таблица не потеряла строку при откате переписывания).
	var got string
	if err := db.QueryRow(ctx, `SELECT суммаа::text FROM схематест WHERE id = $1`, id).Scan(&got); err != nil {
		t.Fatalf("чтение суммаа: %v", err)
	}
	if got != "100" {
		t.Fatalf("данные изменились при откате: суммаа = %q", got)
	}
}
