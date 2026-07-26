package project

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ivantit66/onebase/internal/dsl/ast"
)

// issue #171: у обработки исполняемый раздел модуля (операторы вне процедур)
// исполняется как точка входа Выполнить. Загрузчик синтезирует процедуру
// Выполнить из тела модуля, если явной Выполнить нет.
func TestModuleBody_ProcessorSynthesizesExecute(t *testing.T) {
	dir := t.TempDir()
	srcDir := filepath.Join(dir, "src")
	if err := os.MkdirAll(srcDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	src := "Перем Итог;\nИтог = 2 + 3;\nСообщить(Итог);\n"
	if err := os.WriteFile(filepath.Join(srcDir, "привет.proc.os"), []byte(src), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}

	proj, err := Load(dir)
	if err != nil {
		t.Fatalf("project.Load: %v", err)
	}
	defer proj.Close()

	prog, ok := proj.Programs["Привет"]
	if !ok {
		t.Fatalf("программа обработки не загружена; Programs=%v", keys(proj.Programs))
	}
	var exec *ast.ProcedureDecl
	for _, p := range prog.Procedures {
		if strings.EqualFold(p.Name.Literal, "Выполнить") {
			exec = p
			break
		}
	}
	if exec == nil {
		t.Fatalf("тело модуля обработки не превратилось в процедуру Выполнить; процедур=%d", len(prog.Procedures))
		return
	}
	// Тело Выполнить должно содержать операторы из тела модуля (присваивание + вызов),
	// с объявлением переменной модуля впереди.
	if len(exec.Body) < 2 {
		t.Fatalf("Выполнить пустая или короче тела модуля: %d операторов", len(exec.Body))
	}
	foundAssign := false
	for _, st := range exec.Body {
		if _, ok := st.(*ast.AssignStmt); ok {
			foundAssign = true
		}
	}
	if !foundAssign {
		t.Fatalf("в синтезированной Выполнить нет присваивания из тела модуля")
	}
}

// issue #171: тело модуля допустимо ТОЛЬКО у обработок. У общего модуля
// (.module.os) операторы вне процедур — ошибка загрузки (как и у модуля
// объекта/менеджера в 1С, где исполняемого раздела нет).
func TestModuleBody_CommonModuleRejected(t *testing.T) {
	dir := t.TempDir()
	srcDir := filepath.Join(dir, "src")
	if err := os.MkdirAll(srcDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	src := "Функция Удвоить(Х) Экспорт\n  Возврат Х * 2;\nКонецФункции\nЗапустить();\n"
	if err := os.WriteFile(filepath.Join(srcDir, "утилиты.module.os"), []byte(src), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}

	_, err := Load(dir)
	if err == nil {
		t.Fatal("ожидали ошибку загрузки: тело модуля в общем модуле недопустимо")
	}
	if !strings.Contains(err.Error(), "тело модуля") {
		t.Fatalf("ожидали понятное сообщение про тело модуля, получили: %v", err)
	}
}
