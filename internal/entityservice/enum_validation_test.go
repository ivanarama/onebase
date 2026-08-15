package entityservice

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/ivantit66/onebase/internal/dsl/interpreter"
	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/ivantit66/onebase/internal/runtime"
	"github.com/ivantit66/onebase/internal/storage"
)

// Значение перечисления не проверялось ничем: произвольная строка доезжала до
// БД как есть (#769).
//
// Для констант это чинили в #320/#321, но тот фикс закрыл ровно один хендлер.
// Реквизиты справочников и документов остались без проверки, потому что её
// никогда не было в общем ядре записи — а через Save идут ВСЕ пути: браузерная
// форма (там `<select>` ограничивает выбор на клиенте, то есть ни на чём),
// REST v2 и синхронизация офлайн-очереди, где устаревшее значение из кэша
// проходит молча.
func enumFixture(t *testing.T) (context.Context, *storage.DB, *Service, *metadata.Entity) {
	t.Helper()
	ctx := context.Background()
	db, err := storage.ConnectSQLite(ctx, filepath.Join(t.TempDir(), "enum.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })

	status := &metadata.Enum{Name: "СтатусЗадачи", Values: []string{"Новая", "ВРаботе", "Закрыта"}}
	task := &metadata.Entity{
		Name: "Задача",
		Kind: metadata.KindCatalog,
		Fields: []metadata.Field{
			{Name: "Наименование", Type: metadata.FieldTypeString},
			{Name: "Статус", Type: "enum:СтатусЗадачи", EnumName: "СтатусЗадачи"},
		},
		TableParts: []metadata.TablePart{{Name: "Этапы", Fields: []metadata.Field{
			{Name: "Состояние", Type: "enum:СтатусЗадачи", EnumName: "СтатусЗадачи"},
		}}},
	}
	if err := db.Migrate(ctx, []*metadata.Entity{task}); err != nil {
		t.Fatal(err)
	}
	reg := runtime.NewRegistry()
	reg.Load(runtime.LoadOptions{Entities: []*metadata.Entity{task}, Enums: []*metadata.Enum{status}})
	return ctx, db, &Service{Store: db, Reg: reg, Interp: interpreter.New()}, task
}

func TestSave_ЧужоеЗначениеПеречисленияОтклоняется(t *testing.T) {
	ctx, db, svc, task := enumFixture(t)

	id := uuid.New()
	res, err := svc.Save(ctx, SaveRequest{
		Entity: task, ID: id, IsNew: true,
		Fields: map[string]any{"Наименование": "Тест", "Статус": "НЕ_СУЩЕСТВУЮЩЕЕ"},
	})
	if err != nil {
		t.Fatalf("ожидался пользовательский отказ, а не технический сбой: %v", err)
	}
	if res.DSLError == "" {
		t.Fatal("значение вне перечисления записано молча")
	}
	if !strings.Contains(res.DSLError, "СтатусЗадачи") || !strings.Contains(res.DSLError, "НЕ_СУЩЕСТВУЮЩЕЕ") {
		t.Errorf("отказ не называет ни поле, ни значение: %s", res.DSLError)
	}
	// Отказ обязан быть отказом: записи в базе быть не должно.
	if _, err := db.GetByID(ctx, task.Name, id, task); err == nil {
		t.Error("объект записан несмотря на отказ")
	}
}

// Та же проверка для строк табличной части: там значение приходит из тех же
// источников и проверялось так же — никак.
func TestSave_ЧужоеЗначениеПеречисленияВТЧОтклоняется(t *testing.T) {
	ctx, _, svc, task := enumFixture(t)

	res, err := svc.Save(ctx, SaveRequest{
		Entity: task, ID: uuid.New(), IsNew: true,
		Fields: map[string]any{"Наименование": "Тест", "Статус": "Новая"},
		TablePartRows: map[string][]map[string]any{
			"Этапы": {{"Состояние": "Новая"}, {"Состояние": "ЧУЖОЕ"}},
		},
	})
	if err != nil {
		t.Fatalf("технический сбой: %v", err)
	}
	if res.DSLError == "" {
		t.Fatal("значение вне перечисления в строке ТЧ записано молча")
	}
	// Номер строки в сообщении — иначе в таблице на сто строк искать негде.
	if !strings.Contains(res.DSLError, "Этапы[2]") {
		t.Errorf("отказ не указывает строку: %s", res.DSLError)
	}
}

// Обратная сторона: допустимые значения и пустое «не выбрано» проходят.
// Обязательность поля — отдельный механизм, эта проверка её не подменяет.
func TestSave_ДопустимыеЗначенияПроходят(t *testing.T) {
	ctx, db, svc, task := enumFixture(t)

	for _, val := range []any{"Новая", "Закрыта", "", nil} {
		id := uuid.New()
		res, err := svc.Save(ctx, SaveRequest{
			Entity: task, ID: id, IsNew: true,
			Fields: map[string]any{"Наименование": "Тест", "Статус": val},
		})
		if err != nil {
			t.Fatalf("значение %v: %v", val, err)
		}
		if res.DSLError != "" {
			t.Fatalf("значение %v отклонено: %s", val, res.DSLError)
		}
		if _, err := db.GetByID(ctx, task.Name, id, task); err != nil {
			t.Fatalf("значение %v: объект не записан: %v", val, err)
		}
	}
}

// Ненайденное перечисление при ЗАГРУЖЕННЫХ других — отказ: конфигурация с
// такой ссылкой не проходит metadata.Validate, значит сюда она попасть может
// только сломанной, и записывать «что угодно» на этом основании нельзя.
func TestSave_НенайденноеПеречислениеОтклоняется(t *testing.T) {
	ctx, _, svc, task := enumFixture(t)
	broken := *task
	broken.Fields = []metadata.Field{
		{Name: "Наименование", Type: metadata.FieldTypeString},
		{Name: "Статус", Type: "enum:НетТакого", EnumName: "НетТакого"},
	}
	broken.TableParts = nil

	res, err := svc.Save(ctx, SaveRequest{
		Entity: &broken, ID: uuid.New(), IsNew: true,
		Fields: map[string]any{"Наименование": "Тест", "Статус": "Любое"},
	})
	if err != nil {
		t.Fatalf("технический сбой: %v", err)
	}
	if res.DSLError == "" || !strings.Contains(res.DSLError, "не найдено") {
		t.Fatalf("запись с несуществующим перечислением принята: %q", res.DSLError)
	}
}

// Реестр вообще без перечислений проверку не включает: проверять не с чем.
// Так же поступает metadata.Validate (`len(enums) > 0`), и служебные прогоны с
// неполным реестром не должны терять возможность записи.
func TestSave_БезЗагруженныхПеречисленийЗаписьПроходит(t *testing.T) {
	ctx := context.Background()
	db, err := storage.ConnectSQLite(ctx, filepath.Join(t.TempDir(), "noenum.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })

	task := &metadata.Entity{
		Name: "Задача",
		Kind: metadata.KindCatalog,
		Fields: []metadata.Field{
			{Name: "Наименование", Type: metadata.FieldTypeString},
			{Name: "Статус", Type: "enum:СтатусЗадачи", EnumName: "СтатусЗадачи"},
		},
	}
	if err := db.Migrate(ctx, []*metadata.Entity{task}); err != nil {
		t.Fatal(err)
	}
	reg := runtime.NewRegistry()
	reg.Load(runtime.LoadOptions{Entities: []*metadata.Entity{task}}) // перечислений нет
	svc := &Service{Store: db, Reg: reg, Interp: interpreter.New()}

	res, err := svc.Save(ctx, SaveRequest{
		Entity: task, ID: uuid.New(), IsNew: true,
		Fields: map[string]any{"Наименование": "Тест", "Статус": "Любое"},
	})
	if err != nil {
		t.Fatalf("технический сбой: %v", err)
	}
	if res.DSLError != "" {
		t.Fatalf("запись отклонена при пустом реестре перечислений: %s", res.DSLError)
	}
}
