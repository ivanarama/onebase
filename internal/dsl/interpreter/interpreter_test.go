package interpreter_test

import (
	"testing"

	"github.com/ivantit66/onebase/internal/dsl/interpreter"
	"github.com/ivantit66/onebase/internal/dsl/lexer"
	"github.com/ivantit66/onebase/internal/dsl/parser"
	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/ivantit66/onebase/internal/runtime"
)

func runProc(t *testing.T, src string, obj *runtime.Object) error {
	t.Helper()
	l := lexer.New(src, "test.os")
	p := parser.New(l)
	prog, err := p.ParseProgram()
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(prog.Procedures) == 0 {
		t.Fatal("no procedures")
	}
	interp := interpreter.New()
	return interp.Run(prog.Procedures[0], obj)
}

func TestInterpreter_ErrorOnEmptyNumber(t *testing.T) {
	src := `Procedure OnWrite()
  If this.Number = "" Then
    Error("Number is required");
  EndIf;
EndProcedure`

	obj := runtime.NewObject("Invoice", metadata.KindDocument)
	obj.Set("Number", "")

	err := runProc(t, src, obj)
	if err == nil {
		t.Fatal("expected error for empty Number")
	}
	dslErr, ok := err.(*interpreter.DSLError)
	if !ok {
		t.Fatalf("want DSLError, got %T: %v", err, err)
	}
	if dslErr.Msg != "Number is required" {
		t.Fatalf("wrong message: %q", dslErr.Msg)
	}
}

func TestInterpreter_NoErrorWithNumber(t *testing.T) {
	src := `Procedure OnWrite()
  If this.Number = "" Then
    Error("Number is required");
  EndIf;
EndProcedure`

	obj := runtime.NewObject("Invoice", metadata.KindDocument)
	obj.Set("Number", "INV-001")

	if err := runProc(t, src, obj); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestInterpreter_Assign(t *testing.T) {
	src := `Procedure SetField()
  this.Status = "active";
EndProcedure`

	obj := runtime.NewObject("Invoice", metadata.KindDocument)
	if err := runProc(t, src, obj); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if obj.Get("Status") != "active" {
		t.Fatalf("expected Status=active, got %v", obj.Get("Status"))
	}
}

func TestInterpreter_UnknownFunction(t *testing.T) {
	src := `Procedure Bad()
  DoSomething();
EndProcedure`
	obj := runtime.NewObject("X", metadata.KindDocument)
	err := runProc(t, src, obj)
	if err == nil {
		t.Fatal("expected error for unknown function")
	}
}
