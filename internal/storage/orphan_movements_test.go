package storage

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/google/uuid"
	"github.com/ivantit66/onebase/internal/metadata"
)

// OrphanMovements не должна зависать на единственном SQLite-соединении
// (SetMaxOpenConns(1)). Раньше вложенный COUNT выполнялся при открытом
// внешнем курсоре rows → блокировка на ожидании соединения. Тест также
// проверяет, что осиротевшие движения находятся и удаляются.
func TestOrphanMovements_NoDeadlockDetectAndDelete(t *testing.T) {
	ctx := context.Background()
	db, err := ConnectSQLite(ctx, filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	doc := &metadata.Entity{
		Name: "ПоступлениеТоваров", Kind: metadata.KindDocument, Posting: true,
		Fields: []metadata.Field{{Name: "Номер", Type: metadata.FieldTypeString}},
	}
	reg := &metadata.Register{
		Name:       "ОстаткиТоваров",
		Dimensions: []metadata.Field{{Name: "Номенклатура", Type: metadata.FieldTypeString}},
		Resources:  []metadata.Field{{Name: "Количество", Type: metadata.FieldTypeNumber}},
	}
	if err := db.Migrate(ctx, []*metadata.Entity{doc}); err != nil {
		t.Fatal(err)
	}
	if err := db.MigrateRegisters(ctx, []*metadata.Register{reg}); err != nil {
		t.Fatal(err)
	}

	// Движение с recorder на несуществующий документ (таблица документа пуста)
	// — это и есть осиротевшее движение.
	if err := db.WriteMovements(ctx, reg.Name, doc.Name, uuid.New(),
		[]map[string]any{{"ВидДвижения": "Приход", "Номенклатура": "Стол", "Количество": float64(5)}},
		reg, nil); err != nil {
		t.Fatal(err)
	}

	regs := []*metadata.Register{reg}
	ents := []*metadata.Entity{doc}

	// Должна вернуться без зависания и найти 1 осиротевшее движение.
	stats, err := db.OrphanMovements(ctx, regs, ents)
	if err != nil {
		t.Fatal(err)
	}
	var total int
	for _, s := range stats {
		total += s.Count
	}
	if total != 1 {
		t.Fatalf("ожидалось 1 осиротевшее движение, получили %d", total)
	}

	// Удаление вычищает их, повторное обнаружение пусто.
	deleted, err := db.DeleteOrphanMovements(ctx, regs, ents)
	if err != nil {
		t.Fatalf("DeleteOrphanMovements: %v", err)
	}
	if deleted != 1 {
		t.Errorf("DeleteOrphanMovements: удалено %d, ожидалось 1", deleted)
	}
	rest, err := db.OrphanMovements(ctx, regs, ents)
	if err != nil {
		t.Fatal(err)
	}
	if len(rest) != 0 {
		t.Errorf("после очистки осиротевших быть не должно, получили %v", rest)
	}
}

// #622: нечитаемый регистр обязан завершать проверку ОШИБКОЙ, а не выпадать из
// статистики (иначе doctor отчитается «сирот нет» не потому что их нет, а потому
// что не смотрели). Отсутствие таблицы при этом остаётся законным пропуском.
func TestOrphanMovements_QueryErrorSurfaces(t *testing.T) {
	ctx := context.Background()
	db, err := ConnectSQLite(ctx, filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	reg := &metadata.Register{
		Name:       "Битый",
		Dimensions: []metadata.Field{{Name: "Номенклатура", Type: metadata.FieldTypeString}},
		Resources:  []metadata.Field{{Name: "Количество", Type: metadata.FieldTypeNumber}},
	}

	// Регистр НЕ мигрирован: таблицы нет — это законный пропуск, не ошибка.
	stats, err := db.OrphanMovements(ctx, []*metadata.Register{reg}, nil)
	if err != nil {
		t.Fatalf("отсутствие таблицы регистра — не ошибка: %v", err)
	}
	if len(stats) != 0 {
		t.Fatalf("для несуществующего регистра статистика должна быть пустой: %v", stats)
	}

	// Таблица есть, но без колонки recorder_type — запрос по регистру не выполнится.
	// Раньше такой регистр молча выпадал из статистики; теперь — ошибка наверх.
	if _, err := db.Exec(ctx, `CREATE TABLE `+metadata.RegisterTableName(reg.Name)+` (id TEXT)`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.OrphanMovements(ctx, []*metadata.Register{reg}, nil); err == nil {
		t.Fatal("нечитаемый регистр обязан вернуть ошибку, а не пустую статистику")
	}
}

// #622: то же для сухого прогона --forget-document. Здесь проглоченная ошибка
// опаснее всего: прогон существует, чтобы показать объём НЕОБРАТИМОГО удаления,
// и недосчитанный «0» читается как «удалять нечего» — то есть уговаривает снять
// --dry-run и снести историю вслепую.
func TestCountMovementsOfRecorderType_QueryErrorSurfaces(t *testing.T) {
	ctx := context.Background()
	db, err := ConnectSQLite(ctx, filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	reg := &metadata.Register{
		Name:       "Битый",
		Dimensions: []metadata.Field{{Name: "Номенклатура", Type: metadata.FieldTypeString}},
		Resources:  []metadata.Field{{Name: "Количество", Type: metadata.FieldTypeNumber}},
	}
	regs := []*metadata.Register{reg}

	// Регистр НЕ мигрирован: таблицы нет — законный пропуск, не ошибка.
	n, err := db.CountMovementsOfRecorderType(ctx, regs, []string{"СовсемУбранныйДокумент"})
	if err != nil {
		t.Fatalf("отсутствие таблицы регистра — не ошибка: %v", err)
	}
	if n != 0 {
		t.Fatalf("для несуществующего регистра ожидался ноль, получили %d", n)
	}

	// Таблица есть, но без колонки recorder_type — счётный запрос не выполнится.
	if _, err := db.Exec(ctx, `CREATE TABLE `+metadata.RegisterTableName(reg.Name)+` (id TEXT)`); err != nil {
		t.Fatal(err)
	}
	n, err = db.CountMovementsOfRecorderType(ctx, regs, []string{"СовсемУбранныйДокумент"})
	if err == nil {
		t.Fatal("нечитаемый регистр обязан вернуть ошибку, а не молчаливый ноль")
	}
	if n != 0 {
		t.Errorf("вместе с ошибкой уехала частичная сумма %d — её примут за настоящий объём", n)
	}
}
