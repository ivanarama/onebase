package rowsource

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/ivantit66/onebase/internal/dsl/interpreter"
)

const traceSQL = "ВЫБРАТЬ Р.Note КАК Note ИЗ Документ.RowRequest КАК Р"

func runTextTrace(t *testing.T, body, helpers string) *Result {
	t.Helper()
	ctx, db, reg, _, src, _ := fixture(t)
	src.Code = helpers + "\nФункция Build() Экспорт\n" + body + "\nРезультат[0].Вставить(\"Works\",\"ok\");Возврат Результат;КонецФункции"
	result, err := Run(ctx, db, reg, Query{Text: initialQuery}, src)
	if err != nil {
		t.Fatal(err)
	}
	if result.queryEvidence == nil {
		t.Fatal("missing actual runtime evidence")
	}
	return result
}
func TestRowSourceRuntimeTextRequiresActualLexicalAssignment(t *testing.T) {
	for _, tc := range []struct {
		name, body string
		lexical    bool
	}{
		{"literal", `Q=Новый Запрос;Q.Text="` + traceSQL + `";Q.Execute();`, true},
		{"same variable", `S="` + traceSQL + `";Q=Новый Запрос;Q.Text=S;Q.Execute();`, false},
		{"concat", `Q=Новый Запрос;Q.Text="` + traceSQL + `"+"";Q.Execute();`, false},
		{"function return", `Q=Новый Запрос;Q.Text=TextValue();Q.Execute();`, false},
		{"compound invalidates", `Q=Новый Запрос;Q.Text="` + traceSQL + `";Q.Text+="";Q.Execute();`, false},
		{"branch keeps control", `Q=Новый Запрос;Если Результат.Количество()>0 Тогда Q.Text="` + traceSQL + `";КонецЕсли;Q.Execute();`, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			result := runTextTrace(t, tc.body, `Функция TextValue() Возврат "`+traceSQL+`";КонецФункции`)
			reads := result.queryEvidence.reads
			if len(reads) != 1 {
				t.Fatalf("reads=%d", len(reads))
			}
			b := reads[0].binding
			if (b.lexical != nil) != tc.lexical || !b.controlRequired || b.dataRequired == tc.lexical {
				t.Fatal("unproven text/control became a literal")
			}
			if b.object <= 0 || b.version <= 0 || tc.lexical && b.site <= 0 {
				t.Fatal("missing actual receiver/site binding")
			}
		})
	}
}
func TestRowSourceRuntimeTextSeparatesInstancesAndVersions(t *testing.T) {
	result := runTextTrace(t, `Q=Новый Запрос;Q.Text="`+traceSQL+`";Q.Execute();
 R=Новый Запрос;S=Q.Text;R.Text=S;R.Execute();Q.Text=S;Q.Execute();`, "")
	r := result.queryEvidence.reads
	if len(r) != 3 || r[0].binding.lexical == nil || r[1].binding.lexical != nil || r[2].binding.lexical != nil {
		t.Fatal("query alias transferred lexical proof")
	}
	if r[0].binding.object == r[1].binding.object || r[0].binding.object != r[2].binding.object || r[0].binding.version >= r[2].binding.version {
		t.Fatal("object/version identity collapsed")
	}
	if r[0].text != r[1].text || r[1].text != r[2].text {
		t.Fatal("fixture text differs")
	}
}
func TestRowSourceRuntimeTextKeepsOrdinaryStructureProperties(t *testing.T) {
	result := runTextTrace(t, `S=Новый Структура("Text","");S.Text="`+traceSQL+`";Q=Новый Запрос;Q.Text=S.Text;Q.Execute();`, "")
	if result.queryEvidence.reads[0].binding.lexical != nil {
		t.Fatal("structure Text spelling minted Query proof")
	}
}
func TestRowSourceRuntimeTextPreservesEvaluationOrder(t *testing.T) {
	ctx, db, reg, _, src, _ := fixture(t)
	src.Code = `Функция Value(S) S.Вставить("Order",S.Order+"V");Возврат "` + traceSQL + `";КонецФункции
 Функция Receiver(S) S.Вставить("Order",S.Order+"R");Возврат S.Q;КонецФункции
 Функция Build() Экспорт
 S=Новый Структура("Order,Q","",Новый Запрос);
 Receiver(S).Text=Value(S);S.Q.Execute();
 Результат[0].Вставить("Works",S.Order);Возврат Результат;КонецФункции`
	result, err := Run(ctx, db, reg, Query{Text: initialQuery}, src)
	if err != nil {
		t.Fatal(err)
	}
	if result.Rows[0]["Works"] != "VR" || len(result.queryEvidence.reads) != 1 || result.queryEvidence.reads[0].binding.lexical != nil {
		t.Fatal("RHS/receiver order or count changed")
	}
}
func TestRowSourceRuntimeTextCannotCrossExecution(t *testing.T) {
	e := &execution{}
	p := compileEvidence(t, `Q.Text="`+traceSQL+`";`)
	vars := map[string]any{}
	p.bindTextSites(e, vars)
	for _, callback := range vars {
		fn := callback.(interpreter.BuiltinFunc)
		_, err := fn([]any{traceSQL, &opaqueQuery{execution: &execution{}}}, "", 0)
		if err == nil {
			t.Fatal("foreign Query accepted")
		}
	}
}
func TestRowSourceRuntimeTextEvidenceIsPrivateAndRedacted(t *testing.T) {
	result := runTextTrace(t, `Q=Новый Запрос;Q.Text="`+traceSQL+`";Q.Execute();`, "")
	b, err := json.Marshal(result)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(b), "queryEvidence") || strings.Contains(string(b), traceSQL) {
		t.Fatal("private text trace serialized")
	}
	if fmt.Sprintf("%+v", result.queryEvidence) != "ReportQueryEvidence" || fmt.Sprintf("%#v", result.queryEvidence) != "ReportQueryEvidence" {
		t.Fatal("private trace formatted")
	}
}
