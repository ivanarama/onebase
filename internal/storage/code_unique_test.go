package storage_test

// Уникальность кода и номера (план 117E).
//
// Тесты матричные, потому что гарантию даёт СУБД, а не Go: индекс, код отказа и
// формат сообщения у SQLite и PostgreSQL разные, и юнит на одном диалекте
// показал бы зелёное там, где на другом поведение молча иное.

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/ivantit66/onebase/internal/dbtest"
	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/ivantit66/onebase/internal/storage"
)

func uniqueCatalog(name string, unique bool) *metadata.Entity {
	return &metadata.Entity{
		Name: name, Kind: metadata.KindCatalog,
		Fields: []metadata.Field{
			{Name: metadata.StandardCodeField, Type: metadata.FieldTypeString},
			{Name: "Наименование", Type: metadata.FieldTypeString},
		},
		Numerator: &metadata.Numerator{Prefix: "К-", Length: 6, Period: "none", Unique: unique},
	}
}

// Занятый код отклоняется, и пользователь видит человеческий текст, а не
// «UNIQUE constraint failed: контрагенты.код» / SQLSTATE 23505.
func TestUniqueCode_RejectsDuplicateMatrix(t *testing.T) {
	dbtest.ForEachDialect(t, func(t *testing.T, db *storage.DB) {
		ctx := context.Background()
		ent := uniqueCatalog("Контрагенты"+uuid.NewString()[:8], true)
		if err := db.Migrate(ctx, []*metadata.Entity{ent}); err != nil {
			t.Fatalf("миграция: %v", err)
		}

		first := map[string]any{metadata.StandardCodeField: "К-000042", "Наименование": "Альфа"}
		if err := db.Upsert(ctx, ent.Name, uuid.New(), first, ent); err != nil {
			t.Fatalf("первая запись: %v", err)
		}

		second := map[string]any{metadata.StandardCodeField: "К-000042", "Наименование": "Бета"}
		err := db.Upsert(ctx, ent.Name, uuid.New(), second, ent)
		if err == nil {
			t.Fatal("дубль кода записан: уникальность не работает")
		}
		if !errors.Is(err, storage.ErrCodeDuplicate) {
			t.Fatalf("ошибка = %v, ожидалась ErrCodeDuplicate", err)
		}
		text := err.Error()
		for _, want := range []string{metadata.StandardCodeField, "К-000042"} {
			if !strings.Contains(text, want) {
				t.Errorf("в сообщении нет %q: %s", want, text)
			}
		}
		if strings.Contains(text, "UNIQUE constraint") || strings.Contains(text, "SQLSTATE") {
			t.Errorf("наружу утёк текст драйвера: %s", text)
		}
	})
}

// Без unique: true дубли по-прежнему разрешены — уникальность не включается
// сама собой, иначе она сломала бы живые конфигурации при обновлении.
func TestUniqueCode_OffByDefaultMatrix(t *testing.T) {
	dbtest.ForEachDialect(t, func(t *testing.T, db *storage.DB) {
		ctx := context.Background()
		ent := uniqueCatalog("Склады"+uuid.NewString()[:8], false)
		if err := db.Migrate(ctx, []*metadata.Entity{ent}); err != nil {
			t.Fatalf("миграция: %v", err)
		}
		for _, name := range []string{"Альфа", "Бета"} {
			row := map[string]any{metadata.StandardCodeField: "К-000001", "Наименование": name}
			if err := db.Upsert(ctx, ent.Name, uuid.New(), row, ent); err != nil {
				t.Fatalf("запись %s без unique отклонена: %v", name, err)
			}
		}
	})
}

// Перезапись ТОЙ ЖЕ записи её собственным кодом — не дубль. Иначе повторное
// сохранение открытой карточки падало бы «код занят» на её же коде.
func TestUniqueCode_RewriteSameRowMatrix(t *testing.T) {
	dbtest.ForEachDialect(t, func(t *testing.T, db *storage.DB) {
		ctx := context.Background()
		ent := uniqueCatalog("Номенклатура"+uuid.NewString()[:8], true)
		if err := db.Migrate(ctx, []*metadata.Entity{ent}); err != nil {
			t.Fatalf("миграция: %v", err)
		}
		id := uuid.New()
		row := map[string]any{metadata.StandardCodeField: "К-000007", "Наименование": "Альфа"}
		if err := db.Upsert(ctx, ent.Name, id, row, ent); err != nil {
			t.Fatalf("первая запись: %v", err)
		}
		row["Наименование"] = "Альфа (переименована)"
		if err := db.Upsert(ctx, ent.Name, id, row, ent); err != nil {
			t.Fatalf("перезапись своей же записи отклонена: %v", err)
		}
	})
}

// Включение уникальности при пустых кодах отклоняется с указанием на renumber:
// NULL в уникальном индексе не конфликтуют ни на одном диалекте, поэтому DDL
// прошёл бы молча, а дубли всплыли бы позже и непонятно откуда.
func TestUniqueCode_PreconditionEmptyValuesMatrix(t *testing.T) {
	dbtest.ForEachDialect(t, func(t *testing.T, db *storage.DB) {
		ctx := context.Background()
		name := "Договоры" + uuid.NewString()[:8]
		plain := uniqueCatalog(name, false)
		if err := db.Migrate(ctx, []*metadata.Entity{plain}); err != nil {
			t.Fatalf("миграция без unique: %v", err)
		}
		if err := db.Upsert(ctx, name, uuid.New(), map[string]any{"Наименование": "Без кода"}, plain); err != nil {
			t.Fatalf("вставка: %v", err)
		}

		strict := uniqueCatalog(name, true)
		err := db.Migrate(ctx, []*metadata.Entity{strict})
		if err == nil {
			t.Fatal("уникальность включена при пустых кодах — молча и без эффекта")
		}
		// Сверка именно с константой: по ней лаунчер узнаёт класс ошибки в
		// хвосте лога дочернего процесса и показывает кнопку «дозаполнить коды»
		// вместо инструкции для консоли (#1067). Разойдутся текст и маркер —
		// кнопка тихо перестанет появляться.
		if !strings.Contains(err.Error(), storage.RenumberHint) {
			t.Errorf("в отказе нет подсказки про %s: %v", storage.RenumberHint, err)
		}
	})
}

// Включение уникальности при уже существующих дублях тоже отклоняется — но
// сообщением про исправление, а не про дозаполнение.
func TestUniqueCode_PreconditionExistingDuplicatesMatrix(t *testing.T) {
	dbtest.ForEachDialect(t, func(t *testing.T, db *storage.DB) {
		ctx := context.Background()
		name := "Партнёры" + uuid.NewString()[:8]
		plain := uniqueCatalog(name, false)
		if err := db.Migrate(ctx, []*metadata.Entity{plain}); err != nil {
			t.Fatalf("миграция без unique: %v", err)
		}
		for _, n := range []string{"Альфа", "Бета"} {
			row := map[string]any{metadata.StandardCodeField: "К-000001", "Наименование": n}
			if err := db.Upsert(ctx, name, uuid.New(), row, plain); err != nil {
				t.Fatalf("вставка %s: %v", n, err)
			}
		}

		err := db.Migrate(ctx, []*metadata.Entity{uniqueCatalog(name, true)})
		if err == nil {
			t.Fatal("уникальность включена поверх существующих дублей")
		}
		if !strings.Contains(err.Error(), "повторяющихся") {
			t.Errorf("отказ не объясняет причину: %v", err)
		}
	})
}

// Чистая база с уникальностью мигрируется без нареканий, и код после этого
// выдаётся и пишется — проверка не должна блокировать нормальный случай.
func TestUniqueCode_CleanBaseMigratesMatrix(t *testing.T) {
	dbtest.ForEachDialect(t, func(t *testing.T, db *storage.DB) {
		ctx := context.Background()
		ent := uniqueCatalog("Валюты"+uuid.NewString()[:8], true)
		if err := db.Migrate(ctx, []*metadata.Entity{ent}); err != nil {
			t.Fatalf("миграция чистой базы: %v", err)
		}
		if err := db.EnsureNumeratorSchema(ctx); err != nil {
			t.Fatalf("схема нумератора: %v", err)
		}
		code, err := db.GenerateNumber(ctx, ent, map[string]any{})
		if err != nil {
			t.Fatalf("GenerateNumber: %v", err)
		}
		row := map[string]any{metadata.StandardCodeField: code, "Наименование": "Рубль"}
		if err := db.Upsert(ctx, ent.Name, uuid.New(), row, ent); err != nil {
			t.Fatalf("запись выданного кода отклонена: %v", err)
		}
		// Повторная миграция той же схемы не должна падать на своём же индексе.
		if err := db.Migrate(ctx, []*metadata.Entity{ent}); err != nil {
			t.Fatalf("повторная миграция: %v", err)
		}
	})
}

// Ручной уникальный индекс по тому же полю не дублируется автоматическим: две
// одинаковые гарантии дали бы два индекса и два разных сообщения об одном и том
// же отказе.
func TestUniqueCodeIndexSpec_SkipsManualIndex(t *testing.T) {
	ent := uniqueCatalog("Контрагенты", true)
	if _, need := storage.UniqueCodeIndexSpec(ent); !need {
		t.Fatal("без ручного индекса автоматический не запрошен")
	}
	ent.Indexes = []metadata.IndexSpec{{Fields: []string{metadata.StandardCodeField}, Unique: true}}
	if _, need := storage.UniqueCodeIndexSpec(ent); need {
		t.Error("при ручном уникальном индексе по тому же полю заведён второй")
	}
	// Неуникальный ручной индекс — не замена: он ничего не гарантирует.
	ent.Indexes = []metadata.IndexSpec{{Fields: []string{metadata.StandardCodeField}}}
	if _, need := storage.UniqueCodeIndexSpec(ent); !need {
		t.Error("неуникальный ручной индекс принят за гарантию уникальности")
	}
}

// Без numerator.unique индекс не запрашивается вовсе.
func TestUniqueCodeIndexSpec_OffWithoutFlag(t *testing.T) {
	if _, need := storage.UniqueCodeIndexSpec(uniqueCatalog("Склады", false)); need {
		t.Error("индекс запрошен без unique: true")
	}
	if _, need := storage.UniqueCodeIndexSpec(&metadata.Entity{Name: "X", Kind: metadata.KindCatalog}); need {
		t.Error("индекс запрошен без нумератора вовсе")
	}
}

// Со scope: счётчик свой в каждом разрезе, поэтому одинаковые номера у разных
// организаций законны — глобальный индекс отклонил бы первый же документ второй
// организации. Уникальность здесь составная.
func TestUniqueCode_ScopedIsCompositeMatrix(t *testing.T) {
	dbtest.ForEachDialect(t, func(t *testing.T, db *storage.DB) {
		ctx := context.Background()
		ent := &metadata.Entity{
			Name: "Реализация" + uuid.NewString()[:8], Kind: metadata.KindDocument,
			Fields: []metadata.Field{
				{Name: "Номер", Type: metadata.FieldTypeString},
				{Name: "Организация", Type: metadata.FieldTypeString},
			},
			Numerator: &metadata.Numerator{Prefix: "Р-", Length: 6, Period: "none", Scope: "Организация", Unique: true},
		}
		if err := db.Migrate(ctx, []*metadata.Entity{ent}); err != nil {
			t.Fatalf("миграция: %v", err)
		}
		for _, org := range []string{"Альфа", "Бета"} {
			row := map[string]any{"Номер": "Р-000001", "Организация": org}
			if err := db.Upsert(ctx, ent.Name, uuid.New(), row, ent); err != nil {
				t.Fatalf("номер Р-000001 у организации %s отклонён: %v", org, err)
			}
		}
		// А внутри одной организации повтор по-прежнему запрещён.
		dup := map[string]any{"Номер": "Р-000001", "Организация": "Альфа"}
		err := db.Upsert(ctx, ent.Name, uuid.New(), dup, ent)
		if !errors.Is(err, storage.ErrCodeDuplicate) {
			t.Fatalf("повтор внутри разреза: ошибка = %v, ожидалась ErrCodeDuplicate", err)
		}
		if !strings.Contains(err.Error(), "Номер") {
			t.Errorf("сообщение не называет поле: %s", err)
		}
	})
}

func TestUniqueCodeIndexSpec_ScopeFirst(t *testing.T) {
	ent := uniqueCatalog("Контрагенты", true)
	ent.Fields = append(ent.Fields, metadata.Field{Name: "Организация", Type: metadata.FieldTypeString})
	ent.Numerator.Scope = "Организация"
	spec, need := storage.UniqueCodeIndexSpec(ent)
	if !need {
		t.Fatal("индекс не запрошен")
	}
	want := []string{"Организация", metadata.StandardCodeField}
	if strings.Join(spec.Fields, ",") != strings.Join(want, ",") {
		t.Errorf("поля индекса = %v, ожидались %v", spec.Fields, want)
	}
}

// Д10: снятие unique: true из YAML должно снимать и индекс. CREATE INDEX IF NOT
// EXISTS ничего не удаляет, поэтому без явного DROP отключение уникальности не
// давало бы НИЧЕГО — записи продолжали бы отклоняться, а автор конфигурации
// искал бы причину в коде, а не в базе.
func TestUniqueCode_DisablingDropsIndexMatrix(t *testing.T) {
	dbtest.ForEachDialect(t, func(t *testing.T, db *storage.DB) {
		ctx := context.Background()
		name := "Проекты" + uuid.NewString()[:8]
		if err := db.Migrate(ctx, []*metadata.Entity{uniqueCatalog(name, true)}); err != nil {
			t.Fatalf("миграция с unique: %v", err)
		}
		strict := uniqueCatalog(name, true)
		if err := db.Upsert(ctx, name, uuid.New(),
			map[string]any{metadata.StandardCodeField: "К-000001", "Наименование": "Альфа"}, strict); err != nil {
			t.Fatalf("первая запись: %v", err)
		}
		if err := db.Upsert(ctx, name, uuid.New(),
			map[string]any{metadata.StandardCodeField: "К-000001", "Наименование": "Бета"}, strict); err == nil {
			t.Fatal("уникальность не действует — тест не проверяет то, что должен")
		}

		relaxed := uniqueCatalog(name, false)
		if err := db.Migrate(ctx, []*metadata.Entity{relaxed}); err != nil {
			t.Fatalf("миграция без unique: %v", err)
		}
		if err := db.Upsert(ctx, name, uuid.New(),
			map[string]any{metadata.StandardCodeField: "К-000001", "Наименование": "Бета"}, relaxed); err != nil {
			t.Fatalf("после снятия unique запись всё ещё отклоняется — индекс не снят: %v", err)
		}
	})
}

// Смена scope должна снять прежний составной индекс. Иначе после перехода с
// разреза «Организация» на «Склад» база продолжает одновременно применять оба
// правила и отклоняет допустимый одинаковый код на разных складах.
func TestUniqueCode_ChangingScopeDropsOldIndexMatrix(t *testing.T) {
	dbtest.ForEachDialect(t, func(t *testing.T, db *storage.DB) {
		ctx := context.Background()
		name := "Заказы" + uuid.NewString()[:8]
		byOrganization := uniqueCatalog(name, true)
		byOrganization.Fields = append(byOrganization.Fields,
			metadata.Field{Name: "Организация", Type: metadata.FieldTypeString},
			metadata.Field{Name: "Склад", Type: metadata.FieldTypeString},
		)
		byOrganization.Numerator.Scope = "Организация"
		if err := db.Migrate(ctx, []*metadata.Entity{byOrganization}); err != nil {
			t.Fatalf("миграция с разрезом по организации: %v", err)
		}
		if err := db.Upsert(ctx, name, uuid.New(), map[string]any{
			metadata.StandardCodeField: "К-000001",
			"Наименование":             "Первый",
			"Организация":              "Альфа",
			"Склад":                    "Север",
		}, byOrganization); err != nil {
			t.Fatalf("первая запись: %v", err)
		}

		byWarehouse := uniqueCatalog(name, true)
		byWarehouse.Fields = append(byWarehouse.Fields,
			metadata.Field{Name: "Организация", Type: metadata.FieldTypeString},
			metadata.Field{Name: "Склад", Type: metadata.FieldTypeString},
		)
		byWarehouse.Numerator.Scope = "Склад"
		if err := db.Migrate(ctx, []*metadata.Entity{byWarehouse}); err != nil {
			t.Fatalf("смена разреза на склад: %v", err)
		}
		if err := db.Upsert(ctx, name, uuid.New(), map[string]any{
			metadata.StandardCodeField: "К-000001",
			"Наименование":             "Второй",
			"Организация":              "Альфа",
			"Склад":                    "Юг",
		}, byWarehouse); err != nil {
			t.Fatalf("старый индекс по организации не снят: %v", err)
		}
	})
}

// Объявленный вручную уникальный индекс снятием не задевается: он не наш.
func TestUniqueCode_KeepsManualIndexMatrix(t *testing.T) {
	dbtest.ForEachDialect(t, func(t *testing.T, db *storage.DB) {
		ctx := context.Background()
		ent := uniqueCatalog("Бренды"+uuid.NewString()[:8], false)
		ent.Indexes = []metadata.IndexSpec{{Fields: []string{metadata.StandardCodeField}, Unique: true}}
		if err := db.Migrate(ctx, []*metadata.Entity{ent}); err != nil {
			t.Fatalf("миграция: %v", err)
		}
		if err := db.Upsert(ctx, ent.Name, uuid.New(),
			map[string]any{metadata.StandardCodeField: "К-000001", "Наименование": "Альфа"}, ent); err != nil {
			t.Fatalf("первая запись: %v", err)
		}
		if err := db.Upsert(ctx, ent.Name, uuid.New(),
			map[string]any{metadata.StandardCodeField: "К-000001", "Наименование": "Бета"}, ent); err == nil {
			t.Fatal("ручной уникальный индекс снесён снятием автоматического")
		}
	})
}
