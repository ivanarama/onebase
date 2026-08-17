package query_test

// Булевы колонки одинаковы на обоих движках (#962, находка Н4).
//
// Матричный тест обязателен по самой природе дефекта: PostgreSQL отдаёт bool,
// SQLite хранит булево в INTEGER и отдаёт int64. Раздельные тесты этого не
// показывают — каждый проверяет своё ожидание на своём движке и оба зелёные,
// пока поведение молча разное (правило из CLAUDE.md, повод — #607).
//
// Цена расхождения не косметическая: правило условного оформления
// «услуга = Истина» на PostgreSQL срабатывает, на SQLite нет; ключи группировок
// расходятся; `onebase query --json` отдаёт разные типы. Пресет отчёта,
// настроенный на одном движке, на другом ведёт себя иначе.

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/ivantit66/onebase/internal/dbtest"
	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/ivantit66/onebase/internal/query"
	"github.com/ivantit66/onebase/internal/storage"
)

func boolCatalog() *metadata.Entity {
	return &metadata.Entity{
		Name: "Номенклатура", Kind: metadata.KindCatalog,
		Fields: []metadata.Field{
			{Name: "Наименование", Type: metadata.FieldTypeString},
			{Name: "Услуга", Type: metadata.FieldTypeBool},
		},
	}
}

func TestRun_БулеваКолонкаОдинаковаНаОбоихДвижках(t *testing.T) {
	dbtest.ForEachDialect(t, func(t *testing.T, db *storage.DB) {
		ctx := context.Background()
		ent := boolCatalog()
		if err := db.Migrate(ctx, []*metadata.Entity{ent}); err != nil {
			t.Fatalf("миграция: %v", err)
		}
		if err := db.Upsert(ctx, ent.Name, uuid.New(),
			map[string]any{"Наименование": "Доставка", "Услуга": true}, ent); err != nil {
			t.Fatalf("вставка услуги: %v", err)
		}
		if err := db.Upsert(ctx, ent.Name, uuid.New(),
			map[string]any{"Наименование": "Тумбочка", "Услуга": false}, ent); err != nil {
			t.Fatalf("вставка товара: %v", err)
		}

		compiled, err := query.Compile("ВЫБРАТЬ Наименование, Услуга ИЗ Справочник.Номенклатура УПОРЯДОЧИТЬ ПО Наименование",
			query.CompileOpts{Entities: []*metadata.Entity{ent}, Dialect: db.Dialect()})
		if err != nil {
			t.Fatalf("компиляция: %v", err)
		}

		rows, _, err := query.Run(ctx, db, &compiled)
		if err != nil {
			t.Fatalf("выполнение: %v", err)
		}
		if len(rows) != 2 {
			t.Fatalf("строк %d, ожидалось 2", len(rows))
		}

		// Именно bool, а не int64 и не 1/0: по этому значению прикладной код
		// ветвится, а отчёт строит ключ группировки.
		for _, row := range rows {
			v, ok := row["услуга"]
			if !ok {
				t.Fatalf("нет колонки «услуга» в строке %+v", row)
			}
			if _, isBool := v.(bool); !isBool {
				t.Fatalf("услуга пришла как %T (%v) — на другом движке будет иначе, и правила оформления разойдутся", v, v)
			}
		}
		if rows[0]["услуга"] != true {
			t.Errorf("Доставка: услуга = %v, ожидалось true", rows[0]["услуга"])
		}
		if rows[1]["услуга"] != false {
			t.Errorf("Тумбочка: услуга = %v, ожидалось false", rows[1]["услуга"])
		}
	})
}

// NULL обязан остаться пустым значением: «не заполнено» — не то же самое, что
// Ложь, и подменять одно другим нельзя.
func TestRun_ПустоеБулевоНеСтановитсяЛожью(t *testing.T) {
	rows := []map[string]any{{"услуга": nil}, {"услуга": int64(1)}}
	query.NormalizeBoolColumns([]string{"услуга"}, rows)

	if rows[0]["услуга"] != nil {
		t.Errorf("NULL превратился в %v", rows[0]["услуга"])
	}
	if rows[1]["услуга"] != true {
		t.Errorf("int64(1) не приведён к true: %v (%T)", rows[1]["услуга"], rows[1]["услуга"])
	}
}
