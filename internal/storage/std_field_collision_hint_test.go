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

// writeCatalogProject — справочник «Ученики» из заявки #1161. Тест идёт через
// project.Load по той же причине, что и сосед по файлу number_field_id_test.go:
// синтез стандартного поля живёт в загрузчике YAML, и собранная в коде сущность
// его бы не выполнила.
func writeCatalogProject(t *testing.T, catalogYAML string) *metadata.Entity {
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
	write("catalogs/ученики.yaml", catalogYAML)

	proj, err := project.Load(dir)
	if err != nil {
		t.Fatalf("project.Load: %v", err)
	}
	t.Cleanup(proj.Close)
	for _, e := range proj.Entities {
		if e.Name == "Ученики" {
			return e
		}
	}
	t.Fatal("справочник Ученики не загружен")
	return nil
}

// Базам, куда конфигуратор уже записал «Код» с чужим id, фикс сохранения не
// поможет: в YAML идентификатор так и останется чужим, и миграция продолжит
// отказывать. Отказ обязан называть правку, а не оставлять человека с общим
// «разберите вручную» — правка тут ровно одна (#1161).
func TestПланИзменений_ПодсказкаПроСтандартныйКод(t *testing.T) {
	ctx := context.Background()
	db, err := storage.ConnectSQLite(ctx, filepath.Join(t.TempDir(), "code.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(db.Close)

	// Было: «Код» синтезирован платформой, колонка числится за std_code.
	before := writeCatalogProject(t, `name: Ученики
numerator: {length: 8, period: none, unique: true}
fields:
  - {id: f_4b7b017c, name: Фамилия, type: string}
`)
	if err := db.Migrate(ctx, []*metadata.Entity{before}); err != nil {
		t.Fatalf("миграция: %v", err)
	}
	id := uuid.New()
	if err := db.Upsert(ctx, before.Name, id, map[string]any{"Код": "00000001", "Фамилия": "Иванов"}, before); err != nil {
		t.Fatal(err)
	}

	// Стало: конфигуратор записал «Код» в fields с собственным id — ровно тот
	// YAML, что приложен к заявке.
	after := writeCatalogProject(t, `name: Ученики
numerator: {length: 8, period: none, unique: true}
fields:
  - {id: f_61228bb1, name: Код, type: string}
  - {id: f_4b7b017c, name: Фамилия, type: string}
`)

	_, err = db.PlanTableChanges(ctx, metadata.TableName(after.Name), after.Fields)
	if err == nil {
		t.Fatal("план построен молча: колонка кода досталась полю с чужим id")
	}
	for _, want := range []string{metadata.StandardCodeFieldID, "id: " + metadata.StandardCodeFieldID, "numerator"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("в отказе нет %q, человеку не с чем идти в YAML:\n%v", want, err)
		}
	}

	// Данные отказ не тронул.
	row, err := db.GetByID(ctx, after.Name, id, after)
	if err != nil {
		t.Fatal(err)
	}
	if got, _ := row["Фамилия"].(string); got != "Иванов" {
		t.Errorf("фамилия = %q, ожидалась «Иванов»", got)
	}
}

// Чужая коллизия подсказку получать не должна: сторож не знает, какое из полей
// лишнее, и совет «пропишите id» увёл бы разбор в сторону.
func TestПланИзменений_ЧужаяКоллизияБезПодсказки(t *testing.T) {
	ctx := context.Background()
	db, err := storage.ConnectSQLite(ctx, filepath.Join(t.TempDir(), "plain.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(db.Close)

	before := writeCatalogProject(t, `name: Ученики
fields:
  - {id: f_aaa, name: Класс, type: string}
`)
	if err := db.Migrate(ctx, []*metadata.Entity{before}); err != nil {
		t.Fatalf("миграция: %v", err)
	}

	// Поле убрали, а другое с тем же именем колонки завели заново.
	after := writeCatalogProject(t, `name: Ученики
fields:
  - {id: f_bbb, name: Класс, type: string}
`)
	_, err = db.PlanTableChanges(ctx, metadata.TableName(after.Name), after.Fields)
	if err == nil {
		t.Fatal("сторож коллизии промолчал")
	}
	// Убеждаемся, что отказал именно сторож коллизии: иначе «подсказки нет»
	// было бы правдой по случайной причине.
	if !strings.Contains(err.Error(), "числится за убранным полем") {
		t.Fatalf("отказ пришёл не от сторожа коллизии: %v", err)
	}
	if strings.Contains(err.Error(), metadata.StandardCodeFieldID) || strings.Contains(err.Error(), metadata.StandardNumberFieldID) {
		t.Errorf("подсказка про стандартное поле выдана не по адресу:\n%v", err)
	}
}
