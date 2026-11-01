package query_test

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/ivantit66/onebase/internal/dbtest"
	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/ivantit66/onebase/internal/query"
	"github.com/ivantit66/onebase/internal/storage"
)

// Табличная часть как источник запроса, как в 1С: «ИЗ Справочник.Мастера.
// СписокНаправлений КАК С». Ссылка источника — владелец строки, НомерСтроки —
// номер строки, реквизиты ТЧ — как поля. Без этого «мастера направления»
// приходилось собирать загрузкой каждой карточки целиком (конфигурация cc2:
// подбор мастеров при распределении заявки — 5–6 секунд на 661 мастере).
//
// Матрица: источник — подзапрос с переименованием колонок, а строковая
// политика владельца уходит в него через родителя; расхождение диалектов
// здесь — ровно то, что матрица обязана ловить.
func TestТабличнаяЧастьКакИсточникЗапроса(t *testing.T) {
	dbtest.ForEachDialect(t, func(t *testing.T, db *storage.DB) {
		ctx := context.Background()
		направления := &metadata.Entity{
			Name: "ТЧНаправления", Kind: metadata.KindCatalog,
			Fields: []metadata.Field{{Name: "Наименование", Type: metadata.FieldTypeString}},
		}
		тч := metadata.TablePart{Name: "СписокНаправлений", Fields: []metadata.Field{
			{Name: "Направление", Type: "reference:ТЧНаправления", RefEntity: "ТЧНаправления"},
			{Name: "Основное", Type: metadata.FieldTypeBool},
		}}
		мастера := &metadata.Entity{
			Name: "ТЧМастера", Kind: metadata.KindCatalog,
			Fields: []metadata.Field{
				{Name: "Наименование", Type: metadata.FieldTypeString},
				{Name: "Филиал", Type: metadata.FieldTypeString},
			},
			TableParts: []metadata.TablePart{тч},
		}
		сущности := []*metadata.Entity{направления, мастера}
		if err := db.Migrate(ctx, сущности); err != nil {
			t.Fatalf("миграция: %v", err)
		}
		холод, стирка := uuid.New(), uuid.New()
		for ид, имя := range map[uuid.UUID]string{холод: "Холодильники", стирка: "Стиральные"} {
			if err := db.Upsert(ctx, направления.Name, ид, map[string]any{"Наименование": имя}, направления); err != nil {
				t.Fatal(err)
			}
		}
		мастер := func(имя, филиал string, напр ...uuid.UUID) uuid.UUID {
			t.Helper()
			ид := uuid.New()
			if err := db.Upsert(ctx, мастера.Name, ид, map[string]any{"Наименование": имя, "Филиал": филиал}, мастера); err != nil {
				t.Fatal(err)
			}
			var строки []map[string]any
			for i, н := range напр {
				строки = append(строки, map[string]any{"Направление": н.String(), "Основное": i == 0})
			}
			if err := db.UpsertTablePartRows(ctx, мастера.Name, тч.Name, ид, строки, тч); err != nil {
				t.Fatal(err)
			}
			return ид
		}
		иванов := мастер("Иванов", "МСК", холод, стирка)
		петров := мастер("Петров", "СПБ", стирка, холод)
		мастер("Сидоров", "МСК", стирка)

		прогнать := func(t *testing.T, текст string, фильтры map[query.SourceRef]*storage.Predicate) ([]map[string]any, query.Result) {
			t.Helper()
			res, err := query.Compile(текст, query.CompileOpts{
				Dialect: db.Dialect(), Entities: сущности,
				Params: map[string]any{"Направление": холод.String()}, RowFilters: фильтры,
			})
			if err != nil {
				t.Fatalf("компиляция %q: %v", текст, err)
			}
			строки, _, err := db.RunQuery(ctx, res.SQL, res.Args)
			if err != nil {
				t.Fatalf("выполнение %s: %v", res.SQL, err)
			}
			return строки, res
		}
		значения := func(строки []map[string]any, колонка string) []string {
			var out []string
			for _, r := range строки {
				out = append(out, fmt.Sprint(r[колонка]))
			}
			sort.Strings(out)
			return out
		}
		сверить := func(t *testing.T, получено, ожидается []string) {
			t.Helper()
			sort.Strings(ожидается)
			if strings.Join(получено, "|") != strings.Join(ожидается, "|") {
				t.Fatalf("получено %v, ожидалось %v", получено, ожидается)
			}
		}

		t.Run("Ссылка источника — владелец, НомерСтроки — номер строки", func(t *testing.T) {
			строки, res := прогнать(t, `ВЫБРАТЬ С.Ссылка КАК Мастер, С.НомерСтроки КАК Номер
				ИЗ Справочник.ТЧМастера.СписокНаправлений КАК С
				ГДЕ С.Направление = &Направление`, nil)
			сверить(t, значения(строки, "мастер"), []string{иванов.String(), петров.String()})
			номера := map[string]string{}
			for _, r := range строки {
				номера[fmt.Sprint(r["мастер"])] = fmt.Sprint(r["номер"])
			}
			if номера[иванов.String()] != "1" || номера[петров.String()] != "2" {
				t.Fatalf("номер строки: %v", номера)
			}
			найден := false
			for _, src := range res.Sources {
				if src.Kind == "catalog" && src.Name == мастера.Name && src.TablePart == тч.Name {
					найден = true
				}
			}
			if !найден {
				t.Fatalf("источник для прав — владелец с табличной частью: %+v", res.Sources)
			}
		})

		t.Run("соединение с владельцем", func(t *testing.T) {
			строки, _ := прогнать(t, `ВЫБРАТЬ РАЗЛИЧНЫЕ М.Наименование КАК Имя
				ИЗ Справочник.ТЧМастера.СписокНаправлений КАК С
				ВНУТРЕННЕЕ СОЕДИНЕНИЕ Справочник.ТЧМастера КАК М ПО М.Ссылка = С.Ссылка
				ГДЕ С.Направление = &Направление`, nil)
			сверить(t, значения(строки, "имя"), []string{"Иванов", "Петров"})
		})

		t.Run("реквизит ссылки табличной части через точку", func(t *testing.T) {
			строки, _ := прогнать(t, `ВЫБРАТЬ С.Направление.Наименование КАК Направление
				ИЗ Справочник.ТЧМастера.СписокНаправлений КАК С
				ГДЕ С.Основное = ИСТИНА`, nil)
			сверить(t, значения(строки, "направление"), []string{"Стиральные", "Стиральные", "Холодильники"})
		})

		t.Run("строковая политика владельца отбирает строки ТЧ", func(t *testing.T) {
			фильтры := map[query.SourceRef]*storage.Predicate{
				{Kind: "catalog", Name: мастера.Name}: {Field: "Филиал", Op: "eq", Value: "МСК"},
			}
			строки, _ := прогнать(t, `ВЫБРАТЬ С.Ссылка КАК Мастер
				ИЗ Справочник.ТЧМастера.СписокНаправлений КАК С
				ГДЕ С.Направление = &Направление`, фильтры)
			сверить(t, значения(строки, "мастер"), []string{иванов.String()})
		})
	})
}
