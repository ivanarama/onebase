package storage_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/ivantit66/onebase/internal/project"
	"github.com/ivantit66/onebase/internal/storage"
)

// Синтезированный «Номер» документа обязан иметь устойчивый ID (#868).
//
// Сценарий из пересмотра плана 117 (#668): «Номер» объявлен в YAML явно, с id;
// потом строку убрали, оставив numerator: — платформа синтезирует «Номер» сама.
// Синтез БЕЗ id означал, что в PlanTableChanges карта wanted (она строится
// только по полям с id) про эту колонку не знает: сторож коллизии молчит, и
// план предлагает ChangeDrop — «данные колонки будут потеряны безвозвратно».
// С --allow-destructive это сносит номера ВСЕХ документов.
//
// «Код» справочника такой ID получил сразу (StandardCodeFieldID), «Номер» —
// нет: решение №2 применили к одному из двух одинаковых случаев.
//
// Тест идёт через project.Load, а не через литерал metadata.Entity: синтез
// живёт в загрузчике YAML, и собранная в коде сущность его бы не выполнила.
func writeDocProject(t *testing.T, docYAML string) *project.Project {
	t.Helper()
	dir := t.TempDir()
	write := func(rel, body string) {
		p := filepath.Join(dir, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o750); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	write("config/app.yaml", "name: Тест\n")
	write("documents/реализация.yaml", docYAML)

	proj, err := project.Load(dir)
	if err != nil {
		t.Fatalf("project.Load: %v", err)
	}
	t.Cleanup(proj.Close)
	return proj
}

func docEntity(t *testing.T, proj *project.Project) *metadata.Entity {
	t.Helper()
	for _, e := range proj.Entities {
		if e.Name == "Реализация" {
			return e
		}
	}
	t.Fatal("документ Реализация не загружен")
	return nil
}

func TestСинтезированныйНомер_НеПланируетсяКУдалению(t *testing.T) {
	ctx := context.Background()
	db, err := storage.ConnectSQLite(ctx, filepath.Join(t.TempDir(), "num.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(db.Close)

	// Было: «Номер» объявлен явно, со своим id.
	before := docEntity(t, writeDocProject(t, `name: Реализация
numerator: {prefix: "Р-", length: 6}
fields:
  - {id: f_number, name: Номер, type: string}
  - {name: Сумма, type: number(15,2)}
`))
	if err := db.Migrate(ctx, []*metadata.Entity{before}); err != nil {
		t.Fatalf("миграция: %v", err)
	}
	id := uuid.New()
	if err := db.Upsert(ctx, before.Name, id, map[string]any{"Номер": "Р-000001", "Сумма": "10"}, before); err != nil {
		t.Fatal(err)
	}

	// Стало: строку про «Номер» убрали, numerator: остался — поле синтезируется.
	after := docEntity(t, writeDocProject(t, `name: Реализация
numerator: {prefix: "Р-", length: 6}
fields:
  - {name: Сумма, type: number(15,2)}
`))
	var synthesized *metadata.Field
	for i := range after.Fields {
		if after.Fields[i].Name == metadata.StandardNumberField {
			synthesized = &after.Fields[i]
		}
	}
	if synthesized == nil {
		t.Fatal("«Номер» не синтезирован — тест не про то, что задуман")
	}
	if synthesized.ID == "" {
		t.Fatal("синтезированный «Номер» без ID: миграция примет колонку за новую и снесёт данные")
	}

	changes, err := db.PlanTableChanges(ctx, metadata.TableName(after.Name), after.Fields)
	for _, c := range changes {
		if c.Kind == storage.ChangeDrop && c.From == "номер" {
			t.Fatalf("план предлагает снести колонку номера: %+v (под --allow-destructive пропали бы номера ВСЕХ документов)", c)
		}
	}
	// Сторож коллизии обязан ЗАГОВОРИТЬ: колонка числится за убранным полем, а
	// занимает её синтезированный «Номер». Отказ с объяснением — правильный
	// исход, ровно как у «Кода» справочника (117B), где это уже так работает.
	if err == nil {
		t.Fatal("план построен молча: расхождение карты и метаданных не замечено")
	}
	if !strings.Contains(err.Error(), "номер") || !strings.Contains(err.Error(), "вручную") {
		t.Errorf("отказ не объясняет, что делать: %v", err)
	}

	// И главное: данные на месте — отказ не тронул ни колонку, ни номера.
	row, err := db.GetByID(ctx, after.Name, id, after)
	if err != nil {
		t.Fatal(err)
	}
	if got, _ := row[metadata.StandardNumberField].(string); got != "Р-000001" {
		t.Errorf("номер = %q, ожидался «Р-000001»", got)
	}
}

// Обычный случай — «Номер» синтезируется с самого начала — миграцией проходит
// и повторным планом ничего не предлагает: устойчивый ID не должен породить
// вечное «изменение», которое применяется на каждом запуске.
func TestСинтезированныйНомер_ПовторныйПланПуст(t *testing.T) {
	ctx := context.Background()
	db, err := storage.ConnectSQLite(ctx, filepath.Join(t.TempDir(), "num2.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(db.Close)

	doc := docEntity(t, writeDocProject(t, `name: Реализация
numerator: {prefix: "Р-", length: 6}
fields:
  - {name: Сумма, type: number(15,2)}
`))
	if err := db.Migrate(ctx, []*metadata.Entity{doc}); err != nil {
		t.Fatalf("миграция: %v", err)
	}
	changes, err := db.PlanTableChanges(ctx, metadata.TableName(doc.Name), doc.Fields)
	if err != nil {
		t.Fatalf("повторный план: %v", err)
	}
	for _, c := range changes {
		if c.From == "номер" || c.To == "номер" {
			t.Errorf("повторный план трогает колонку номера: %+v", c)
		}
	}
}
