package interpreter_test

import (
	"strings"
	"testing"

	"github.com/ivantit66/onebase/internal/dsl/interpreter"
	"github.com/ivantit66/onebase/internal/dsl/lexer"
	"github.com/ivantit66/onebase/internal/dsl/parser"
	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/ivantit66/onebase/internal/runtime"
)

// Оператор должен не только парситься, но и реально возбуждать исключение,
// которое ловится Попыткой — иначе получился бы «зелёный» разбор мёртвого кода.
func TestDSL_ВызватьИсключениеБезСкобокРаботает(t *testing.T) {
	src := `Процедура Тест()
		Попытка
			ВызватьИсключение "сломалось";
		Исключение
			Возврат "поймано: " + ОписаниеОшибки();
		КонецПопытки;
		Возврат "не сработало";
	КонецПроцедуры`
	l := lexer.New(src, "test.os")
	prog, err := parser.New(l).ParseProgram()
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	interp := interpreter.New()
	obj := runtime.NewObject("Test", metadata.KindDocument)
	var result any
	if err := interp.RunWithResult(prog.Procedures[0], obj, &result); err != nil {
		t.Fatalf("run: %v", err)
	}
	s, _ := result.(string)
	if !strings.HasPrefix(s, "поймано:") {
		t.Errorf("результат %q — исключение не возбудилось", s)
	}
}
