package interpreter_test

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
	"github.com/ivantit66/onebase/internal/query"
	"github.com/ivantit66/onebase/internal/storage"
)

type infoRefQueryRegistry struct {
	entityReg
	infoRegs []*metadata.InfoRegister
}

func (r *infoRefQueryRegistry) InfoRegisters() []*metadata.InfoRegister { return r.infoRegs }

// Both public consumers must agree: query.Run exposes logical column names,
// and the DSL keeps bare references as presentations and .Ссылка as typed refs.
func TestQueryInfoRegisterReferencesMatrix(t *testing.T) {
	dbtest.ForEachDialect(t, func(t *testing.T, db *storage.DB) {
		for _, periodic := range []bool{true, false} {
			t.Run(fmt.Sprintf("periodic=%t", periodic), func(t *testing.T) {
				ctx := context.Background()
				entities := []*metadata.Entity{
					{Name: "Филиал", Kind: metadata.KindCatalog, Fields: []metadata.Field{{ID: "branch_name", Name: "Наименование", Type: metadata.FieldTypeString}}},
					{Name: "Реклама", Kind: metadata.KindCatalog, Fields: []metadata.Field{{ID: "ad_name", Name: "Наименование", Type: metadata.FieldTypeString}}},
					{Name: "Заявка", Kind: metadata.KindDocument, Fields: []metadata.Field{{ID: "doc_number", Name: "Номер", Type: metadata.FieldTypeString}}},
				}
				ir := &metadata.InfoRegister{
					Name: fmt.Sprintf("НомераРекламы%t", periodic), Periodic: periodic,
					Dimensions: []metadata.Field{
						{Name: "НомерТелефона", Type: metadata.FieldTypeString},
						{Name: "Филиал", Type: "reference:Филиал", RefEntity: "Филиал"},
					},
					Resources: []metadata.Field{
						{Name: "Реклама", Type: "reference:Реклама", RefEntity: "Реклама"},
						{Name: "Основание", Type: "reference:Заявка", RefEntity: "Заявка"},
					},
				}
				require.NoError(t, db.Migrate(ctx, entities))
				require.NoError(t, db.MigrateInfoRegisters(ctx, []*metadata.InfoRegister{ir}))
				branchID, oldAdID, newAdID, docID := uuid.New(), uuid.New(), uuid.New(), uuid.New()
				require.NoError(t, db.Upsert(ctx, "Филиал", branchID, map[string]any{"Наименование": "МСК"}, entities[0]))
				require.NoError(t, db.Upsert(ctx, "Реклама", oldAdID, map[string]any{"Наименование": "Старая"}, entities[1]))
				require.NoError(t, db.Upsert(ctx, "Реклама", newAdID, map[string]any{"Наименование": "Новая"}, entities[1]))
				require.NoError(t, db.Upsert(ctx, "Заявка", docID, map[string]any{"Номер": "З-1"}, entities[2]))
				oldPeriod := time.Date(2026, 10, 6, 12, 0, 0, 0, time.UTC)
				newPeriod := oldPeriod.Add(time.Hour)
				var period *time.Time
				if periodic {
					period = &oldPeriod
				}
				dims := map[string]any{"НомерТелефона": "495", "Филиал": branchID.String()}
				if periodic {
					require.NoError(t, db.InfoRegSet(ctx, ir, dims, map[string]any{"Реклама": oldAdID.String(), "Основание": docID.String()}, period))
					period = &newPeriod
				}
				require.NoError(t, db.InfoRegSet(ctx, ir, dims, map[string]any{"Реклама": newAdID.String(), "Основание": docID.String()}, period))
				require.NoError(t, db.InfoRegSet(ctx, ir, map[string]any{"НомерТелефона": "empty", "Филиал": branchID.String()}, map[string]any{}, period))
				registry := &infoRefQueryRegistry{entityReg: entityReg{entities: entities}, infoRegs: []*metadata.InfoRegister{ir}}
				for _, source := range []string{"", ".СрезПоследних()", ".СрезПервых()"} {
					t.Run("source="+source, func(t *testing.T) {
						from := "РегистрСведений." + ir.Name + source
						where := " ГДЕ НомерТелефона = &Н"
						if source == "" && periodic {
							where += " И Реклама = &Р"
						}
						adName, adID := "Новая", newAdID
						if periodic && source == ".СрезПервых()" {
							adName, adID = "Старая", oldAdID
						}
						opts := query.CompileOpts{Entities: entities, InfoRegs: registry.infoRegs, Dialect: db.Dialect(), Params: map[string]any{"Н": "495", "Р": newAdID.String()}}
						for _, projection := range []string{
							"Филиал, Реклама, Основание",
							"Филиал КАК Филиал, Реклама КАК Реклама, Основание КАК Основание",
						} {
							compiled, err := query.Compile("ВЫБРАТЬ "+projection+" ИЗ "+from+where, opts)
							require.NoError(t, err)
							rows, _, err := query.Run(ctx, db, &compiled)
							require.NoError(t, err, compiled.SQL)
							require.Len(t, rows, 1)
							assert.Equal(t, "МСК", rows[0]["филиал"])
							assert.Equal(t, adName, rows[0]["реклама"])
							assert.Equal(t, "З-1", rows[0]["основание"])
							assert.Empty(t, compiled.RefColumns, "presentations must not become refs")
						}
						if periodic && source != "" {
							// A bound between the two records selects different rows in
							// the last/first slice, including their reference resources.
							boundedOpts := opts
							boundedOpts.Params = map[string]any{"Н": "495", "НаДату": oldPeriod.Add(30 * time.Minute)}
							boundedFrom := strings.TrimSuffix(from, "()") + "(&НаДату)"
							compiled, err := query.Compile("ВЫБРАТЬ Реклама ИЗ "+boundedFrom+where, boundedOpts)
							require.NoError(t, err)
							rows, _, err := query.Run(ctx, db, &compiled)
							require.NoError(t, err, compiled.SQL)
							require.Len(t, rows, 1)
							boundedWant := "Старая"
							if source == ".СрезПервых()" {
								boundedWant = "Новая"
							}
							assert.Equal(t, boundedWant, rows[0]["реклама"])
						}
						projection := "Филиал, Реклама, Основание, Реклама.Ссылка КАК РекСсылка, Основание.Ссылка КАК ДокСсылка"
						src := `Процедура Тест()
       Запрос = Новый Запрос;
       Запрос.Текст = "ВЫБРАТЬ ` + projection + ` ИЗ ` + from + where + `";
       Запрос.УстановитьПараметр("Н", "495");
       Запрос.УстановитьПараметр("Р", "` + adID.String() + `");
       Стр = Запрос.Выполнить()[0];
       Возврат ТипЗнч(Стр.Филиал) + "|" + Стр.Филиал
        + "|" + ТипЗнч(Стр.Реклама) + "|" + Стр.Реклама
        + "|" + Стр.Основание
        + "|" + ТипЗнч(Стр.РекСсылка) + "|" + Строка(Стр.РекСсылка = "` + adID.String() + `")
        + "|" + ТипЗнч(Стр.ДокСсылка) + "|" + Строка(Стр.ДокСсылка = "` + docID.String() + `");
      КонецПроцедуры`
						assert.Equal(t, "Строка|МСК|Строка|"+adName+"|З-1|СправочникСсылка.Реклама|true|ДокументСсылка.Заявка|true", evalQuery(t, src, db, registry))
						emptySrc := `Процедура Тест()
       Запрос = Новый Запрос;
       Запрос.Текст = "ВЫБРАТЬ Реклама, Реклама.Ссылка КАК РекСсылка ИЗ ` + from + ` ГДЕ НомерТелефона = &Н";
       Запрос.УстановитьПараметр("Н", "empty");
       Стр = Запрос.Выполнить()[0];
       Возврат ТипЗнч(Стр.Реклама) + "|" + Строка(Стр.Реклама = "")
        + "|" + ТипЗнч(Стр.РекСсылка) + "|" + Строка(ПустаяСсылка(Стр.РекСсылка));
      КонецПроцедуры`
						// Ordinary projections apply DSL typed-empty normalization; slices
						// preserve their existing SQL NULL / Undefined behavior.
						emptyWant := "Строка|true|СправочникСсылка.Реклама|true"
						if source != "" {
							emptyWant = "Неопределено|false|Неопределено|true"
						}
						assert.Equal(t, emptyWant, evalQuery(t, emptySrc, db, registry))
					})
				}
			})
		}
	})
}
