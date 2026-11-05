package ui

import (
	"context"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"sync/atomic"
	"testing"

	"github.com/ivantit66/onebase/internal/project"
	"github.com/ivantit66/onebase/internal/storage"
)

// runProfileTests собирает проект из набора тест-обработок (имя → код),
// мигрирует :memory: и прогоняет их. Возвращает результат для проверок.
func runProfileTests(t *testing.T, procs map[string]string, netEnabled ...bool) TestRunResult {
	t.Helper()
	dir := t.TempDir()
	procDir := filepath.Join(dir, "processors")
	srcDir := filepath.Join(dir, "src")
	for _, d := range []string{procDir, srcDir} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatalf("mkdir: %v", err)
		}
	}
	for name, code := range procs {
		if err := os.WriteFile(filepath.Join(procDir, name+".yaml"),
			[]byte("name: "+name+"\nkind: test\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(srcDir, name+".proc.os"), []byte(code), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	ctx := context.Background()
	proj, err := project.Load(dir)
	if err != nil {
		t.Fatalf("project.Load: %v", err)
	}
	t.Cleanup(func() { proj.Close() })
	db, err := storage.ConnectSQLite(ctx, ":memory:")
	if err != nil {
		t.Fatalf("ConnectSQLite: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	if err := db.Migrate(ctx, proj.Entities); err != nil {
		t.Fatalf("Migrate: %v", err)
	}

	if len(netEnabled) > 0 {
		value := "0"
		if netEnabled[0] {
			value = "1"
		}
		if err := db.SaveSetting(ctx, "net.enabled", value); err != nil {
			t.Fatal(err)
		}
	}
	res, err := RunTests(ctx, proj, db, TestRunOptions{})
	if err != nil {
		t.Fatalf("RunTests: %v", err)
	}
	return res
}

// requireCaseOK проваливает Go-тест, если DSL-тест не прошёл, печатая его
// проваленные проверки/ошибку (внутренние Утверждать несут детали).
func requireCaseOK(t *testing.T, res TestRunResult, name string) {
	t.Helper()
	c := caseByName(res, name)
	if c == nil {
		t.Fatalf("тест %s не найден в результатах", name)
	}
	if c.OK() {
		return
	}
	for _, o := range c.Asserts {
		if !o.Passed {
			t.Errorf("%s: провал проверки %q (%s)", name, o.Desc, o.Detail)
		}
	}
	if c.Err != nil {
		t.Errorf("%s: ошибка выполнения: %v", name, c.Err)
	}
	t.FailNow()
}

func TestProfile_ClockFreeze(t *testing.T) {
	res := runProfileTests(t, map[string]string{
		"ClockTest": "Процедура Выполнить()\n" +
			"  Часы.Установить(Дата(2020, 1, 1));\n" +
			"  Утверждать.Равно(ТекущаяДата(), Дата(2020, 1, 1), \"дата заморожена\");\n" +
			"  Утверждать.Равно(Год(ТекущаяДатаВремя()), 2020, \"год из замороженного времени\");\n" +
			"  Часы.Сбросить();\n" +
			"  Утверждать.Истина(Год(ТекущаяДата()) > 2020, \"после сброса — реальный год\");\n" +
			"КонецПроцедуры\n",
	})
	requireCaseOK(t, res, "ClockTest")
}

func TestProfile_EmailMock(t *testing.T) {
	res := runProfileTests(t, map[string]string{
		// shorthand ОтправитьПисьмо
		"AEmailShort": "Процедура Выполнить()\n" +
			"  Утверждать.Равно(Мок.Email.Количество(), 0, \"старт с чистого мока\");\n" +
			"  ОтправитьПисьмо(\"a@b.ru\", \"Привет\", \"Тело\");\n" +
			"  ОтправитьПисьмо(\"c@d.ru\", \"Ещё\", \"Тело2\");\n" +
			"  Утверждать.Равно(Мок.Email.Количество(), 2, \"2 письма записаны\");\n" +
			"  Утверждать.Равно(Мок.Email[0].Кому, \"a@b.ru\", \"адрес первого\");\n" +
			"  Утверждать.Равно(Мок.Email[1].Тема, \"Ещё\", \"тема второго\");\n" +
			"КонецПроцедуры\n",
		// объект ПисьмоEmail — проверяет изоляцию (мок пуст на старте)
		"BEmailObject": "Процедура Выполнить()\n" +
			"  Утверждать.Равно(Мок.Email.Количество(), 0, \"мок сброшен между тестами\");\n" +
			"  П = Новый ПисьмоEmail();\n" +
			"  П.Кому = \"x@y.ru\";\n" +
			"  П.Тема = \"Тема\";\n" +
			"  П.Текст = \"Тело\";\n" +
			"  П.Отправить();\n" +
			"  Утверждать.Равно(Мок.Email.Количество(), 1, \"объектное письмо записано\");\n" +
			"  Утверждать.Равно(Мок.Email[0].Кому, \"x@y.ru\", \"адрес из объекта\");\n" +
			"КонецПроцедуры\n",
	})
	requireCaseOK(t, res, "AEmailShort")
	requireCaseOK(t, res, "BEmailObject")
}

func TestProfile_HttpExecAiMocks(t *testing.T) {
	res := runProfileTests(t, map[string]string{
		"NetTest": "Процедура Выполнить()\n" +
			"  Отв = HTTPПолучить(\"https://example.com/api\");\n" +
			"  Утверждать.Равно(Мок.Http.Количество(), 1, \"http-вызов записан\");\n" +
			"  Утверждать.Равно(Мок.Http[0].URL, \"https://example.com/api\", \"url записан\");\n" +
			"  Утверждать.Равно(Мок.Http[0].Метод, \"GET\", \"метод GET\");\n" +
			"  Рез = ВыполнитьКоманду(\"ls -la\");\n" +
			"  Утверждать.Равно(Мок.ОС.Количество(), 1, \"команда записана\");\n" +
			"  Утверждать.Равно(Мок.ОС[0].Команда, \"ls -la\", \"текст команды\");\n" +
			"  Отв2 = ЗапросИИ(\"Сколько будет 2+2?\");\n" +
			"  Утверждать.Равно(Мок.ИИ.Количество(), 1, \"ИИ-запрос записан\");\n" +
			"  Утверждать.Равно(Мок.ИИ[0].Запрос, \"Сколько будет 2+2?\", \"текст запроса\");\n" +
			"КонецПроцедуры\n",
	})
	requireCaseOK(t, res, "NetTest")
}

func TestProfile_ReceiveEmailsNeverConnects(t *testing.T) {
	for _, enabled := range []bool{false, true} {
		t.Run(strconv.FormatBool(enabled), func(t *testing.T) {
			ln, err := net.Listen("tcp", "127.0.0.1:0")
			if err != nil {
				t.Fatal(err)
			}
			defer ln.Close() //nolint:errcheck
			var connections atomic.Int32
			done := make(chan struct{})
			go func() {
				defer close(done)
				for {
					conn, err := ln.Accept()
					if err != nil {
						return
					}
					connections.Add(1)
					_ = conn.Close()
				}
			}()
			host, port, _ := net.SplitHostPort(ln.Addr().String())
			procs := map[string]string{}
			for _, proto := range []string{"POP3", "IMAP"} {
				for i, name := range []string{"ПолучитьПисьма", "ReceiveEmails"} {
					procs[fmt.Sprintf("Receive%s%d", proto, i)] = fmt.Sprintf(`
Процедура Выполнить()
 Профиль = Новый Структура("Протокол, Сервер, Порт, Пользователь, Пароль, Защита, Таймаут", "%s", "%s", %s, "test", "test", "НЕТ", 1);
 Письма = %s(Профиль, Новый Структура("ОтметитьПрочитанными", Истина));
 Утверждать.Равно(Письма.Количество(), 0, "пустой ответ мока");
КонецПроцедуры
`, proto, host, port, name)
				}
			}
			res := runProfileTests(t, procs, enabled)
			_ = ln.Close()
			<-done
			if connections.Load() != 0 {
				t.Errorf("реальных подключений: %d", connections.Load())
			}
			for name := range procs {
				requireCaseOK(t, res, name)
			}
		})
	}
}
