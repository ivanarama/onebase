package ui

// DSL-глобал РегламентныеЗадания (#742, план 123).
//
// Тесты идут публичным путём — DSL-код через buildDSLVars + interp.Run, как это
// делает форма или обработка, — а не вызовом методов планировщика напрямую.
// Правило из CLAUDE.md, повод — #611: зелёный тест на функции, которую боевой
// путь не зовёт, хуже отсутствующего.

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
	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/ivantit66/onebase/internal/processor"
	"github.com/ivantit66/onebase/internal/runtime"
	"github.com/ivantit66/onebase/internal/scheduler"
	"github.com/ivantit66/onebase/internal/storage"
)

// schedTestServer поднимает сервер с планировщиком и одним заданием, чья
// обработка исполняет переданный DSL-код.
func schedTestServer(t *testing.T, jobName, procBody string, enabled bool) *Server {
	t.Helper()
	ctx := context.Background()
	db, err := storage.ConnectSQLite(ctx, filepath.Join(t.TempDir(), "sched.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(db.Close)
	if err := db.Migrate(ctx, nil); err != nil {
		t.Fatal(err)
	}
	if err := db.EnsureScheduledRunsTable(ctx); err != nil {
		t.Fatal(err)
	}

	src := "Процедура Выполнить()\n" + procBody + "\nКонецПроцедуры"
	prog, err := parser.New(lexer.New(src, "фоновая.proc.os")).ParseProgram()
	if err != nil {
		t.Fatal(err)
	}
	registry := runtime.NewRegistry()
	registry.Load(runtime.LoadOptions{Programs: map[string]*ast.Program{"ФоноваяОбработка": prog}})
	registry.LoadProcessors([]*processor.Processor{{Name: "ФоноваяОбработка", Title: "Фоновая"}})

	interp := interpreter.New()
	interp.LookupProc = registry.GetModuleProc

	sched := scheduler.New(db, registry, interp)
	// @every 100h — расписание должно быть валидным, но тикать в тесте не
	// должно: проверяем именно запуск по требованию.
	if err := sched.ReloadProjectJobs([]*metadata.ScheduledJob{{
		Name:      jobName,
		Title:     jobName,
		Schedule:  "@every 100h",
		Processor: "ФоноваяОбработка",
		Enabled:   enabled,
		Timeout:   30,
	}}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = sched.Shutdown(context.Background()) })

	return &Server{
		store:            db,
		reg:              registry,
		interp:           interp,
		sched:            sched,
		lockMgr:          runtime.NewLockManager(),
		messages:         NewMessageStore(),
		maxFileSizeBytes: 1 << 20,
	}
}

// runSchedDSL исполняет тело процедуры так же, как это делает обработка.
func runSchedDSL(t *testing.T, s *Server, body string) ([]string, error) {
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

// ждёмСтатус опрашивает журнал, пока прогон не станет терминальным.
func ждёмСтатус(t *testing.T, s *Server, runID string, want string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	var last string
	for time.Now().Before(deadline) {
		msgs, err := runSchedDSL(t, s, `Сообщить(РегламентныеЗадания.Прогон("`+runID+`").Статус);`)
		if err != nil {
			t.Fatalf("опрос прогона: %v", err)
		}
		if len(msgs) == 1 {
			last = msgs[0]
			if last == want {
				return
			}
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("прогон не дошёл до статуса %q, последний = %q", want, last)
}

// Основной сценарий: запустили, получили идентификатор, дождались успеха.
func TestРегламентныеЗадания_ЗапускИОпросСтатуса(t *testing.T) {
	s := schedTestServer(t, "ОбменСУзлами", `Сообщить("обмен выполнен");`, true)

	msgs, err := runSchedDSL(t, s, `Сообщить(РегламентныеЗадания.Запустить("ОбменСУзлами"));`)
	if err != nil {
		t.Fatalf("Запустить: %v", err)
	}
	if len(msgs) != 1 {
		t.Fatalf("сообщения: %v", msgs)
	}
	runID := msgs[0]
	// Идентификатор — не пустой и похож на UUID: именно его прикладной код
	// сохранит, чтобы потом спросить статус.
	if len(runID) != 36 || strings.Count(runID, "-") != 4 {
		t.Fatalf("Запустить вернул %q, ожидался идентификатор прогона", runID)
	}

	// Прогон виден в журнале СРАЗУ — вставка синхронная, до возврата
	// управления. Это и есть смысл правки планировщика.
	runs, err := s.store.ScheduledRuns(context.Background(), "ОбменСУзлами", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(runs) != 1 || runs[0].ID.String() != runID {
		t.Fatalf("прогон не появился в журнале сразу после Запустить: %+v", runs)
	}

	ждёмСтатус(t, s, runID, "success")
}

// Структура прогона отдаёт то, что обещано справкой.
func TestРегламентныеЗадания_ПрогонОтдаётПоляЖурнала(t *testing.T) {
	s := schedTestServer(t, "Отчёт", `Сообщить("готово");`, true)
	msgs, err := runSchedDSL(t, s, `Сообщить(РегламентныеЗадания.Запустить("Отчёт"));`)
	if err != nil {
		t.Fatal(err)
	}
	runID := msgs[0]
	ждёмСтатус(t, s, runID, "success")

	msgs, err = runSchedDSL(t, s, `
  Прогон = РегламентныеЗадания.Прогон("`+runID+`");
  Сообщить(Прогон.Задание);
  Сообщить(Прогон.Статус);
  Сообщить(Прогон.Ошибка);
  Сообщить(Прогон.Вывод);`)
	if err != nil {
		t.Fatalf("чтение прогона: %v", err)
	}
	if len(msgs) != 4 {
		t.Fatalf("сообщения: %v", msgs)
	}
	if msgs[0] != "Отчёт" || msgs[1] != "success" || msgs[2] != "" {
		t.Fatalf("поля прогона: %v", msgs)
	}
	if !strings.Contains(msgs[3], "готово") {
		t.Errorf("Вывод не содержит Сообщить() задания: %q", msgs[3])
	}
}

// «Уже выполняется» — штатная гонка с cron-тиком, и она обязана ловиться
// Попытка/Исключение. Если бы ошибка поднималась не через RaiseUserError,
// исключение прошло бы мимо Попытки и оборвало весь запуск.
func TestРегламентныеЗадания_ЗанятоеЗаданиеЛовитсяПопыткой(t *testing.T) {
	// Задание держим занятым выдержкой — конечной, чтобы тест не зависел от
	// таймаута задания и не оставлял крутящуюся горутину после себя.
	s := schedTestServer(t, "Долгое", `Приостановить(3);`, true)

	if _, err := runSchedDSL(t, s, `РегламентныеЗадания.Запустить("Долгое");`); err != nil {
		t.Fatalf("первый запуск: %v", err)
	}

	msgs, err := runSchedDSL(t, s, `
  Попытка
    РегламентныеЗадания.Запустить("Долгое");
    Сообщить("запустилось повторно");
  Исключение
    Сообщить("поймано: " + ОписаниеОшибки());
  КонецПопытки;`)
	if err != nil {
		t.Fatalf("повторный запуск оборвал выполнение вместо исключения: %v", err)
	}
	if len(msgs) != 1 || !strings.HasPrefix(msgs[0], "поймано:") {
		t.Fatalf("ожидалось перехваченное исключение, получено: %v", msgs)
	}
	if !strings.Contains(msgs[0], "уже выполняется") {
		t.Errorf("текст исключения не объясняет причину: %q", msgs[0])
	}
}

func TestРегламентныеЗадания_НеизвестноеЗаданиеДаётИсключение(t *testing.T) {
	s := schedTestServer(t, "Настоящее", `Сообщить("ок");`, true)
	msgs, err := runSchedDSL(t, s, `
  Попытка
    РегламентныеЗадания.Запустить("ТакогоНет");
  Исключение
    Сообщить(ОписаниеОшибки());
  КонецПопытки;`)
	if err != nil {
		t.Fatalf("DSL: %v", err)
	}
	if len(msgs) != 1 || !strings.Contains(msgs[0], "ТакогоНет") {
		t.Fatalf("исключение не называет задание: %v", msgs)
	}
	// Текст обязан отличаться от «уже выполняется»: это разные проблемы.
	if strings.Contains(msgs[0], "уже выполняется") {
		t.Errorf("«не найдено» неотличимо от «уже выполняется»: %q", msgs[0])
	}
}

// Неизвестный идентификатор — Неопределено, а не исключение: запись могла быть
// подрезана, а id пережить её в прикладных данных.
func TestРегламентныеЗадания_ЧужойИдентификаторДаётНеопределено(t *testing.T) {
	s := schedTestServer(t, "Задание", `Сообщить("ок");`, true)
	msgs, err := runSchedDSL(t, s, `
  Прогон = РегламентныеЗадания.Прогон("6f1b6b7e-0000-4000-8000-000000000000");
  Если Прогон = Неопределено Тогда
    Сообщить("неопределено");
  Иначе
    Сообщить("вернулась структура");
  КонецЕсли;`)
	if err != nil {
		t.Fatalf("DSL: %v", err)
	}
	if len(msgs) != 1 || msgs[0] != "неопределено" {
		t.Fatalf("ожидалось Неопределено, получено: %v", msgs)
	}
}

// А вот мусор вместо идентификатора — ошибка кода, и молчать о ней нельзя:
// иначе опечатка всплывёт только при разборе инцидента.
func TestРегламентныеЗадания_МусорВместоИдентификатораДаётИсключение(t *testing.T) {
	s := schedTestServer(t, "Задание", `Сообщить("ок");`, true)
	msgs, err := runSchedDSL(t, s, `
  Попытка
    РегламентныеЗадания.Прогон("не-идентификатор");
    Сообщить("принято молча");
  Исключение
    Сообщить("отказ");
  КонецПопытки;`)
	if err != nil {
		t.Fatalf("DSL: %v", err)
	}
	if len(msgs) != 1 || msgs[0] != "отказ" {
		t.Fatalf("мусорный идентификатор принят молча: %v", msgs)
	}
}

// enabled: false не мешает запуску по требованию — это штатный способ держать
// «задание без расписания». Тест фиксирует поведение, чтобы его не «починили».
func TestРегламентныеЗадания_ВыключенноеЗаданиеЗапускается(t *testing.T) {
	s := schedTestServer(t, "ПоТребованию", `Сообщить("сработало");`, false)
	msgs, err := runSchedDSL(t, s, `Сообщить(РегламентныеЗадания.Запустить("ПоТребованию"));`)
	if err != nil {
		t.Fatalf("выключенное задание не запустилось: %v", err)
	}
	ждёмСтатус(t, s, msgs[0], "success")
}

// Упавшее задание доезжает до инициатора статусом и текстом ошибки.
func TestРегламентныеЗадания_УпавшееЗаданиеВидноВПрогоне(t *testing.T) {
	s := schedTestServer(t, "Сломанное", `ВызватьИсключение "узел N-217 не ответил";`, true)
	msgs, err := runSchedDSL(t, s, `Сообщить(РегламентныеЗадания.Запустить("Сломанное"));`)
	if err != nil {
		t.Fatal(err)
	}
	runID := msgs[0]
	ждёмСтатус(t, s, runID, "error")

	msgs, err = runSchedDSL(t, s, `Сообщить(РегламентныеЗадания.Прогон("`+runID+`").Ошибка);`)
	if err != nil {
		t.Fatal(err)
	}
	if len(msgs) != 1 || !strings.Contains(msgs[0], "N-217") {
		t.Fatalf("ошибка задания не доехала до инициатора: %v", msgs)
	}
}

// Сервер без планировщика — это procrun и раннер конфигтестов. Глобал там
// инжектируется (точка инжекции одна на все контексты), поэтому обязан внятно
// отказать, а не уронить процесс паникой внутри планировщика.
func TestРегламентныеЗадания_БезПланировщикаВнятныйОтказ(t *testing.T) {
	s := schedTestServer(t, "Задание", `Сообщить("ок");`, true)
	s.sched = nil // ровно то, что делает ui.NewOfflineServer

	msgs, err := runSchedDSL(t, s, `
  Попытка
    РегламентныеЗадания.Запустить("Задание");
    Сообщить("запустилось без планировщика");
  Исключение
    Сообщить(ОписаниеОшибки());
  КонецПопытки;`)
	if err != nil {
		t.Fatalf("DSL: %v", err)
	}
	if len(msgs) != 1 || !strings.Contains(msgs[0], "планировщик недоступен") {
		t.Fatalf("ожидалось объяснение про недоступный планировщик, получено: %v", msgs)
	}
}

// Длительность склеивается со строкой по-человечески. Регрессия: пока поле
// отдавалось как float64, часовой обмен печатался как «3.6e+06 мс» — и ровно в
// том виде, в каком пример приведён в справке.
func TestРегламентныеЗадания_ДлительностьНеУходитВНаучнуюНотацию(t *testing.T) {
	s := schedTestServer(t, "Длительное", `Сообщить("ок");`, true)
	msgs, err := runSchedDSL(t, s, `Сообщить(РегламентныеЗадания.Запустить("Длительное"));`)
	if err != nil {
		t.Fatal(err)
	}
	runID := msgs[0]
	ждёмСтатус(t, s, runID, "success")

	// Час работы — обычная длительность для обмена по сотням узлов.
	if _, err := s.store.Exec(context.Background(),
		`UPDATE _scheduled_runs SET duration_ms=3600000 WHERE id=?`, runID); err != nil {
		t.Fatal(err)
	}
	msgs, err = runSchedDSL(t, s,
		`Сообщить("Обмен занял " + РегламентныеЗадания.Прогон("`+runID+`").ДлительностьМс + " мс");`)
	if err != nil {
		t.Fatal(err)
	}
	if len(msgs) != 1 || msgs[0] != "Обмен занял 3600000 мс" {
		t.Fatalf("длительность склеена нечитаемо: %q", msgs[0])
	}
}

// Запуск внутри транзакции — отказ. Без него на SQLite (пул из одного
// соединения) инициатор и задание ждали бы друг друга до таймаута задания, и
// снаружи это выглядело бы зависшим сервером.
func TestРегламентныеЗадания_ЗапускВТранзакцииОтвергается(t *testing.T) {
	s := schedTestServer(t, "Задание", `Сообщить("ок");`, true)
	msgs, err := runSchedDSL(t, s, `
  НачатьТранзакцию();
  Попытка
    РегламентныеЗадания.Запустить("Задание");
    Сообщить("запустилось внутри транзакции");
  Исключение
    Сообщить("отказ: " + ОписаниеОшибки());
  КонецПопытки;
  ОтменитьТранзакцию();`)
	if err != nil {
		t.Fatalf("DSL: %v", err)
	}
	if len(msgs) != 1 || !strings.HasPrefix(msgs[0], "отказ:") {
		t.Fatalf("запуск в транзакции не отвергнут: %v", msgs)
	}
	if !strings.Contains(msgs[0], "транзакции") {
		t.Errorf("текст отказа не объясняет причину: %q", msgs[0])
	}
	// Прогона быть не должно: отказ случился до планировщика.
	runs, err := s.store.ScheduledRuns(context.Background(), "Задание", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(runs) != 0 {
		t.Fatalf("отказ оставил запись прогона: %+v", runs)
	}
}
