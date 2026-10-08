package ui

// БлокировкаДанных вне записи документа: ключ отпускается в конце исполнения,
// а повторная блокировка того же ключа той же операцией не ждёт саму себя.
//
// Сборщик блокировок (LockCollector) заводили только запись и проведение
// документа. В обработке, событии формы, регламентном задании и тестах
// конфигурации объект БлокировкаДанных оставался без сборщика: ключ без явного
// Разблокировать() не отпускался до конца процесса, и следующий запрос с тем же
// ключом висел навсегда. А второй Заблокировать того же ключа в той же операции
// (в одной транзакции или во вложенном проведении) ждал сам себя — так завис
// тест конфигурации торговой конфигурации PuT.
//
// Путь публичный: RunProcessor — тот же, что procrun и раннер onebase test.
// Каждый запуск ограничен по времени: до исправления сценарии не падали, а
// висели.

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/ivantit66/onebase/internal/project"
	"github.com/ivantit66/onebase/internal/runtime"
	"github.com/ivantit66/onebase/internal/storage"
)

const dlLockK1 = `  Б = БлокировкаДанных();
  Э = Б.Добавить("Проба");
  Э.УстановитьЗначение("Ключ", "К1");
  Б.Заблокировать();
`

var dlProcessors = map[string]string{
	// Берёт ключ и не отпускает его явно.
	"замок": "Процедура Выполнить()\n" + dlLockK1 + "  Сообщить(\"взял\");\nКонецПроцедуры\n",
	// Два объекта блокировки на один ключ в одной транзакции.
	"двазамка": `Процедура Выполнить()
  НачатьТранзакцию();
  Б1 = БлокировкаДанных();
  Э = Б1.Добавить("Проба");
  Э.УстановитьЗначение("Ключ", "К1");
  Б1.Заблокировать();
  Б2 = БлокировкаДанных();
  Э = Б2.Добавить("Проба");
  Э.УстановитьЗначение("Ключ", "К1");
  Б2.Заблокировать();
  ЗафиксироватьТранзакцию();
  Сообщить("оба");
КонецПроцедуры
`,
	// Держит ключ дольше, чтобы параллельный запуск его ждал.
	"долгийзамок": "Процедура Выполнить()\n" + dlLockK1 + "  Приостановить(1);\n  Сообщить(\"отпустил\");\nКонецПроцедуры\n",
	// Держит ключ и проводит документ, проведение которого берёт тот же ключ.
	"вложенныйзамок": "Процедура Выполнить()\n" + dlLockK1 + `  Д = Документы.ЗамокДок.Создать();
  Д.Дата = ТекущаяДата();
  Д.Провести();
  Сообщить("провёл");
КонецПроцедуры
`,
	// Проводит документ (ключ берёт проведение) и потом ещё работает: ключ
	// проведения обязан отпуститься в конце записи, а не в конце обработки.
	"провестииждать": `Процедура Выполнить()
  Д = Документы.ЗамокДок.Создать();
  Д.Дата = ТекущаяДата();
  Д.Провести();
  Приостановить(1);
  Сообщить("дождался");
КонецПроцедуры
`,
}

func dlServer(t *testing.T) (*Server, *runtime.Registry) {
	t.Helper()
	dir := t.TempDir()
	for _, sub := range []string{"documents", "processors", "src"} {
		if err := os.MkdirAll(filepath.Join(dir, sub), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	write := func(rel, text string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(dir, rel), []byte(text), 0o644); err != nil { //nolint:gosec // G703: rel — имена из констант теста, dir — t.TempDir()
			t.Fatal(err)
		}
	}
	write("documents/замокдок.yaml", "name: ЗамокДок\nposting: true\nfields:\n  - {name: Дата, type: date}\n")
	write("src/замокдок.posting.os", "Процедура ОбработкаПроведения()\n"+dlLockK1+"КонецПроцедуры\n")
	for name, src := range dlProcessors {
		write("processors/"+name+".yaml", "name: "+name+"\n")
		write("src/"+name+".proc.os", src)
	}
	ctx := context.Background()
	proj, err := project.Load(dir)
	if err != nil {
		t.Fatalf("project.Load: %v", err)
	}
	t.Cleanup(func() { proj.Close() })
	db, err := storage.ConnectSQLite(ctx, filepath.Join(t.TempDir(), "locks.db"))
	if err != nil {
		t.Fatalf("ConnectSQLite: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	if err := db.Migrate(ctx, proj.Entities); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	s, reg, err := NewOfflineServer(proj, db)
	if err != nil {
		t.Fatalf("NewOfflineServer: %v", err)
	}
	return s, reg
}

type dlResult struct {
	msgs []string
	err  error
	at   time.Time
}

// dlRun запускает обработку в отдельной горутине; результат приходит в канал.
func dlRun(s *Server, reg *runtime.Registry, name string) <-chan dlResult {
	out := make(chan dlResult, 1)
	go func() {
		msgs, runErr, err := s.RunProcessor(context.Background(), reg, name, nil, nil, nil)
		if err == nil {
			err = runErr
		}
		out <- dlResult{msgs: msgs, err: err, at: time.Now()}
	}()
	return out
}

func dlWait(t *testing.T, ch <-chan dlResult, what string) dlResult {
	t.Helper()
	select {
	case r := <-ch:
		if r.err != nil {
			t.Fatalf("%s: %v (сообщения %q)", what, r.err, r.msgs)
		}
		return r
	case <-time.After(10 * time.Second):
		t.Fatalf("%s: завис — блокировка не отпущена", what)
		return dlResult{}
	}
}

func TestDataLockReleasedAtExecutionEnd(t *testing.T) {
	s, reg := dlServer(t)
	dlWait(t, dlRun(s, reg, "замок"), "первый запуск")
	dlWait(t, dlRun(s, reg, "замок"), "второй запуск с тем же ключом")
}

func TestDataLockReentrantInOneTransaction(t *testing.T) {
	s, reg := dlServer(t)
	r := dlWait(t, dlRun(s, reg, "двазамка"), "два объекта блокировки на один ключ")
	if len(r.msgs) != 1 || r.msgs[0] != "оба" {
		t.Fatalf("сообщения %q", r.msgs)
	}
	dlWait(t, dlRun(s, reg, "замок"), "ключ после транзакции")
}

func TestDataLockReentrantInNestedPosting(t *testing.T) {
	s, reg := dlServer(t)
	dlWait(t, dlRun(s, reg, "вложенныйзамок"), "проведение, берущее ключ обработки")
	dlWait(t, dlRun(s, reg, "замок"), "ключ после обработки")
}

func TestDataLockStillExcludesConcurrentExecutions(t *testing.T) {
	s, reg := dlServer(t)
	start := time.Now()
	long := dlRun(s, reg, "долгийзамок")
	time.Sleep(300 * time.Millisecond)
	short := dlRun(s, reg, "замок")
	l := dlWait(t, long, "долгая блокировка")
	sh := dlWait(t, short, "параллельная блокировка")
	if sh.at.Before(l.at) {
		t.Fatalf("параллельный запуск взял ключ через %v, пока его держала другая обработка (та закончила через %v)",
			sh.at.Sub(start), l.at.Sub(start))
	}
}

func TestDataLockOfPostingReleasedAtWriteEnd(t *testing.T) {
	s, reg := dlServer(t)
	long := dlRun(s, reg, "провестииждать")
	time.Sleep(400 * time.Millisecond)
	short := dlRun(s, reg, "замок")
	sh := dlWait(t, short, "блокировка после проведения")
	l := dlWait(t, long, "обработка с проведением")
	if !sh.at.Before(l.at) {
		t.Fatalf("ключ проведения держался до конца обработки: параллельный запуск закончился после неё")
	}
}
