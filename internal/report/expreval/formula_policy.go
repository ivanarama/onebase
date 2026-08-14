package expreval

import (
	"fmt"
	"strings"

	"github.com/ivantit66/onebase/internal/dsl/ast"
	"github.com/ivantit66/onebase/internal/dsl/interpreter"
	"github.com/ivantit66/onebase/internal/report/compose"
)

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
	switch v := expr.(type) {
	case *ast.Ident, *ast.StringLit, *ast.NumberLit, *ast.BoolLit:
		return nil
	case *ast.BinaryExpr:
		if err := validateFormulaExpr(v.Left); err != nil {
			return err
		}
		return validateFormulaExpr(v.Right)
	case *ast.UnaryExpr:
		return validateFormulaExpr(v.Operand)
	case *ast.TernaryExpr:
		if err := validateFormulaExpr(v.Cond); err != nil {
			return err
		}
		if err := validateFormulaExpr(v.True); err != nil {
			return err
		}
		return validateFormulaExpr(v.False)
	case *ast.MemberExpr:
		// A field read is part of the formula contract; a method call has a
		// CallExpr above it and is rejected in that branch.
		return validateFormulaExpr(v.Object)
	case *ast.IndexExpr:
		if err := validateFormulaExpr(v.Object); err != nil {
			return err
		}
		return validateFormulaExpr(v.Index)
	case *ast.ArrayLit:
		for _, element := range v.Elements {
			if err := validateFormulaExpr(element); err != nil {
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
			if err := validateFormulaExpr(arg); err != nil {
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
		switch value.(type) {
		case interpreter.BuiltinFunc, interpreter.FallbackBuiltinFunc, func([]any) any:
			return fmt.Errorf("поле строки %q содержит исполняемую функцию; формулы отчёта принимают только данные", name)
		}
	}
	return nil
}
