package rowsource

import (
	"context"
	"github.com/google/uuid"
	"github.com/ivantit66/onebase/internal/auth"
	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/ivantit66/onebase/internal/query"
	"github.com/ivantit66/onebase/internal/report"
	"github.com/ivantit66/onebase/internal/report/compose"
	"github.com/ivantit66/onebase/internal/report/resultfilter"
	"github.com/ivantit66/onebase/internal/runtime"
	"github.com/ivantit66/onebase/internal/storage"
	"github.com/shopspring/decimal"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

const initialQuery = "ВЫБРАТЬ Р.Ссылка КАК Request, Р.Amount КАК Amount, Р.Count КАК Count, Р.Moment КАК Moment, Р.Note КАК Note ИЗ Документ.RowRequest КАК Р"
const enrichCode = `Функция Build() Экспорт
 Для Каждого Р Из Результат Цикл
  З=Новый Запрос;
  З.Текст="ВЫБРАТЬ Т.WorkText КАК WorkText, Т.Pos КАК Pos, Т.Ссылка КАК Request ИЗ Документ.RowRequest.Works КАК Т ГДЕ Т.Ссылка=&ID УПОРЯДОЧИТЬ ПО Т.Pos";
  З.УстановитьПараметр("ID",Р.Request);
  Текст="";
  Для Каждого Работа Из З.Выполнить() Цикл
   Текст=Текст+Работа.WorkText+"; "+Символ(10);
  КонецЦикла;
  Р.Вставить("Works",Текст+Р.Note);
 КонецЦикла;
 Возврат Результат;
КонецФункции`

func fixture(t *testing.T) (context.Context, *storage.DB, *runtime.Registry, *metadata.Entity, Source, uuid.UUID) {
	t.Helper()
	ctx := context.Background()
	db, err := storage.ConnectSQLite(ctx, filepath.Join(t.TempDir(), "source.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	ent := &metadata.Entity{Name: "RowRequest", Kind: metadata.KindDocument, Presentation: []string{"Label"}, Fields: []metadata.Field{
		{Name: "Label", Type: "string"}, {Name: "Note", Type: "string"}, {Name: "Amount", Type: "number"}, {Name: "Count", Type: "number"}, {Name: "Moment", Type: "date"}, {Name: "Hidden", Type: "string", PII: true}, {Name: "Owner", Type: "string"},
	}, TableParts: []metadata.TablePart{{Name: "Works", Fields: []metadata.Field{{Name: "WorkText", Type: "string"}, {Name: "Pos", Type: "number"}}}}}
	if err = db.Migrate(ctx, []*metadata.Entity{ent}); err != nil {
		t.Fatal(err)
	}
	id := uuid.New()
	if err = db.Upsert(ctx, ent.Name, id, map[string]any{"Label": "safe label", "Note": "Заметка для клиента", "Amount": decimal.RequireFromString("125.50"), "Count": decimal.NewFromInt(1), "Moment": time.Date(2026, 11, 16, 12, 0, 0, 0, time.UTC), "Owner": "reader", "Hidden": "not_for_dsl"}, ent); err != nil {
		t.Fatal(err)
	}
	if err = db.UpsertTablePartRows(ctx, ent.Name, "Works", id, []map[string]any{{"WorkText": "Работа А", "Pos": 1}, {"WorkText": "Работа А", "Pos": 2}}, ent.TableParts[0]); err != nil {
		t.Fatal(err)
	}
	reg := runtime.NewRegistry()
	reg.Load(runtime.LoadOptions{Entities: []*metadata.Entity{ent}, Reports: []*report.Report{{Name: "RowSource", Query: initialQuery}}})
	src := Source{Report: "RowSource", Entry: "Build", Code: enrichCode, Extras: []Extra{{Name: "Works", Type: "string"}}}
	return ctx, db, reg, ent, src, id
}

func TestRowSourceTypedEnrichment(t *testing.T) {
	ctx, db, reg, _, src, id := fixture(t)
	result, err := Run(ctx, db, reg, Query{Text: initialQuery}, src)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Rows) != 1 || len(result.Columns) != 6 {
		t.Fatalf("shape changed: %+v", result)
	}
	row := result.Rows[0]
	if row["request"] != id.String() || row["Works"] != "Работа А; \nРабота А; \nЗаметка для клиента" {
		t.Fatalf("identity/order/duplicates/note changed: %v", row)
	}
	if _, ok := row["moment"].(time.Time); !ok {
		t.Fatal("date type lost")
	}
	comp, err := compose.Compose(result.Rows, report.Composition{Groupings: []string{"note"}, Detail: true, Measures: []report.Measure{{Field: "amount", Agg: "sum"}, {Field: "count", Agg: "sum"}}, Totals: report.Totals{Grand: true, Subtotals: true}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	amount, ok := compose.ExportToDecimal(comp.Grand["amount"])
	if !ok || !amount.Equal(decimal.RequireFromString("125.50")) {
		t.Fatal("amount changed")
	}
	count, ok := compose.ExportToDecimal(comp.Grand["count"])
	if !ok || !count.Equal(decimal.NewFromInt(1)) {
		t.Fatal("count changed")
	}
	if comp.Groups[0].Details[0].Values["Works"] != row["Works"] {
		t.Fatal("ordinary compose lost enrichment")
	}
	origins := result.Columns[5].Origins
	tablePart, literal, pure := false, false, false
	for _, origin := range origins {
		tablePart = tablePart || origin.TablePart == "Works"
		literal = literal || origin.Kind == "literal"
		pure = pure || origin.Kind == "validated-program"
	}
	if len(result.Operations) == 0 {
		t.Fatal("validated operation list missing")
	}
	if !tablePart || !literal || !pure {
		t.Fatalf("missing host lineage: %+v", origins)
	}
}

func TestRowSourceRejectsChangedOriginals(t *testing.T) {
	for _, body := range []string{
		`Результат[0].Amount=0;`,
		`Результат[0].Request="00000000-0000-0000-0000-000000000001";`,
		`Результат[0].Moment="2026-11-16";`,
		`Результат[0].Вставить("Unknown","x");`,
		`Результат.Добавить(Результат[0]);`,
	} {
		t.Run(body, func(t *testing.T) {
			ctx, db, reg, _, src, _ := fixture(t)
			src.Code = strings.Replace(src.Code, "Возврат Результат;", body+" Возврат Результат;", 1)
			result, err := Run(ctx, db, reg, Query{Text: initialQuery}, src)
			if err == nil || result != nil {
				t.Fatalf("changed input accepted: %+v %v", result, err)
			}
			if !strings.Contains(err.Error(), "измен") && !strings.Contains(err.Error(), "identity") && !strings.Contains(err.Error(), "неизвестное выходное") {
				t.Fatalf("wrong rejection stage: %v", err)
			}
		})
	}
}

func TestRowSourceRejectsCapabilitiesAndUnknownOperations(t *testing.T) {
	for _, body := range []string{
		`ВыполнитьКоманду("x");`,
		`С=Новый HTTPСоединение("localhost");`,
		`Вычислить("1");`,
		`Результат[0].Request.ПолучитьОбъект();`,
		`Попытка Результат[0].Вставить("Works","x"); Исключение КонецПопытки;`,
		`НачатьТранзакцию();`,
		`Модуль.ВыполнитьКоманду("x");`,
		`Пока Истина Цикл КонецЦикла;`,
	} {
		t.Run(body, func(t *testing.T) {
			ctx, db, reg, _, src, _ := fixture(t)
			src.Code = `Функция Build() Экспорт ` + body + ` Возврат Результат; КонецФункции`
			if result, err := Run(ctx, db, reg, Query{Text: initialQuery}, src); err == nil || result != nil {
				t.Fatal("unproven code accepted")
			}
		})
	}
}

func TestRowSourceProtectedAndUnknownReads(t *testing.T) {
	for _, tc := range []struct{ name, query string }{
		{"pii_alias", "ВЫБРАТЬ Р.Hidden КАК Innocent ИЗ Документ.RowRequest КАК Р"},
		{"protected_label", "ВЫБРАТЬ Р.Label КАК Innocent ИЗ Документ.RowRequest КАК Р"},
		{"expression", "ВЫБРАТЬ Р.Amount+1 КАК Amount ИЗ Документ.RowRequest КАК Р"},
		{"wildcard", "ВЫБРАТЬ Р.* ИЗ Документ.RowRequest КАК Р"},
		{"unknown_source", "ВЫБРАТЬ Р.Amount ИЗ Справочник.RowRequest КАК Р"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx, db, reg, _, src, _ := fixture(t)
			// Admin/full does not turn protected inputs into module data.
			ctx = auth.ContextWithUser(ctx, &auth.User{Login: "admin", IsAdmin: true})
			if result, err := Run(ctx, db, reg, Query{Text: tc.query}, src); err == nil || result != nil {
				t.Fatal("unproven/protected root read accepted")
			}
		})
	}
}

func TestRowSourceAdditionalQueryCannotReadPII(t *testing.T) {
	ctx, db, reg, _, src, _ := fixture(t)
	src.Code = `Функция Build() Экспорт
      З=Новый Запрос;З.Текст="ВЫБРАТЬ Hidden КАК Innocent ИЗ Документ.RowRequest";З.Выполнить();
      Для Каждого Р Из Результат Цикл Р.Вставить("Works","fixed");КонецЦикла;
      Возврат Результат;
    КонецФункции`
	if result, err := Run(auth.ContextWithUser(ctx, &auth.User{Login: "admin", IsAdmin: true}), db, reg, Query{Text: initialQuery}, src); err == nil || result != nil {
		t.Fatal("protected additional read ignored because output was constant")
	}
}

func TestRowSourceOrdinaryObjectAndFieldRights(t *testing.T) {
	for _, mode := range []string{"object", "field", "report"} {
		t.Run(mode, func(t *testing.T) {
			ctx, db, reg, _, src, _ := fixture(t)
			perm := auth.Permission{Reports: map[string][]string{"RowSource": {"run"}}, Documents: map[string][]string{"RowRequest": {"read"}}}
			switch mode {
			case "object":
				perm.Documents = map[string][]string{}
			case "report":
				perm.Reports = map[string][]string{}
			case "field":
				perm.FieldAccess.Documents = map[string]auth.FieldPolicies{"RowRequest": {"Note": {Read: "hide"}}}
			}
			user := &auth.User{Login: "reader", Roles: []*auth.Role{{Name: "R", Permissions: perm}}}
			if result, err := Run(auth.ContextWithUser(ctx, user), db, reg, Query{Text: initialQuery}, src); err == nil || result != nil {
				t.Fatal("ordinary rights bypassed")
			}
		})
	}
}

func TestRowSourceSchemaAndCancellation(t *testing.T) {
	for _, mode := range []string{"collision", "wrong_type", "unknown_type", "missing", "cancel"} {
		t.Run(mode, func(t *testing.T) {
			ctx, db, reg, _, src, _ := fixture(t)
			switch mode {
			case "collision":
				src.Extras[0].Name = "amount"
			case "wrong_type":
				src.Extras[0].Type = "number"
			case "unknown_type":
				src.Extras[0].Type = "reference:RowRequest"
			case "missing":
				src.Extras[0].Name = "Absent"
			case "cancel":
				var cancel context.CancelFunc
				ctx, cancel = context.WithCancel(ctx)
				cancel()
			}
			if result, err := Run(ctx, db, reg, Query{Text: initialQuery}, src); err == nil || result != nil {
				t.Fatal("invalid schema/cancellation ignored")
			}
		})
	}
}

func TestRowSourceTablePartProof(t *testing.T) {
	_, db, reg, _, _, id := fixture(t)
	text := "ВЫБРАТЬ Т.WorkText КАК WorkText, Т.Pos КАК Pos, Т.Ссылка КАК Request ИЗ Документ.RowRequest.Works КАК Т ГДЕ Т.Ссылка=&ID УПОРЯДОЧИТЬ ПО Т.Pos"
	res, err := query.Compile(text, query.CompileOpts{Entities: reg.Entities(), Dialect: db.Dialect(), Params: map[string]any{"ID": id.String()}})
	if err != nil {
		t.Fatal(err)
	}
	fields := resultfilter.Schema(res, resultfilter.RegistryLookup(reg), resultfilter.ReportAllowed(nil))
	if len(fields) != 3 || fields[2].RefEntity != "RowRequest" || fields[0].SourceTablePart != "Works" {
		t.Fatal("tablepart lineage/root not proven")
	}
}

func TestRowSourceRLSIncludesTableParts(t *testing.T) {
	ctx, db, reg, ent, src, _ := fixture(t)
	other := uuid.New()
	if err := db.Upsert(ctx, ent.Name, other, map[string]any{"Label": "other", "Note": "other", "Owner": "other", "Amount": decimal.NewFromInt(2), "Count": decimal.NewFromInt(1)}, ent); err != nil {
		t.Fatal(err)
	}
	if err := db.UpsertTablePartRows(ctx, ent.Name, "Works", other, []map[string]any{{"WorkText": "FORBIDDEN_OTHER_OWNER", "Pos": 1}}, ent.TableParts[0]); err != nil {
		t.Fatal(err)
	}
	perm := auth.Permission{Reports: map[string][]string{"RowSource": {"run"}}, Documents: map[string][]string{"RowRequest": {"read"}}, RowAccess: auth.RowAccess{Documents: map[string]auth.RowPolicies{"RowRequest": {"read": {Field: "Owner", Op: "eq", Value: auth.RowValue{User: "login"}}}}}}
	reader := &auth.User{Login: "reader", Roles: []*auth.Role{{Name: "R", Permissions: perm}}}
	// Read the whole child source: filtering just the initial parent query is insufficient.
	src.Code = strings.Replace(src.Code, "ГДЕ Т.Ссылка=&ID ", "", 1)
	result, err := Run(auth.ContextWithUser(ctx, reader), db, reg, Query{Text: initialQuery}, src)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Rows) != 1 || strings.Contains(result.Rows[0]["Works"].(string), "FORBIDDEN_OTHER_OWNER") {
		t.Fatalf("child RLS escaped: %v", result.Rows)
	}
}

func TestRowSourcePreservesNullAndTypedEmpty(t *testing.T) {
	ctx, db, reg, ent, src, id := fixture(t)
	// Date NULL remains NULL; the hook must not invent a canonical date/string.
	if err := db.Upsert(ctx, ent.Name, id, map[string]any{"Label": "safe", "Note": "", "Amount": decimal.Zero, "Count": decimal.Zero, "Moment": nil, "Owner": "reader"}, ent); err != nil {
		t.Fatal(err)
	}
	src.Code = `Функция Build() Экспорт Для Каждого Р Из Результат Цикл Р.Вставить("Works",Р.Note);КонецЦикла; Возврат Результат;КонецФункции`
	result, err := Run(ctx, db, reg, Query{Text: initialQuery}, src)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Rows) != 1 || result.Rows[0]["moment"] != nil || result.Rows[0]["Works"] != "" || result.Rows[0]["request"] != id.String() {
		t.Fatalf("typed empties changed: %v", result.Rows)
	}
}

func TestRowSourceAtomicBudgetRefusals(t *testing.T) {
	for _, body := range []string{
		`Для Н=1 По 257 Цикл З=Новый Запрос;З.Текст="ВЫБРАТЬ Amount ИЗ Документ.RowRequest";З.Выполнить();КонецЦикла;`,
		`Для Н=1 По 100001 Цикл А=1;КонецЦикла;`,
	} {
		t.Run(body, func(t *testing.T) {
			ctx, db, reg, _, src, _ := fixture(t)
			src.Code = `Функция Build() Экспорт ` + body + ` Для Каждого Р Из Результат Цикл Р.Вставить("Works","x");КонецЦикла;Возврат Результат;КонецФункции`
			if result, err := Run(ctx, db, reg, Query{Text: initialQuery}, src); err == nil || result != nil {
				t.Fatal("budget produced a partial result")
			}
		})
	}
}

func TestRowSourceQueryObjectIsOpaque(t *testing.T) {
	ctx, db, reg, _, src, _ := fixture(t)
	src.Code = `Функция Build() Экспорт
   З=Новый Запрос;
   Для Каждого Р Из Результат Цикл Р.Вставить("Works",Строка(З));КонецЦикла;
   Возврат Результат;
 КонецФункции`
	result, err := Run(ctx, db, reg, Query{Text: initialQuery}, src)
	if err != nil {
		t.Fatal(err)
	}
	if result.Rows[0]["Works"] != "Запрос" {
		t.Fatal("host object was structurally formatted")
	}
	src.Code = strings.Replace(src.Code, "Строка(З)", "Строка(__factory_Query)", 1)
	if result, err = Run(ctx, db, reg, Query{Text: initialQuery}, src); err == nil || result != nil {
		t.Fatal("ambient factory reference escaped")
	}
}
