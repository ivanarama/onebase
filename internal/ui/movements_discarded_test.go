package ui

// Движения вне записи документа (issue #743). Коллектор движений
// подставляется в переменные DSL два десятка раз, а сбрасывают его в базу
// ровно пять путей записи и проведения. В остальных контекстах
// `Движения.X.Добавить()` выглядел рабочим и молча выбрасывал строки — это
// первое, что пробует человек, пришедший из 1С, и расхождение он находит потом
// по остаткам.

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/ivantit66/onebase/internal/dsl/interpreter"
	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/ivantit66/onebase/internal/project"
	"github.com/ivantit66/onebase/internal/runtime"
	"github.com/ivantit66/onebase/internal/storage"
)

// writeMovementsProject — проект с регистром накопления и обработкой, которая
// пытается писать в него движения.
func writeMovementsProject(t *testing.T, procOS string) string {
	t.Helper()
	dir := t.TempDir()
	mk := func(sub string) string {
		p := filepath.Join(dir, sub)
		if err := os.MkdirAll(p, 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", sub, err)
		}
		return p
	}
	regDir := mk("registers")
	procDir := mk("processors")
	srcDir := mk("src")

	reg := "name: ОстаткиТоваров\n" +
		"dimensions:\n  - {name: Номенклатура, type: string}\n" +
		"resources:\n  - {name: Количество, type: number}\n"
	if err := os.WriteFile(filepath.Join(regDir, "остаткитоваров.yaml"), []byte(reg), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(procDir, "перепроведение.yaml"),
		[]byte("name: Перепроведение\ntitle: Перепроведение\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(srcDir, "перепроведение.proc.os"), []byte(procOS), 0o644); err != nil {
		t.Fatal(err)
	}
	return dir
}

func runMovementsProc(t *testing.T, procOS string) (messages []string, runErr error) {
	t.Helper()
	dir := writeMovementsProject(t, procOS)
	ctx := context.Background()
	proj, err := project.Load(dir)
	if err != nil {
		t.Fatalf("project.Load: %v", err)
	}
	defer proj.Close()

	db, err := storage.ConnectSQLite(ctx, filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("ConnectSQLite: %v", err)
	}
	defer db.Close()
	if err := db.MigrateRegisters(ctx, proj.Registers); err != nil {
		t.Fatalf("миграция регистров: %v", err)
	}

	msgs, rErr, err := RunProcessorOffline(ctx, proj, db, "Перепроведение", nil, nil)
	if err != nil {
		t.Fatalf("RunProcessorOffline: %v", err)
	}
	return msgs, rErr
}

// В обработке движения писать некуда — это отказ с внятным текстом, а не тихий
// no-op. Молча выброшенные строки выглядят как отработавшая логика.
func TestMovements_InProcessorAreRejected(t *testing.T) {
	procOS := "Процедура Выполнить()\n" +
		"  Дв = Движения.ОстаткиТоваров.Добавить();\n" +
		"  Дв.Номенклатура = \"Стол\";\n" +
		"  Дв.Количество = 5;\n" +
		"  Сообщить(\"дошли до конца\");\n" +
		"КонецПроцедуры\n"

	msgs, runErr := runMovementsProc(t, procOS)
	if runErr == nil {
		t.Fatalf("движения в обработке молча проглочены, сообщения=%v", msgs)
	}
	text := runErr.Error()
	for _, want := range []string{"Движения.ОстаткиТоваров.Добавить()", "сохранять некуда", "ОбработкаПроведения"} {
		if !strings.Contains(text, want) {
			t.Errorf("в тексте отказа нет %q: %s", want, text)
		}
	}
	if !strings.Contains(text, "обработка") {
		t.Errorf("отказ не называет контекст: %s", text)
	}
	for _, m := range msgs {
		if strings.Contains(m, "дошли до конца") {
			t.Error("выполнение продолжилось после отказа")
		}
	}
}

// Отказ ловится Попыткой, как любая прикладная ошибка: код, который сознательно
// пробует записать движения и имеет запасной путь, не обязан падать.
func TestMovements_RejectionIsCatchable(t *testing.T) {
	procOS := "Процедура Выполнить()\n" +
		"  Попытка\n" +
		"    Движения.ОстаткиТоваров.Добавить();\n" +
		"    Сообщить(\"без ошибки\");\n" +
		"  Исключение\n" +
		"    Сообщить(\"поймано\");\n" +
		"  КонецПопытки;\n" +
		"КонецПроцедуры\n"

	msgs, runErr := runMovementsProc(t, procOS)
	if runErr != nil {
		t.Fatalf("Попытка не перехватила отказ: %v", runErr)
	}
	if len(msgs) != 1 || msgs[0] != "поймано" {
		t.Fatalf("сообщения=%v, ожидалось [поймано]", msgs)
	}
}

// Очистить остаётся безобидным: отказывать не за что, и код, который на всякий
// случай чистит набор перед заполнением, не должен падать раньше времени.
func TestMovements_ClearIsAllowedOutsidePosting(t *testing.T) {
	procOS := "Процедура Выполнить()\n" +
		"  Движения.ОстаткиТоваров.Очистить();\n" +
		"  Сообщить(\"ok\");\n" +
		"КонецПроцедуры\n"

	msgs, runErr := runMovementsProc(t, procOS)
	if runErr != nil {
		t.Fatalf("Очистить отклонена: %v", runErr)
	}
	if len(msgs) != 1 || msgs[0] != "ok" {
		t.Fatalf("сообщения=%v, ожидалось [ok]", msgs)
	}
}

// Тот же отказ — во всех контекстах, где коллектор не сбрасывается (#898).
//
// Тест на обработке закрывал один путь из четырёх, названных в заявке
// (регламентное задание, обработка, отчёт, печатная форма) — а именно первый из
// них и есть тот, где человек, пришедший из 1С, пробует писать движения раньше
// всего. Контексты различаются только меткой DocType коллектора, поэтому
// проверяются здесь напрямую: поднимать ради этого планировщик и рендер отчёта
// значило бы проверять их, а не границу движений.
func TestMovements_RejectedInEveryNonPersistingContext(t *testing.T) {
	reg := &metadata.Register{
		Name:       "ОстаткиТоваров",
		Dimensions: []metadata.Field{{Name: "Номенклатура", Type: metadata.FieldTypeString}},
		Resources:  []metadata.Field{{Name: "Количество", Type: metadata.FieldTypeNumber}},
	}
	s, ctx := newSubmitTestServer(t, nil)
	s.reg.Load(runtime.LoadOptions{Registers: []*metadata.Register{reg}})

	for _, c := range []struct{ docType, label string }{
		{"scheduler", "регламентное задание"},
		{"processor", "обработка"},
		{"report", "отчёт"},
		{"page", "страница"},
		{"console", "консоль кода"},
		{"intake", "приём сообщений"},
		{"service", "HTTP-сервис"},
	} {
		t.Run(c.docType, func(t *testing.T) {
			mc := runtime.NewMovementsCollector(c.docType, uuid.Nil)
			var msgs []string
			vars, txState := s.buildDSLVarsWithMessagesTx(ctx, mc, &msgs)
			defer interpreter.RollbackTxExecution(txState)

			prog := mustParse(t, `Процедура Тест()
  Движения.ОстаткиТоваров.Добавить();
  Сообщить("дошли до конца");
КонецПроцедуры`)
			err := s.interp.Run(prog.Procedures[0], nil, vars)
			if err == nil {
				t.Fatalf("движения в контексте %q приняты молча, сообщения=%v", c.docType, msgs)
			}
			text := err.Error()
			if !strings.Contains(text, "сохранять некуда") {
				t.Errorf("не тот отказ: %s", text)
			}
			// Контекст назван человеческим именем: «регламентное задание», а не
			// «scheduler» — иначе подсказка не помогает тому, кто её читает.
			if !strings.Contains(text, c.label) {
				t.Errorf("отказ не называет контекст %q: %s", c.label, text)
			}
			// Имя регистра — как в конфигурации, а не как пришло из DSL.
			if !strings.Contains(text, "Движения.ОстаткиТоваров.Добавить()") {
				t.Errorf("имя регистра искажено: %s", text)
			}
			for _, m := range msgs {
				if strings.Contains(m, "дошли до конца") {
					t.Error("выполнение продолжилось после отказа")
				}
			}
		})
	}
}
