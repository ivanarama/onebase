package rowsource

import (
	"context"
	"reflect"
	"strings"
	"testing"

	"github.com/ivantit66/onebase/internal/report"
	"github.com/ivantit66/onebase/internal/report/compose"
	"github.com/ivantit66/onebase/internal/runtime"
	"github.com/ivantit66/onebase/internal/storage"
	"github.com/shopspring/decimal"
)

func subsetFixture(t *testing.T) (context.Context, *storage.DB, *runtime.Registry, CodeLookup) {
	t.Helper()
	ctx, db, reg, _, _, _ := fixture(t)
	rep := reg.GetReport("RowSource")
	rep.RowSource = registeredDefinition()
	rep.RowSource.Inputs = []string{"request", "NOTE"}
	return ctx, db, reg, func(context.Context, string) (string, error) { return enrichCode, nil }
}
func TestRowSourceSubsetKeepsOriginalsHostOnly(t *testing.T) {
	for _, path := range []string{"registered", "captured"} {
		t.Run(path, func(t *testing.T) {
			ctx, db, reg, load := subsetFixture(t)
			definition := reg.GetReport("RowSource").RowSource
			selected := definition.Inputs
			definition.Inputs = nil
			before, err := RunRegistered(ctx, db, reg, "RowSource", Query{Text: initialQuery}, load)
			definition.Inputs = selected
			if err != nil {
				t.Fatal(err)
			}
			var result *Result
			if path == "registered" {
				result, err = RunRegistered(ctx, db, reg, "RowSource", Query{Text: initialQuery}, load)
			} else {
				var frame *InputFrame
				frame, err = CaptureInputFrame(ctx, db, reg, "RowSource", InputSelection{}, InputSelection{}, load)
				if err == nil {
					result, err = RunCapturedInputs(ctx, db, reg, frame)
				}
			}
			if err != nil {
				t.Fatal(err)
			}
			if len(result.Columns) != 6 || len(result.Rows) != 1 || result.Rows[0]["Works"] != "Работа А; \nРабота А; \nЗаметка для клиента" {
				t.Fatal("enrichment/output shape changed")
			}
			amount, ok := compose.ExportToDecimal(result.Rows[0]["amount"])
			if !ok || !amount.Equal(decimal.RequireFromString("125.50")) || !reflect.DeepEqual(before.Rows[0]["amount"], result.Rows[0]["amount"]) {
				t.Fatal("host original type/value changed")
			}
		})
	}
}
func TestRowSourceSubsetAbsentFieldIsNotInDSL(t *testing.T) {
	ctx, db, reg, _ := subsetFixture(t)
	code := `Функция Build() Экспорт
 Если Результат[0].Свойство("Amount") Тогда Возврат Новый Массив;КонецЕсли;
 Если Результат[0].Свойство("Moment") Тогда Возврат Новый Массив;КонецЕсли;
 Результат[0].Вставить("Works",Результат[0].Note);Возврат Результат;КонецФункции`
	result, err := RunRegistered(ctx, db, reg, "RowSource", Query{Text: initialQuery}, func(context.Context, string) (string, error) { return code, nil })
	if err != nil || result == nil {
		t.Fatal("unselected original leaked into DSL", err)
	}
	if _, present := result.Rows[0]["amount"]; !present {
		t.Fatal("host original disappeared")
	}
}
func TestRowSourceSubsetRejectsReplacementOfHostOriginal(t *testing.T) {
	ctx, db, reg, _ := subsetFixture(t)
	code := `Функция Build() Экспорт Результат[0].Вставить("Amount",0);Результат[0].Вставить("Works","");Возврат Результат;КонецФункции`
	result, err := RunRegistered(ctx, db, reg, "RowSource", Query{Text: initialQuery}, func(context.Context, string) (string, error) { return code, nil })
	if err == nil || result != nil {
		t.Fatal("DSL replaced host-only original")
	}
}
func TestRowSourceSubsetDoesNotPermitUnknownOrProtectedOriginals(t *testing.T) {
	for _, mode := range []string{"protected", "unknown expression", "unknown input", "duplicate input", "empty input", "internal input", "extra collision"} {
		t.Run(mode, func(t *testing.T) {
			ctx, db, reg, load := subsetFixture(t)
			rep := reg.GetReport("RowSource")
			switch mode {
			case "protected":
				rep.Query = strings.Replace(initialQuery, "Р.Note КАК Note", "Р.Note КАК Note,Р.Hidden КАК Hidden", 1)
			case "unknown expression":
				rep.Query = strings.Replace(initialQuery, "Р.Count КАК Count", "1 КАК Count", 1)
			case "unknown input":
				rep.RowSource.Inputs = []string{"not_a_column"}
			case "duplicate input":
				rep.RowSource.Inputs = []string{"Request", "REQUEST"}
			case "empty input":
				rep.RowSource.Inputs = []string{}
			case "internal input":
				rep.RowSource.Inputs = []string{"__secret"}
			case "extra collision":
				rep.RowSource.Columns = []report.RowSourceColumn{{Name: "Amount", Type: "number"}}
			}
			result, err := RunRegistered(ctx, db, reg, "RowSource", Query{Text: rep.Query}, load)
			if err == nil || result != nil {
				t.Fatal("subset bypassed proof/policy/schema")
			}
		})
	}
}
func TestRowSourceSubsetRetainsPrivateOrdinalWithDuplicateRefs(t *testing.T) {
	ctx, db, reg, load := subsetFixture(t)
	rep := reg.GetReport("RowSource")
	rep.Query = "ВЫБРАТЬ Р.Ссылка КАК Request,Р.Note КАК Note,Т.Pos КАК Pos ИЗ Документ.RowRequest КАК Р ВНУТРЕННЕЕ СОЕДИНЕНИЕ Документ.RowRequest.Works КАК Т ПО Р.Ссылка=Т.Ссылка УПОРЯДОЧИТЬ ПО Т.Pos"
	result, err := RunRegistered(ctx, db, reg, "RowSource", Query{Text: rep.Query}, load)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Rows) != 2 || result.Rows[0]["request"] != result.Rows[1]["request"] || result.Rows[0]["pos"] == result.Rows[1]["pos"] {
		t.Fatal("host originals deduplicated or reordered")
	}
}
func TestRowSourceSubsetCapturedMetadataCannotChange(t *testing.T) {
	ctx, db, reg, load := subsetFixture(t)
	frame, err := CaptureInputFrame(ctx, db, reg, "RowSource", InputSelection{}, InputSelection{}, load)
	if err != nil {
		t.Fatal(err)
	}
	reg.GetReport("RowSource").RowSource.Inputs = append(reg.GetReport("RowSource").RowSource.Inputs, "Amount")
	result, err := RunCapturedInputs(ctx, db, reg, frame)
	if err == nil || result != nil {
		t.Fatal("stale subset metadata accepted")
	}
}
