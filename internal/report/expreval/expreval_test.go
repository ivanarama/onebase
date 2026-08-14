package expreval

import (
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ivantit66/onebase/internal/dsl/ast"
	"github.com/ivantit66/onebase/internal/dsl/interpreter"
	"github.com/ivantit66/onebase/internal/dsl/lexer"
	"github.com/ivantit66/onebase/internal/dsl/parser"
	"github.com/ivantit66/onebase/internal/report"
	"github.com/ivantit66/onebase/internal/report/compose"
)

// Конфигурационная функция не является формулой, даже если её тело когда-либо
// уложилось бы в runtime-лимиты. Отказ происходит по AST до входа в функцию.
func TestEvaluator_НеВызываетЗацикленнуюФункциюКонфигурации(t *testing.T) {
	src := "Функция Вечность()\n\tПока Истина Цикл\n\tКонецЦикла;\n\tВозврат Истина;\nКонецФункции\n"
	prog, err := parser.New(lexer.New(src, "t.os")).ParseProgram()
	if err != nil {
		t.Fatal(err)
	}
	procs := map[string]*ast.ProcedureDecl{}
	for _, d := range prog.Procedures {
		procs[d.Name.Literal] = d
		procs[strings.ToLower(d.Name.Literal)] = d
	}
	interp := interpreter.New()
	interp.LookupProc = func(name string) *ast.ProcedureDecl {
		if p, ok := procs[name]; ok {
			return p
		}
		return procs[strings.ToLower(name)]
	}

	ev := New(interp, interpreter.SandboxProfile{MaxLoopIters: 10_000})
	start := time.Now()
	_, evalErr := ev.EvalBool("Вечность()", compose.Row{})
	if evalErr == nil {
		t.Fatal("вызов функции конфигурации принят как чистая формула")
	}
	if !strings.Contains(evalErr.Error(), "Вечность") {
		t.Fatalf("ошибка не называет запрещённую функцию: %v", evalErr)
	}
	if elapsed := time.Since(start); elapsed > time.Second {
		t.Fatalf("отказ произошёл не до выполнения функции: %v", elapsed)
	}
}

func TestEvaluator_КонфигурацияНеМожетПодменитьРазрешённыйBuiltin(t *testing.T) {
	shadow := parseFormulaTestProc(t, `Функция Формат(Значение, Настройка)
  ВызватьИсключение("подмена вызвана");
КонецФункции`)
	interp := interpreter.New()
	interp.LookupProc = func(name string) *ast.ProcedureDecl {
		if strings.EqualFold(name, "Формат") {
			return shadow
		}
		return nil
	}

	ev := New(interp, DefaultProfile())
	got, err := ev.EvalBool(`Формат(10, "ЧЦ=2") <> ""`, compose.Row{})
	if err != nil || !got {
		t.Fatalf("чистый builtin был подменён функцией конфигурации: got=%v err=%v", got, err)
	}
}

func parseFormulaTestProc(t *testing.T, src string) *ast.ProcedureDecl {
	t.Helper()
	prog, err := parser.New(lexer.New(src, "formula-test.os")).ParseProgram()
	if err != nil {
		t.Fatal(err)
	}
	if len(prog.Procedures) != 1 {
		t.Fatalf("процедур в fixture: %d", len(prog.Procedures))
	}
	return prog.Procedures[0]
}

// Контроль: нормальная формула считается штатно — та же реализация, что и до
// объединения путей.
func TestEvaluator_NormalExpression(t *testing.T) {
	ev := New(interpreter.New(), DefaultProfile())
	got, err := ev.EvalBool("1 = 1", compose.Row{})
	if err != nil {
		t.Fatalf("EvalBool: %v", err)
	}
	if !got {
		t.Fatal("ожидалось true для «1 = 1»")
	}
}

func TestPureFormulaWhitelist_ВсеИменаЗарегистрированы(t *testing.T) {
	known := interpreter.KnownBuiltinNames()
	for name := range pureFormulaFunctions {
		if _, ok := known[name]; !ok {
			t.Errorf("whitelist содержит незарегистрированную функцию %q", name)
		}
	}
}

// Профиль формул закрывает возможности, а не только время (#884).
//
// Лимиты времени и итераций (#788) закрыли DoS, но файлы, сеть и запуск
// процессов формуле оставались доступны. Формула отчёта не должна уметь того,
// что умеет полноценный модуль: внешние отчёты загружаются через админку и
// живут в БД, то есть формула может приехать файлом со стороны — как внешняя
// обработка.
func TestEvaluator_ПрофильЗапрещаетВозможности(t *testing.T) {
	ev := New(interpreter.New(), DefaultProfile())

	cases := []struct {
		name, expr, want string
	}{
		{"временный файл", `ПолучитьИмяВременногоФайла() <> ""`, "файл"},
		{"каталог временных файлов", `КаталогВременныхФайлов() <> ""`, "файл"},
		{"поиск файлов", `НайтиФайлы("/", "*") <> Неопределено`, "файл"},
		{"HTTP-запрос", `HTTPПолучить("http://example.com") <> Неопределено`, "http"},
		{"команда ОС", `ВыполнитьКоманду("id") <> Неопределено`, "команд"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, err := ev.EvalBool(c.expr, compose.Row{})
			if err == nil {
				t.Fatalf("формула «%s» выполнена без ошибки — возможность не запрещена", c.expr)
			}
			if !strings.Contains(strings.ToLower(err.Error()), c.want) {
				t.Errorf("ошибка не объясняет запрет (%q): %v", c.want, err)
			}
		})
	}
}

// Обратная сторона whitelist: обе языковые формы, смешанный регистр и нужные
// арифметика/строки/даты/формат должны остаться совместимыми.
func TestEvaluator_АрифметикаСтрокиДатыРаботают(t *testing.T) {
	ev := New(interpreter.New(), DefaultProfile())
	row := compose.Row{"Сумма": float64(150), "Клиент": "ООО Ромашка"}

	for _, c := range []struct {
		name, expr string
	}{
		{"арифметика", "Сумма * 2 > 200"},
		{"строки", `СтрДлина(Клиент) > 3`},
		{"английский алиас строки", `STRLEN(Клиент) > 3`},
		{"верхний регистр", `ВРег(Клиент) <> ""`},
		{"смешанный регистр", `сТрСоДеРжИт(Клиент, "Ромашка")`},
		{"математика", `Sqrt(Abs(-9)) = 3`},
		{"даты", `Год(ТекущаяДата()) > 2000`},
		{"английский алиас даты", `Year(Today()) > 2000`},
		{"формат", `Формат(Сумма, "ЧЦ=10") <> ""`},
		{"чтение поля ЭтотОбъект", `ЭтотОбъект.Сумма = 150`},
	} {
		t.Run(c.name, func(t *testing.T) {
			got, err := ev.EvalBool(c.expr, row)
			if err != nil {
				t.Fatalf("формула «%s» отвергнута: %v", c.expr, err)
			}
			if !got {
				t.Errorf("формула «%s» дала Ложь", c.expr)
			}
		})
	}
}

func TestEvaluator_ФормулаНеМожетИзменитьСтрокуОтчёта(t *testing.T) {
	ev := New(interpreter.New(), DefaultProfile())
	row := compose.Row{"Сумма": float64(150)}
	_, err := ev.EvalBool(`ЗаполнитьЗначенияСвойств(ЭтотОбъект, Новый Структура("Сумма", 0)) = Неопределено`, row)
	if got := row["Сумма"]; got != float64(150) {
		t.Errorf("формула изменила строку отчёта: Сумма=%v", got)
	}
	if err == nil {
		t.Error("изменяющий builtin выполнен формулой без ошибки")
	}
}

type formulaEffectObject struct{ touched atomic.Bool }

func (o *formulaEffectObject) Get(string) any  { return nil }
func (o *formulaEffectObject) Set(string, any) { o.touched.Store(true) }
func (o *formulaEffectObject) CallMethod(string, []any) any {
	o.touched.Store(true)
	return nil
}

func TestEvaluator_ОпасныеКонструкцииОтклоняютсяДоВыполнения(t *testing.T) {
	ev := New(interpreter.New(), DefaultProfile())
	object := &formulaEffectObject{}

	for _, tc := range []struct {
		name, expr, want string
	}{
		{"метод объекта", `Объект.Записать() = Неопределено`, "Записать"},
		{"создание объекта", `Новый Структура("Ключ", 1) = Неопределено`, "Новый"},
		{"исключение", `ВызватьИсключение("стоп")`, "ВызватьИсключение"},
		{"вычислить", `Вычислить("1 = 1")`, "Вычислить"},
		{"неизвестная функция", `ОпаснаяФункция()`, "ОпаснаяФункция"},
		{"присваивание", `Сумма = 0; Истина`, ""},
		{"цикл", `Пока Истина Цикл КонецЦикла`, ""},
		{"попытка", `Попытка Истина Исключение Ложь КонецПопытки`, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := ev.EvalBool(tc.expr, compose.Row{"Сумма": float64(150), "Объект": object})
			if err == nil {
				t.Fatalf("опасная формула выполнена: %s", tc.expr)
			}
			if tc.want != "" && !strings.Contains(strings.ToLower(err.Error()), strings.ToLower(tc.want)) {
				t.Fatalf("ошибка не называет запрет %q: %v", tc.want, err)
			}
		})
	}
	if object.touched.Load() {
		t.Fatal("метод объекта был вызван до отказа whitelist")
	}
}

func TestEvaluator_ОшибкаПравилаНеМеняетПорядокСледующихПравил(t *testing.T) {
	ev := New(interpreter.New(), DefaultProfile())
	row := compose.Row{"Сумма": float64(150)}

	if _, err := ev.EvalBool(`ЗаполнитьЗначенияСвойств(ЭтотОбъект, Новый Структура("Сумма", 0)) = Неопределено`, row); err == nil {
		t.Fatal("первое изменяющее правило не отвергнуто")
	}
	got, err := ev.EvalBool(`Сумма = 150`, row)
	if err != nil || !got {
		t.Fatalf("следующее правило не увидело исходную строку: got=%v err=%v row=%v", got, err, row)
	}
	if got := row["Сумма"]; got != float64(150) {
		t.Fatalf("порядок правил зависит от побочного эффекта предыдущей формулы: Сумма=%v", got)
	}
}

func TestEvaluator_КомпоновкаПродолжаетСледующимПравиломПослеЗапрета(t *testing.T) {
	row := compose.Row{"Группа": "A", "Сумма": float64(150)}
	spec := report.Composition{
		Groupings: []string{"Группа"},
		Measures:  []report.Measure{{Field: "Сумма", Agg: "sum"}},
		Detail:    true,
		Conditional: []report.CondRule{
			{
				When:  `ЗаполнитьЗначенияСвойств(ЭтотОбъект, Новый Структура("Сумма", 0)) = Неопределено`,
				Field: "",
				Style: report.CellStyle{Background: "#danger"},
			},
			{
				When:  `Сумма = 150`,
				Field: "",
				Style: report.CellStyle{Background: "#safe"},
			},
		},
	}
	res, err := compose.Compose([]compose.Row{row}, spec, New(interpreter.New(), DefaultProfile()))
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Warnings) != 1 || !strings.Contains(res.Warnings[0], "ЗаполнитьЗначенияСвойств") {
		t.Fatalf("запрет первого правила не попал в предупреждения: %v", res.Warnings)
	}
	if len(res.Groups) != 1 || len(res.Groups[0].Details) != 1 {
		t.Fatalf("неожиданная структура результата: %+v", res.Groups)
	}
	if got := res.Groups[0].Details[0].Styles[""].Background; got != "#safe" {
		t.Fatalf("после запрещённого правила не применилось следующее: background=%q", got)
	}
	if got := row["Сумма"]; got != float64(150) {
		t.Fatalf("компоновка изменила исходную строку: Сумма=%v", got)
	}
}

func TestEvaluator_СтрокаНеМожетПодменитьЧистыйBuiltinКолбэком(t *testing.T) {
	var touched atomic.Bool
	row := compose.Row{
		"Формат": interpreter.BuiltinFunc(func([]any, string, int) (any, error) {
			touched.Store(true)
			return "выполнено", nil
		}),
	}
	ev := New(interpreter.New(), DefaultProfile())
	if _, err := ev.EvalBool(`Формат(1, "ЧЦ=1") <> ""`, row); err == nil {
		t.Fatal("исполняемое значение из строки принято формулой")
	}
	if touched.Load() {
		t.Fatal("колбэк из строки был выполнен")
	}
}

func TestEvaluator_СтрокаНеМожетПодменитьЧистыйBuiltinReadOnlyКолбэком(t *testing.T) {
	var touched atomic.Bool
	row := compose.Row{
		"Формат": interpreter.ReadOnlyBuiltinFunc(func([]any, string, int) (any, error) {
			touched.Store(true)
			return "выполнено", nil
		}),
	}
	ev := New(interpreter.New(), DefaultProfile())
	if _, err := ev.EvalBool(`Формат(1, "ЧЦ=1") <> ""`, row); err == nil {
		t.Fatal("read-only callback из строки принят формулой")
	}
	if touched.Load() {
		t.Fatal("read-only callback из строки был выполнен")
	}
}
