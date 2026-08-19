package ui

// DSL-глобал ФоновыеЗадания (#848, план 130).
//
// Тесты идут публичным путём — DSL-код через buildDSLVars + interp.Run, как это
// делает форма или обработка. Правило из CLAUDE.md, повод — #611: зелёный тест
// на функции, которую боевой путь не зовёт, хуже отсутствующего.

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ivantit66/onebase/internal/dsl/ast"
	"github.com/ivantit66/onebase/internal/dsl/interpreter"
	"github.com/ivantit66/onebase/internal/dsl/lexer"
	"github.com/ivantit66/onebase/internal/dsl/parser"
	"github.com/ivantit66/onebase/internal/jobqueue"
	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/ivantit66/onebase/internal/processor"
	"github.com/ivantit66/onebase/internal/runtime"
	"github.com/ivantit66/onebase/internal/scheduler"
	"github.com/ivantit66/onebase/internal/storage"
)

// queueTestServer поднимает сервер с планировщиком, живой очередью и одним
// заданием, чья обработка исполняет переданный DSL-код.
func queueTestServer(t *testing.T, jobName, procBody string) *Server {
	t.Helper()
	ctx := context.Background()
	db, err := storage.ConnectSQLite(ctx, filepath.Join(t.TempDir(), "queue.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(db.Close)
	if err := db.Migrate(ctx, nil); err != nil {
		t.Fatal(err)
	}
	if err := db.EnsureServiceSchema(ctx); err != nil {
		t.Fatal(err)
	}

	src := "Процедура Выполнить(Узел = \"\")\n" + procBody + "\nКонецПроцедуры"
	prog, err := parser.New(lexer.New(src, "фоновая.proc.os")).ParseProgram()
	if err != nil {
		t.Fatal(err)
	}
	registry := runtime.NewRegistry()
	registry.Load(runtime.LoadOptions{Programs: map[string]*ast.Program{"ФоноваяОбработка": prog}})
	registry.LoadProcessors([]*processor.Processor{{
		Name:  "ФоноваяОбработка",
		Title: "Фоновая",
		// Параметр объявлен: очередь перекрывает params задания поимённо, а
		// runProcessor отдаёт обработке только объявленные параметры.
		Params: []processor.Param{{Name: "Узел", Type: "string"}},
	}})

	interp := interpreter.New()
	interp.LookupProc = registry.GetModuleProc

	sched := scheduler.New(db, registry, interp)
	// @every 100h — расписание валидное, но тикать в тесте не должно: проверяем
	// исполнение из очереди, а не по расписанию.
	if err := sched.ReloadProjectJobs([]*metadata.ScheduledJob{{
		Name:      jobName,
		Title:     jobName,
		Schedule:  "@every 100h",
		Processor: "ФоноваяОбработка",
		Enabled:   false,
		Timeout:   30,
	}}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = sched.Shutdown(context.Background()) })

	cfg := jobqueue.DefaultConfig()
	cfg.PollInterval = 20 * time.Millisecond
	cfg.Lease = 3 * time.Second
	cfg.RetryBackoff = 10 * time.Millisecond
	cfg.MaxAttempts = 1
	// Останов теста не должен ждать дольше самого теста: занятое задание
	// прервётся, строка вернётся в очередь развёрткой.
	cfg.DrainTimeout = 2 * time.Second
	pool := jobqueue.New(db, sched, cfg)
	queueCtx, queueCancel := context.WithCancel(context.Background())
	queueDone := make(chan struct{})
	go func() {
		defer close(queueDone)
		_ = pool.Run(queueCtx)
	}()
	t.Cleanup(func() {
		queueCancel()
		<-queueDone
	})

	return &Server{
		store:            db,
		reg:              registry,
		interp:           interp,
		sched:            sched,
		cfg:              Config{JobQueue: pool},
		lockMgr:          runtime.NewLockManager(),
		messages:         NewMessageStore(),
		maxFileSizeBytes: 1 << 20,
	}
}

// runQueueDSL исполняет тело процедуры так же, как это делает обработка.
func runQueueDSL(t *testing.T, s *Server, body string) ([]string, error) {
	t.Helper()
	prog, err := parser.New(lexer.New("Процедура Тест()\n"+body+"\nКонецПроцедуры", "тест.proc.os")).ParseProgram()
	if err != nil {
		t.Fatal(err)
	}
	var proc *ast.ProcedureDecl
	for _, p := range prog.Procedures {
		proc = p
	}
	var msgs []string
	vars, txState := s.buildDSLVarsWithMessagesTx(context.Background(), nil, &msgs)
	defer interpreter.RollbackTxExecution(txState)
	return msgs, s.interp.Run(proc, nil, vars)
}

// ждёмСтатусЗадачи опрашивает очередь тем же способом, что и прикладной код.
func ждёмСтатусЗадачи(t *testing.T, s *Server, id, want string) []string {
	t.Helper()
	deadline := time.Now().Add(15 * time.Second)
	last := ""
	for time.Now().Before(deadline) {
		msgs, err := runQueueDSL(t, s, `
			Задача = ФоновыеЗадания.Задача("`+id+`");
			Если Задача <> Неопределено Тогда
				Сообщить(Задача.Статус);
				Сообщить(Задача.Вывод);
			КонецЕсли;`)
		if err != nil {
			t.Fatalf("опрос задачи: %v", err)
		}
		if len(msgs) == 2 {
			last = msgs[0]
			if last == want {
				return msgs
			}
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("задача не дошла до статуса %q, последний = %q", want, last)
	return nil
}

func TestФоновыеЗадания_ПоставитьИсполняетЗаданиеСПараметрамиЗадачи(t *testing.T) {
	s := queueTestServer(t, "ОбменСУзлом", `Сообщить("обмен с узлом " + Узел);`)

	msgs, err := runQueueDSL(t, s, `
		Ид = ФоновыеЗадания.Поставить("ОбменСУзлом", Новый Структура("Узел", "N-042"));
		Сообщить(Ид);`)
	if err != nil {
		t.Fatalf("Поставить: %v", err)
	}
	if len(msgs) != 1 || strings.TrimSpace(msgs[0]) == "" {
		t.Fatalf("Поставить вернул %v, ожидался идентификатор задачи", msgs)
	}

	done := ждёмСтатусЗадачи(t, s, msgs[0], "done")
	// Параметр задачи обязан дойти до обработки: ради этого очередь и нужна —
	// 360 задач круга отличаются ровно узлом.
	if !strings.Contains(done[1], "обмен с узлом N-042") {
		t.Fatalf("вывод задачи = %q, ожидался обмен с узлом N-042", done[1])
	}
}

func TestФоновыеЗадания_ПоставитьВТранзакцииОткатываетсяВместеСНей(t *testing.T) {
	s := queueTestServer(t, "ОбменСУзлом", `Сообщить("исполнено");`)

	// Постановка внутри транзакции разрешена намеренно (в отличие от
	// РегламентныеЗадания.Запустить): задача — это строка, и она обязана
	// разделить судьбу данных, ради которых ставилась.
	msgs, err := runQueueDSL(t, s, `
		НачатьТранзакцию();
		Ид = ФоновыеЗадания.Поставить("ОбменСУзлом");
		Сообщить(Ид);
		ОтменитьТранзакцию();`)
	if err != nil {
		t.Fatalf("постановка в транзакции: %v", err)
	}
	if len(msgs) != 1 {
		t.Fatalf("Поставить в транзакции вернул %v", msgs)
	}

	// Откат снял задачу — исполнитель её не увидит ни сейчас, ни позже.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		found, err := runQueueDSL(t, s, `
			Задача = ФоновыеЗадания.Задача("`+msgs[0]+`");
			Если Задача <> Неопределено Тогда
				Сообщить("есть: " + Задача.Статус);
			КонецЕсли;`)
		if err != nil {
			t.Fatalf("чтение задачи: %v", err)
		}
		if len(found) != 0 {
			time.Sleep(50 * time.Millisecond)
			continue
		}
		return
	}
	t.Fatal("задача пережила откат транзакции инициатора")
}

func TestФоновыеЗадания_ОтказыНаНеизвестноеЗаданиеИЧужойИдентификатор(t *testing.T) {
	s := queueTestServer(t, "ОбменСУзлом", `Сообщить("исполнено");`)

	_, err := runQueueDSL(t, s, `ФоновыеЗадания.Поставить("НетТакого");`)
	if err == nil {
		t.Fatal("постановка несуществующего задания прошла молча")
	}
	if !strings.Contains(err.Error(), "НетТакого") {
		t.Fatalf("непонятная ошибка: %v", err)
	}

	// Чужой идентификатор — не ошибка: история могла быть подрезана ретенцией.
	msgs, err := runQueueDSL(t, s, `
		Если ФоновыеЗадания.Задача("11111111-2222-3333-4444-555555555555") = Неопределено Тогда
			Сообщить("нет такой");
		КонецЕсли;`)
	if err != nil {
		t.Fatalf("чтение чужой задачи: %v", err)
	}
	if len(msgs) != 1 || msgs[0] != "нет такой" {
		t.Fatalf("чужой идентификатор дал %v, ожидалось Неопределено", msgs)
	}

	// А мусор вместо идентификатора — ошибка кода, и молчать о ней нельзя.
	if _, err := runQueueDSL(t, s, `ФоновыеЗадания.Задача("не-uuid");`); err == nil {
		t.Fatal("мусор вместо идентификатора принят молча")
	}
}

func TestФоновыеЗадания_ГлубинаСчитаетОжидающихЗадач(t *testing.T) {
	// Задание держится в исполнении, пока не отпустят: так очередь гарантированно
	// не успевает разобрать поставленное.
	// Задание занимает исполнителя на несколько секунд: столько, чтобы очередь
	// гарантированно не успела разобрать поставленное, и не дольше.
	s := queueTestServer(t, "ДолгийОбмен", `
		Для Сч = 1 По 60 Цикл
			Приостановить(0.05);
		КонецЦикла;`)

	if _, err := runQueueDSL(t, s, `
		ФоновыеЗадания.Поставить("ДолгийОбмен", Неопределено, "задача-1");
		ФоновыеЗадания.Поставить("ДолгийОбмен", Неопределено, "задача-2");
		ФоновыеЗадания.Поставить("ДолгийОбмен", Неопределено, "задача-3");`); err != nil {
		t.Fatalf("постановка: %v", err)
	}

	// Один исполнитель (SQLite) взял одну задачу, две ждут.
	deadline := time.Now().Add(10 * time.Second)
	last := ""
	for time.Now().Before(deadline) {
		msgs, err := runQueueDSL(t, s, `Сообщить(ФоновыеЗадания.Глубина());`)
		if err != nil {
			t.Fatalf("Глубина: %v", err)
		}
		if len(msgs) == 1 {
			last = msgs[0]
			if last == "2" {
				return
			}
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("глубина очереди = %q, ожидалось 2", last)
}

func TestФоновыеЗадания_КлючИдемпотентностиВозвращаетТотЖеИдентификатор(t *testing.T) {
	// Задание занимает исполнителя на несколько секунд: столько, чтобы очередь
	// гарантированно не успела разобрать поставленное, и не дольше.
	s := queueTestServer(t, "ДолгийОбмен", `
		Для Сч = 1 По 60 Цикл
			Приостановить(0.05);
		КонецЦикла;`)

	msgs, err := runQueueDSL(t, s, `
		Первый = ФоновыеЗадания.Поставить("ДолгийОбмен", Неопределено, "обмен-N-042");
		Второй = ФоновыеЗадания.Поставить("ДолгийОбмен", Неопределено, "обмен-N-042");
		Сообщить(Первый);
		Сообщить(Второй);`)
	if err != nil {
		t.Fatalf("постановка с ключом: %v", err)
	}
	if len(msgs) != 2 || msgs[0] != msgs[1] {
		t.Fatalf("ключ идемпотентности дал разные задачи: %v", msgs)
	}
}
