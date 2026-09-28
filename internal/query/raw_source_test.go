package query_test

// Источник запроса — объект конфигурации. Имя в ИЗ, которого нет в
// метаданных, уходило в SQL дословно и не регистрировалось источником, так
// что его не видели ни объектный RBAC (ИИ-помощник, недоверенный DSL), ни
// строковый доступ. Так читались служебные таблицы — _users с хешами паролей
// и секретами второго фактора, _sessions — и любые таблицы в обход прав.
// Голое имя сущности («ИЗ Клиент») — краткая форма, которой пользуются
// конфигурации: она исполняется как раньше, но теперь видна проверке прав.

import (
	"context"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/ivantit66/onebase/internal/dbtest"
	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/ivantit66/onebase/internal/query"
	"github.com/ivantit66/onebase/internal/storage"
)

func rawSourceEntities() []*metadata.Entity {
	return []*metadata.Entity{
		{Name: "Заявка", Kind: metadata.KindDocument, Fields: []metadata.Field{
			{Name: "Номер", Type: metadata.FieldTypeString},
			{Name: "Дата", Type: metadata.FieldTypeDate},
			{Name: "Клиент", Type: "reference:КлиентИст", RefEntity: "КлиентИст"},
		}},
		{Name: "КлиентИст", Kind: metadata.KindCatalog, Fields: []metadata.Field{
			{Name: "Наименование", Type: metadata.FieldTypeString},
		}},
	}
}

func TestQueryRejectsSourceOutsideConfiguration(t *testing.T) {
	for _, text := range []string{
		`ВЫБРАТЬ password_hash ИЗ _users`,
		`ВЫБРАТЬ У.login ИЗ _users КАК У`,
		`ВЫБРАТЬ З.Номер ИЗ Документ.Заявка КАК З ЛЕВОЕ СОЕДИНЕНИЕ _sessions КАК С ПО С.user_id = З.Номер`,
		`ВЫБРАТЬ Номер ИЗ Документ.Заявка ГДЕ Номер В (ВЫБРАТЬ login ИЗ _users)`,
		`ВЫБРАТЬ З.Номер ИЗ Документ.Заявка КАК З, _users КАК У`,
		`ВЫБРАТЬ name ИЗ sqlite_master`,
		// Квалификация схемой не обходит проверку: первым идёт имя схемы.
		`ВЫБРАТЬ password_hash ИЗ main._users`,
		`ВЫБРАТЬ password_hash ИЗ public._users`,
		// Таблица регистра под сырым именем — тоже мимо метаданных.
		`ВЫБРАТЬ * ИЗ рег_остатки`,
	} {
		for _, dialect := range []storage.Dialect{storage.SQLiteDialect{}, storage.PgDialect{}} {
			res, err := query.Compile(text, query.CompileOpts{Dialect: dialect, Entities: rawSourceEntities()})
			if err == nil {
				t.Errorf("%s (%s): запрос скомпилирован, ожидался отказ\nSQL: %s", text, dialect.Name(), res.SQL)
				continue
			}
			if !strings.Contains(err.Error(), "не объект конфигурации") {
				t.Errorf("%s (%s): ошибка не называет причину: %v", text, dialect.Name(), err)
			}
		}
	}
}

func TestQueryBareEntitySourceIsRegistered(t *testing.T) {
	заявка := query.SourceRef{Kind: "document", Name: "Заявка"}
	клиент := query.SourceRef{Kind: "catalog", Name: "КлиентИст"}
	for _, c := range []struct {
		text string
		want []query.SourceRef
	}{
		{`ВЫБРАТЬ Номер ИЗ Заявка`, []query.SourceRef{заявка}},
		{`ВЫБРАТЬ Номер ИЗ заявка`, []query.SourceRef{заявка}},
		{`ВЫБРАТЬ З.Номер ИЗ Заявка КАК З, КлиентИст К ГДЕ К.Ссылка = З.Клиент`, []query.SourceRef{заявка, клиент}},
		{`ВЫБРАТЬ З.Номер ИЗ Документ.Заявка КАК З ЛЕВОЕ СОЕДИНЕНИЕ КлиентИст КАК К ПО К.Ссылка = З.Клиент`, []query.SourceRef{заявка, клиент}},
		{`ВЫБРАТЬ Т.Номер ИЗ (ВЫБРАТЬ Номер ИЗ Заявка) КАК Т`, []query.SourceRef{заявка}},
		// FROM внутри EXTRACT — часть выражения, а не место источника.
		{`ВЫБРАТЬ EXTRACT(YEAR FROM Дата) КАК Г ИЗ Документ.Заявка`, []query.SourceRef{заявка}},
	} {
		res, err := query.Compile(c.text, query.CompileOpts{Dialect: storage.SQLiteDialect{}, Entities: rawSourceEntities()})
		if err != nil {
			t.Errorf("%s: компиляция: %v", c.text, err)
			continue
		}
		for _, w := range c.want {
			found := false
			for _, s := range res.Sources {
				if s == w {
					found = true
				}
			}
			if !found {
				t.Errorf("%s: источник %+v не зарегистрирован, Sources = %+v", c.text, w, res.Sources)
			}
		}
	}
}

// Краткая форма исполняется как раньше — на обоих диалектах.
func TestQueryBareEntitySourceExecutes(t *testing.T) {
	dbtest.ForEachDialect(t, func(t *testing.T, db *storage.DB) {
		ctx := context.Background()
		ents := rawSourceEntities()
		if err := db.Migrate(ctx, ents); err != nil {
			t.Fatalf("Migrate: %v", err)
		}
		if err := db.Upsert(ctx, "Заявка", uuid.New(), map[string]any{"Номер": "0001"}, ents[0]); err != nil {
			t.Fatalf("Upsert: %v", err)
		}
		compiled, err := query.Compile(`ВЫБРАТЬ Номер ИЗ Заявка`, query.CompileOpts{Dialect: db.Dialect(), Entities: ents})
		if err != nil {
			t.Fatalf("компиляция: %v", err)
		}
		rows, _, err := query.Run(ctx, db, &compiled)
		if err != nil {
			t.Fatalf("выполнение: %v\nSQL: %s", err, compiled.SQL)
		}
		if len(rows) != 1 || rows[0]["номер"] != "0001" {
			t.Fatalf("ожидалась одна строка с номером 0001, получено %v", rows)
		}
	})
}

// Строковый доступ внедряется только в полную форму источника: голое имя под
// политикой отклоняется с подсказкой, а не отдаёт чужие строки.
func TestQueryBareEntitySourceUnderRowAccess(t *testing.T) {
	filters := map[query.SourceRef]*storage.Predicate{
		{Kind: "document", Name: "Заявка"}: {Field: "Номер", Op: "eq", Value: "0001"},
	}
	_, err := query.Compile(`ВЫБРАТЬ Номер ИЗ Заявка`, query.CompileOpts{
		Dialect: storage.SQLiteDialect{}, Entities: rawSourceEntities(), RowFilters: filters,
	})
	if err == nil || !strings.Contains(err.Error(), "Документ.Заявка") {
		t.Fatalf("ожидался отказ с подсказкой полной формы, получено %v", err)
	}
	if _, err := query.Compile(`ВЫБРАТЬ Номер ИЗ Документ.Заявка`, query.CompileOpts{
		Dialect: storage.SQLiteDialect{}, Entities: rawSourceEntities(), RowFilters: filters,
	}); err != nil {
		t.Fatalf("полная форма под строковым доступом должна компилироваться: %v", err)
	}
}
