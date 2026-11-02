package interpreter_test

// Сортировка по ссылке в сгруппированном запросе (матрица SQLite/PostgreSQL).
//
// «СГРУППИРОВАТЬ ПО Р.Номенклатура УПОРЯДОЧИТЬ ПО Номенклатура»: группировка
// идёт по колонке ссылки, а сортировка — по её представлению из авто-JOIN
// (ref_номенклатура.наименование). Этой колонки в GROUP BY нет, и PostgreSQL
// отвергал запрос: column must appear in the GROUP BY clause (42803). SQLite
// такой запрос молча принимает — отсюда матрица. Найдено на отчёте «По штрафам»
// конфигурации колл-центра при пробе выпуска на PostgreSQL.

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/ivantit66/onebase/internal/dbtest"
	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/ivantit66/onebase/internal/storage"
)

func TestQueryGroupedOrderByRefPresentation(t *testing.T) {
	dbtest.ForEachDialect(t, func(t *testing.T, db *storage.DB) {
		ctx := context.Background()
		ents := refQueryEntities()
		ents[1].Fields = append(ents[1].Fields, metadata.Field{ID: "d_date", Name: "Дата", Type: metadata.FieldTypeDate})
		require.NoError(t, db.Migrate(ctx, ents))

		bolt, anchor := uuid.New(), uuid.New()
		require.NoError(t, db.Upsert(ctx, "Номенклатура", bolt, map[string]any{"Наименование": "Болт"}, ents[0]))
		require.NoError(t, db.Upsert(ctx, "Номенклатура", anchor, map[string]any{"Наименование": "Анкер"}, ents[0]))
		when := time.Date(2026, 10, 1, 10, 0, 0, 0, time.Local)
		for _, nom := range []uuid.UUID{bolt, bolt, anchor} {
			require.NoError(t, db.Upsert(ctx, "РасходТовара", uuid.New(),
				map[string]any{"Номер": "1", "Номенклатура": nom.String(), "Дата": when}, ents[1]))
		}

		result := runOnDBEntities(t, db, ents, `
Функция Тест()
	З = Новый Запрос;
	З.Текст = "ВЫБРАТЬ ГОД(Р.Дата) КАК Год, Р.Номенклатура КАК Номенклатура, КОЛИЧЕСТВО(*) КАК Ч
	|ИЗ Документ.РасходТовара КАК Р
	|СГРУППИРОВАТЬ ПО ГОД(Р.Дата), Р.Номенклатура
	|УПОРЯДОЧИТЬ ПО Год, Номенклатура";
	Итог = "";
	Для Каждого С Из З.Выполнить() Цикл
		Итог = Итог + Строка(С.Номенклатура) + "=" + Строка(С.Ч) + ";";
	КонецЦикла;
	Возврат Итог;
КонецФункции
`)
		// «Анкер» раньше «Болта» — порядок по представлению, а не по UUID.
		assert.Equal(t, anchor.String()+"=1;"+bolt.String()+"=2;", result, "сортировка по представлению ссылки в сгруппированной выборке")
	})
}

// GROUP BY belongs to its SELECT, even when an outer expression reuses the
// child's parenthesis depth or the grouped SELECT orders inside an expression.
func TestQueryGroupedOrderByRefSelectScope(t *testing.T) {
	dbtest.ForEachDialect(t, func(t *testing.T, db *storage.DB) {
		ctx := context.Background()
		ents := refQueryEntities()
		require.NoError(t, db.Migrate(ctx, ents))
		bolt, anchor := uuid.New(), uuid.New()
		require.NoError(t, db.Upsert(ctx, "Номенклатура", bolt, map[string]any{"Наименование": "Болт"}, ents[0]))
		require.NoError(t, db.Upsert(ctx, "Номенклатура", anchor, map[string]any{"Наименование": "Анкер"}, ents[0]))
		docs := []uuid.UUID{uuid.New(), uuid.New(), uuid.New()}
		for i, nom := range []uuid.UUID{bolt, bolt, anchor} {
			require.NoError(t, db.Upsert(ctx, "РасходТовара", docs[i],
				map[string]any{"Номер": fmt.Sprint(i + 1), "Номенклатура": nom.String()}, ents[1]))
		}
		for _, groupedChild := range []bool{false, true} {
			for _, order := range []string{"Номенклатура", "(Номенклатура)", `ЕСТЬNULL(Номенклатура, "")`} {
				t.Run(fmt.Sprintf("child_grouped=%t/order=%s", groupedChild, order), func(t *testing.T) {
					child := "ВЫБРАТЬ Д.Номенклатура ИЗ Документ.РасходТовара КАК Д"
					if groupedChild {
						child += " СГРУППИРОВАТЬ ПО Д.Номенклатура"
					}
					query := "ВЫБРАТЬ Р.Ссылка КАК Док ИЗ Документ.РасходТовара КАК Р ГДЕ Р.Номенклатура В (" + child + ") УПОРЯДОЧИТЬ ПО " + order + ", Р.Номер"
					result := runOnDBEntities(t, db, ents, fmt.Sprintf(`
Функция Тест()
 З = Новый Запрос;
 З.Текст = "%s";
 Итог = "";
 Для Каждого С Из З.Выполнить() Цикл
  Итог = Итог + Строка(С.Док) + ";";
 КонецЦикла;
 Возврат Итог;
КонецФункции
`, strings.ReplaceAll(query, `"`, `""`)))
					assert.Equal(t, docs[2].String()+";"+docs[0].String()+";"+docs[1].String()+";", result)
				})
			}
		}
		for _, order := range []string{"Номенклатура", "(Номенклатура)", `ЕСТЬNULL(Номенклатура, "")`} {
			t.Run("grouped_outer/order="+order, func(t *testing.T) {
				query := "ВЫБРАТЬ Р.Номенклатура КАК Номенклатура, КОЛИЧЕСТВО(*) КАК Ч ИЗ Документ.РасходТовара КАК Р СГРУППИРОВАТЬ ПО Р.Номенклатура УПОРЯДОЧИТЬ ПО " + order
				result := runOnDBEntities(t, db, ents, fmt.Sprintf(`
Функция Тест()
 З = Новый Запрос;
 З.Текст = "%s";
 Итог = "";
 Для Каждого С Из З.Выполнить() Цикл
  Итог = Итог + Строка(С.Номенклатура) + "=" + Строка(С.Ч) + ";";
 КонецЦикла;
 Возврат Итог;
КонецФункции
`, strings.ReplaceAll(query, `"`, `""`)))
				assert.Equal(t, anchor.String()+"=1;"+bolt.String()+"=2;", result)
			})
		}
	})
}

// Explicit aggregates keep their argument; only references outside an aggregate
// need the implicit MIN used for grouped ORDER BY.
func TestQueryGroupedOrderByRefExplicitAggregates(t *testing.T) {
	dbtest.ForEachDialect(t, func(t *testing.T, db *storage.DB) {
		ctx := context.Background()
		ents := refQueryEntities()
		require.NoError(t, db.Migrate(ctx, ents))
		bolt, anchor, nail := uuid.New(), uuid.New(), uuid.New()
		require.NoError(t, db.Upsert(ctx, "Номенклатура", bolt, map[string]any{"Наименование": "Болт"}, ents[0]))
		require.NoError(t, db.Upsert(ctx, "Номенклатура", anchor, map[string]any{"Наименование": "Анкер"}, ents[0]))
		require.NoError(t, db.Upsert(ctx, "Номенклатура", nail, map[string]any{"Наименование": "Гвоздь"}, ents[0]))
		// Group 1 spans both sides of group 2: MIN and MAX must give
		// different orders, while COUNT must keep the two-row group last.
		for i, nom := range []uuid.UUID{anchor, nail, bolt} {
			require.NoError(t, db.Upsert(ctx, "РасходТовара", uuid.New(),
				map[string]any{"Номер": []string{"1", "1", "2"}[i], "Номенклатура": nom.String()}, ents[1]))
		}
		for _, tc := range []struct{ order, want string }{
			{"МИНИМУМ(Номенклатура)", "1;2;"},
			{"МАКСИМУМ(Номенклатура)", "2;1;"},
			{"КОЛИЧЕСТВО(Номенклатура)", "2;1;"},
			{"МИН(Номенклатура)", "1;2;"},
			{"МАКС(Номенклатура)", "2;1;"},
			{"min(Номенклатура)", "1;2;"},
			{"MAX(Номенклатура)", "2;1;"},
			{"COUNT(РАЗЛИЧНЫЕ Номенклатура)", "2;1;"},
			{"МИНИМУМ((Номенклатура))", "1;2;"},
			{`МИНИМУМ(ЕСТЬNULL(Номенклатура, ""))`, "1;2;"},
			{`ЕСТЬNULL(МИНИМУМ(Номенклатура), "")`, "1;2;"},
			{`ЕСТЬNULL(МИНИМУМ(Номенклатура), Номенклатура)`, "1;2;"},
			// A closed aggregate must not suppress the implicit MIN for the next key.
			{`КОЛИЧЕСТВО(Номенклатура), ЕСТЬNULL(Номенклатура, "")`, "2;1;"},
			{`КОЛИЧЕСТВО(Номенклатура), (Номенклатура)`, "2;1;"},
			// The child SELECT still needs its own implicit MIN, even inside
			// an outer aggregate; its closing parenthesis restores the parent.
			{`МИНИМУМ((ВЫБРАТЬ МАКСИМУМ(Д.Номер) ИЗ Документ.РасходТовара КАК Д СГРУППИРОВАТЬ ПО Д.ПометкаУдаления УПОРЯДОЧИТЬ ПО Номенклатура)), Номенклатура`, "1;2;"},
		} {
			t.Run(tc.order, func(t *testing.T) {
				text := "ВЫБРАТЬ Р.Номер КАК Номер, КОЛИЧЕСТВО(*) КАК Ч ИЗ Документ.РасходТовара КАК Р СГРУППИРОВАТЬ ПО Р.Номер УПОРЯДОЧИТЬ ПО " + tc.order + ", Р.Номер"
				src := fmt.Sprintf(`Функция Тест()
 З = Новый Запрос;
 З.Текст = "%s";
 Итог = "";
 Для Каждого С Из З.Выполнить() Цикл
  Итог = Итог + С.Номер + ";";
 КонецЦикла;
 Возврат Итог;
КонецФункции`, strings.ReplaceAll(text, `"`, `""`))
				assert.Equal(t, tc.want, runOnDBEntities(t, db, ents, src))
			})
		}
	})
}
