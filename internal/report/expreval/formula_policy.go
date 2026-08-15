package expreval

import (
	"fmt"
	"math"
	"reflect"
	"strings"
	"time"

	"github.com/shopspring/decimal"

	"github.com/ivantit66/onebase/internal/dsl/ast"
	"github.com/ivantit66/onebase/internal/dsl/interpreter"
	"github.com/ivantit66/onebase/internal/report/compose"
)

const (
	maxFormulaASTNodes      = 256
	maxFormulaStringLiteral = 64 << 10
	maxFormulaStringValue   = 1 << 20
	maxFormulaSourceBytes   = interpreter.MaxUntrustedExpressionBytes
	maxFormulaSourceTokens  = interpreter.MaxUntrustedExpressionTokens
	maxFormulaSyntaxDepth   = interpreter.MaxUntrustedExpressionSyntaxDepth
)

// validateFormulaSource applies iterative limits before ParseProgram enters
// recursive expression parsing. The AST budget below is necessarily too late
// for an expression made from thousands of parentheses or unary operators.
func validateFormulaSource(expr string) error {
	if err := interpreter.ValidateUntrustedExpressionSource(expr); err != nil {
		return fmt.Errorf("formula %w", err)
	}
	return nil
}

// pureFormulaFunctions is deliberately explicit. Report formulas arrive with
// external report definitions and execute for every row, so a newly added DSL
// builtin must not silently become available here. Additions require a review
// that the function is side-effect-free and bounded by the evaluator's sandbox
// profile. Date/time functions are intentionally allowed despite depending on
// the clock: read-only, not mathematical determinism, is the security boundary.
var pureFormulaFunctions = nameSet(
	// Conversions and arithmetic.
	"str строка number число round окр abs абс int цел max макс min мин "+
		"pow sqrt exp log log10 sin cos tan asin acos atan amountinwords числопрописью distribute распределить",
	// Strings.
	"upper врег lower нрег trimall сокрлп left лев right прав mid сред strlen стрдлина strfind стрнайти "+
		"strreplace стрзаменить strstartswith стрначинаетсяс strendswith стрзаканчиваетсяна "+
		"strcontains стрсодержит strsplit стрразделить strjoin стрсоединить strtemplate стршаблон "+
		"trimleft сокрл trimright сокрп stroccurrencecount стрчисловхождений strlinecount стрчислострок "+
		"strgetline стрполучитьстроку charcode кодсимвола strcompare стрсравнить isblankstring пустаястрока "+
		"titlecase трег nstr нстр chr char символ",
	// Dates and date arithmetic.
	"today текущаядата now текущаядатавремя year год month месяц day день hour час minute минута second секунда "+
		"date дата begmonth началомесяца endmonth конецмесяца begyear началогода endyear конецгода "+
		"begweek началонедели endweek конецнедели begday началодня endday конецдня "+
		"begquarter началоквартала endquarter конецквартала beghour началочаса endhour конецчаса "+
		"begminute началоминуты endminute конецминуты dayofweek деньнедели dayofyear деньгода weekofyear неделягода "+
		"addmonth добавитьмесяц addday добавитьдень addyear добавитьгод addseconds добавитьсекунд "+
		"addsecond добавитьсекунду addminutes добавитьминут addminute добавитьминуту "+
		"addhours добавитьчас addhour добавитьчасов datediff разностьдат",
	// Formatting and read-only predicates.
	"format формат isblank пустая isfilled значениезаполнено isemptyref пустаяссылка typeof типзнч type тип",
)

func nameSet(groups ...string) map[string]struct{} {
	result := make(map[string]struct{})
	for _, group := range groups {
		for _, name := range strings.Fields(group) {
			result[strings.ToLower(name)] = struct{}{}
		}
	}
	return result
}

// validateFormulaExpr accepts values and operations only. Statements are
// already excluded by ParseProgram's Return wrapper, but this whitelist also
// fails closed when the expression AST gains a new node kind later.
func validateFormulaExpr(expr ast.Expr) error {
	nodes := 0
	return validateFormulaExprN(expr, &nodes)
}

func validateFormulaExprN(expr ast.Expr, nodes *int) error {
	(*nodes)++
	if *nodes > maxFormulaASTNodes {
		return fmt.Errorf("формула отчёта превышает предел сложности %d узлов", maxFormulaASTNodes)
	}
	switch v := expr.(type) {
	case *ast.Ident, *ast.BoolLit:
		return nil
	case *ast.StringLit:
		if len(v.Value) > maxFormulaStringLiteral {
			return fmt.Errorf("строковый литерал формулы отчёта превышает предел %d байт", maxFormulaStringLiteral)
		}
		return nil
	case *ast.NumberLit:
		d, err := decimal.NewFromString(v.Value)
		if err != nil {
			return fmt.Errorf("некорректное число %q в формуле отчёта: %w", v.Value, err)
		}
		if !interpreter.DecimalWithinSafeBounds(d) {
			return fmt.Errorf("число %q в формуле отчёта вне безопасного диапазона", v.Value)
		}
		return nil
	case *ast.BinaryExpr:
		if err := validateFormulaExprN(v.Left, nodes); err != nil {
			return err
		}
		return validateFormulaExprN(v.Right, nodes)
	case *ast.UnaryExpr:
		return validateFormulaExprN(v.Operand, nodes)
	case *ast.TernaryExpr:
		if err := validateFormulaExprN(v.Cond, nodes); err != nil {
			return err
		}
		if err := validateFormulaExprN(v.True, nodes); err != nil {
			return err
		}
		return validateFormulaExprN(v.False, nodes)
	case *ast.MemberExpr:
		// A field read is part of the formula contract; a method call has a
		// CallExpr above it and is rejected in that branch.
		return validateFormulaExprN(v.Object, nodes)
	case *ast.IndexExpr:
		if err := validateFormulaExprN(v.Object, nodes); err != nil {
			return err
		}
		return validateFormulaExprN(v.Index, nodes)
	case *ast.ArrayLit:
		for _, element := range v.Elements {
			if err := validateFormulaExprN(element, nodes); err != nil {
				return err
			}
		}
		return nil
	case *ast.CallExpr:
		callee, ok := v.Callee.(*ast.Ident)
		if !ok {
			if member, isMethod := v.Callee.(*ast.MemberExpr); isMethod {
				return fmt.Errorf("метод %q запрещён в формуле отчёта: разрешены только чтение полей и чистые функции", member.Field.Literal)
			}
			return fmt.Errorf("косвенный вызов запрещён в формуле отчёта")
		}
		if _, ok := pureFormulaFunctions[strings.ToLower(callee.Tok.Literal)]; !ok {
			return fmt.Errorf("функция %q не входит в список чистых функций формулы отчёта", callee.Tok.Literal)
		}
		for _, arg := range v.Args {
			if err := validateFormulaExprN(arg, nodes); err != nil {
				return err
			}
		}
		return nil
	case *ast.NewExpr:
		return fmt.Errorf("создание объекта Новый %s запрещено в формуле отчёта", v.TypeName.Literal)
	case nil:
		return fmt.Errorf("пустое выражение формулы отчёта")
	default:
		return fmt.Errorf("конструкция %T не разрешена в формуле отчёта", expr)
	}
}

// validateRowBindings closes the remaining name-resolution escape hatch: DSL
// permits a variable to shadow a builtin with an injected Go callback. Report
// rows are data, never executable capabilities.
func validateRowBindings(row compose.Row) error {
	for name, value := range row {
		// Reject every named and unnamed function type, not only the callable
		// types known today. In particular, ReadOnlyBuiltinFunc is safe only for
		// the debugger's read-only membrane; a report row is data and must never
		// be able to replace an allowed formula builtin with executable code.
		valueType := reflect.TypeOf(value)
		if valueType != nil && valueType.Kind() == reflect.Func {
			return fmt.Errorf("поле строки %q содержит исполняемую функцию; формулы отчёта принимают только данные", name)
		}
		switch v := value.(type) {
		case nil, bool, time.Time,
			int, int8, int16, int32, int64,
			uint, uint8, uint16, uint32, uint64:
			// Passive scalar data produced by report and form paths.
		case string:
			if len(v) > maxFormulaStringValue {
				return fmt.Errorf("string field %q exceeds the %d byte limit", name, maxFormulaStringValue)
			}
		case []byte:
			if len(v) > maxFormulaStringValue {
				return fmt.Errorf("byte field %q exceeds the %d byte limit", name, maxFormulaStringValue)
			}
		case decimal.Decimal:
			if !interpreter.DecimalWithinSafeBounds(v) {
				return fmt.Errorf("числовое поле строки %q вне безопасного диапазона", name)
			}
		case float32:
			if math.IsNaN(float64(v)) || math.IsInf(float64(v), 0) {
				return fmt.Errorf("numeric field %q must be finite", name)
			}
		default:
			return fmt.Errorf("row field %q has unsupported type %T; report formulas accept passive scalar data only", name, value)
		case float64:
			if math.IsNaN(v) || math.IsInf(v, 0) {
				return fmt.Errorf("числовое поле строки %q должно быть конечным", name)
			}
		}
	}
	return nil
}
