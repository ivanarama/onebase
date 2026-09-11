package configcheck

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ivantit66/onebase/internal/project"
	"github.com/ivantit66/onebase/internal/storage"
)

// CheckModuleQueries должен компилировать и PREPARE-ить статические запросы из
// .os-модулей, репортить ошибки с локацией; динамические/валидные — пропускать.
func TestCheckModuleQueries(t *testing.T) {
	dir := t.TempDir()
	mkFile(t, filepath.Join(dir, "documents", "заказ.yaml"), `name: Заказ
fields:
  - name: Наименование
    type: string`)
	mkFile(t, filepath.Join(dir, "src", "тест.os"), `Процедура Тест()
  Хороший = Новый Запрос;
  Хороший.Текст = "ВЫБРАТЬ Наименование ИЗ Документ.Заказ";
  Плохой = Новый Запрос;
  Плохой.Текст = "ВЫБРАТЬ Наименование ИЗ Документ.НетТакого";
  Динамический = Новый Запрос;
  Динамический.Текст = "ВЫБРАТЬ " + ИмяПоля + " ИЗ Документ.Заказ";
КонецПроцедуры`)

	proj, err := project.Load(dir)
	if err != nil {
		t.Fatalf("project.Load: %v", err)
	}
	defer proj.Close()

	ctx := context.Background()
	dbPath := filepath.Join(dir, "schema.db")
	db, err := storage.ConnectSQLite(ctx, dbPath)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer func() { db.Close(); _ = os.Remove(dbPath) }()
	if err := db.Migrate(ctx, proj.Entities); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	issues, _ := CheckModuleQueries(proj, func(sql string) error {
		return db.ValidateQuery(ctx, sql)
	})
	if len(issues) != 1 {
		t.Fatalf("ожидалась 1 ошибка (только запрос к Документ.НетТакого), получено %d: %+v", len(issues), issues)
	}
	got := issues[0]
	if !strings.Contains(got.File, "тест.os") {
		t.Errorf("ожидался файл тест.os, получено %q", got.File)
	}
	if got.Line != 5 {
		t.Errorf("ожидалась строка 5 (плохой запрос), получено %d", got.Line)
	}
}
