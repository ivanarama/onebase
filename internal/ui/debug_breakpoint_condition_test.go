package ui

// Условные точки останова (план 99). Проверка идёт публичным путём, которым
// пользуется человек: обработчик /debug/global/* ставит точку с условием, DSL
// исполняется обычным прогоном обработки, а отладчик решает, останавливаться ли
// на строке. Приватные CheckBreakpoint/ShouldStep напрямую не зовём: до этого
// поле Condition сохранялось и возвращалось в UI, но в вычислении не
// участвовало вообще — тест на приватной функции такое расхождение и пропустил
// бы (повод — правило CLAUDE.md про #611).

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ivantit66/onebase/internal/debugger"
	"github.com/ivantit66/onebase/internal/project"
	"github.com/ivantit66/onebase/internal/runtime"
	"github.com/ivantit66/onebase/internal/storage"
)

// Модуль обработки. Точка останова ставится на строку 4 — тело цикла,
// выполняемое пять раз. Итог на входе в строку 4 равен сумме предыдущих Сч.
const debugLoopModule = `Процедура Выполнить()
  Итог = 0;
  Для Сч = 1 По 5 Цикл
    Итог = Итог + Сч;
  КонецЦикла;
  Сообщить("Итог: " + Строка(Итог));
КонецПроцедуры
`

const debugLoopBodyLine = 4

// debugLoopModuleWithFunc — тот же цикл плюс функция модуля, которую можно
// вызвать из условия точки останова.
const debugLoopModuleWithFunc = `Процедура Выполнить()
  Итог = 0;
  Для Сч = 1 По 5 Цикл
    Итог = Итог + Сч;
  КонецЦикла;
  Сообщить("Итог: " + Строка(Итог));
КонецПроцедуры

Функция Удвоить(Знч)
  Возврат Знч * 2;
КонецФункции
`

// debugLoopModuleCallingFunc — цикл вызывает функцию модуля, поэтому точку
// останова можно поставить внутрь самой функции.
const debugLoopModuleCallingFunc = `Процедура Выполнить()
  Итог = 0;
  Для Сч = 1 По 5 Цикл
    Итог = Итог + Удвоить(Сч);
  КонецЦикла;
  Сообщить("Итог: " + Строка(Итог));
КонецПроцедуры

Функция Удвоить(Знч)
  Возврат Знч * 2;
КонецФункции
`

const debugFuncBodyLine = 10

// debugSession — офлайн-сервер с включённой сессией отладки и загруженной
// обработкой «ЦиклОтладки».
type debugSession struct {
	server *Server
	reg    *runtime.Registry
	sess   *debugger.ActiveSession
}

func newDebugSession(t *testing.T, moduleOS string) *debugSession {
	t.Helper()
	dir := t.TempDir()
	for _, sub := range []string{"processors", "src"} {
		if err := os.MkdirAll(filepath.Join(dir, sub), 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", sub, err)
		}
	}
	if err := os.WriteFile(filepath.Join(dir, "processors", "циклотладки.yaml"),
		[]byte("name: ЦиклОтладки\ntitle: Цикл отладки\n"), 0o644); err != nil {
		t.Fatalf("запись обработки: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "src", "циклотладки.proc.os"), []byte(moduleOS), 0o644); err != nil {
		t.Fatalf("запись модуля: %v", err)
	}

	proj, err := project.Load(dir)
	if err != nil {
		t.Fatalf("project.Load: %v", err)
	}
	t.Cleanup(func() { proj.Close() })

	db, err := storage.ConnectSQLite(context.Background(), filepath.Join(t.TempDir(), "debug.db"))
	if err != nil {
		t.Fatalf("ConnectSQLite: %v", err)
	}
	t.Cleanup(func() { db.Close() })

	s, reg, err := NewOfflineServer(proj, db)
	if err != nil {
		t.Fatalf("NewOfflineServer: %v", err)
	}

	// Включаем отладку тем же обработчиком, что и конфигуратор.
	rec := httptest.NewRecorder()
	s.debugGlobalEnable(rec, httptest.NewRequest("POST", "/debug/global/enable", nil))
	if rec.Code != 200 {
		t.Fatalf("включение отладки: код %d, тело %s", rec.Code, rec.Body.String())
	}
	sess := s.globalDebug.Session()
	if sess == nil {
		t.Fatal("сессия отладки не создана")
	}
	t.Cleanup(func() { s.globalDebug.Disable() })
	return &debugSession{server: s, reg: reg, sess: sess}
}

// setBreakpoint ставит точку останова через HTTP-обработчик и возвращает ответ.
func (d *debugSession) setBreakpoint(t *testing.T, line int, condition string) (int, map[string]any) {
	t.Helper()
	body, err := json.Marshal(map[string]any{
		"file": "циклотладки.proc.os", "line": line, "action": "set", "condition": condition,
	})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	rec := httptest.NewRecorder()
	d.server.debugGlobalBreakpoint(rec, httptest.NewRequest("POST", "/debug/global/breakpoint", strings.NewReader(string(body))))
	var resp map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("ответ обработчика не JSON (%d): %s", rec.Code, rec.Body.String())
	}
	return rec.Code, resp
}

// run запускает обработку в отдельной горутине: остановка блокирует поток
// интерпретатора, как и на живом сервере. wantTotal — сообщение, которым
// обработка обязана завершиться: отладка не должна менять результат счёта.
func (d *debugSession) run(t *testing.T, wantTotal string) <-chan error {
	t.Helper()
	done := make(chan error, 1)
	go func() {
		msgs, runErr, err := d.server.RunProcessor(context.Background(), d.reg, "ЦиклОтладки", nil, nil, nil)
		switch {
		case err != nil:
			done <- fmt.Errorf("запуск обработки: %w", err)
		case runErr != nil:
			done <- fmt.Errorf("выполнение обработки: %w", runErr)
		case len(msgs) == 0 || !strings.Contains(msgs[len(msgs)-1], wantTotal):
			done <- fmt.Errorf("обработка не досчитала цикл (ожидалось %q), сообщения: %v", wantTotal, msgs)
		default:
			done <- nil
		}
	}()
	return done
}

// waitPause ждёт остановки интерпретатора и возвращает снимок состояния.
func (d *debugSession) waitPause(t *testing.T) debugger.StatusSnapshot {
	t.Helper()
	select {
	case <-d.sess.PauseChan():
	case <-time.After(10 * time.Second):
		t.Fatal("интерпретатор не остановился на точке останова")
	}
	return d.sess.Snapshot()
}

func (d *debugSession) waitFinish(t *testing.T, done <-chan error) {
	t.Helper()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("обработка завершилась с ошибкой: %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("обработка не завершилась: похоже, отладчик остановился ещё раз")
	}
}

// snapshotVar достаёт переменную из снимка. Имена в окружении DSL лежат в
// нижнем регистре (язык регистронезависим), поэтому сравниваем без учёта регистра.
func snapshotVar(snap debugger.StatusSnapshot, name string) (string, bool) {
	for _, v := range snap.Variables {
		if strings.EqualFold(v.Name, name) {
			return v.Value, true
		}
	}
	return "", false
}

func (d *debugSession) breakpoint(t *testing.T, line int) *debugger.Breakpoint {
	t.Helper()
	bp := d.sess.FindBreakpoint("циклотладки.proc.os", line)
	if bp == nil {
		t.Fatalf("точка останова на строке %d не найдена", line)
	}
	return bp
}

// Точка с условием останавливает ровно на том проходе цикла, где условие
// истинно, — а не на первом, как безусловная.
func TestBreakpointCondition_StopsOnMatchingIterationOnly(t *testing.T) {
	d := newDebugSession(t, debugLoopModule)
	if code, resp := d.setBreakpoint(t, debugLoopBodyLine, "Сч = 4"); code != 200 {
		t.Fatalf("установка точки: код %d, ответ %v", code, resp)
	} else if resp["condition"] != "Сч = 4" {
		t.Fatalf("обработчик не вернул условие: %v", resp)
	}

	done := d.run(t, "Итог: 15")
	snap := d.waitPause(t)

	if snap.State != debugger.StatePaused {
		t.Fatalf("состояние %v, ожидалось paused", snap.State)
	}
	if snap.Location == nil || snap.Location.Line != debugLoopBodyLine {
		t.Fatalf("остановка не на строке %d: %+v", debugLoopBodyLine, snap.Location)
	}
	if got, ok := snapshotVar(snap, "Сч"); !ok || got != "4" {
		t.Fatalf("Сч = %q (найдено=%v), ожидалось 4 — остановились не на том проходе", got, ok)
	}
	// На входе в строку 4 при Сч=4 накоплено 1+2+3.
	if got, ok := snapshotVar(snap, "Итог"); !ok || got != "6" {
		t.Fatalf("Итог = %q (найдено=%v), ожидалось 6", got, ok)
	}

	d.sess.Continue()
	d.waitFinish(t, done)

	bp := d.breakpoint(t, debugLoopBodyLine)
	if bp.HitCount != 1 {
		t.Fatalf("срабатываний %d, ожидалось 1", bp.HitCount)
	}
	if bp.SkipCount != 4 {
		t.Fatalf("пропусков по условию %d, ожидалось 4 (проходы 1,2,3,5)", bp.SkipCount)
	}
	if bp.CondError != "" {
		t.Fatalf("ошибка условия там, где её быть не должно: %s", bp.CondError)
	}
}

// Условие может вызвать функцию модуля: вычисление идёт на потоке
// интерпретатора и само исполняет DSL. Без предохранителя это либо уходило бы
// в рекурсию через ту же строку, либо вставало на мьютексе сессии.
func TestBreakpointCondition_MayCallModuleFunction(t *testing.T) {
	d := newDebugSession(t, debugLoopModuleWithFunc)
	if code, resp := d.setBreakpoint(t, debugLoopBodyLine, "Удвоить(Сч) > 6"); code != 200 {
		t.Fatalf("установка точки: код %d, ответ %v", code, resp)
	}

	done := d.run(t, "Итог: 15")
	snap := d.waitPause(t)

	if got, ok := snapshotVar(snap, "Сч"); !ok || got != "4" {
		t.Fatalf("Сч = %q (найдено=%v), ожидалось 4 — первый проход, где Удвоить(Сч) > 6", got, ok)
	}

	d.sess.Continue()
	// Второй раз условие истинно на Сч=5 — точка обязана сработать снова.
	snap = d.waitPause(t)
	if got, ok := snapshotVar(snap, "Сч"); !ok || got != "5" {
		t.Fatalf("Сч = %q (найдено=%v), ожидалось 5", got, ok)
	}
	d.sess.Continue()
	d.waitFinish(t, done)

	bp := d.breakpoint(t, debugLoopBodyLine)
	if bp.HitCount != 2 {
		t.Fatalf("срабатываний %d, ожидалось 2 (Сч=4 и Сч=5)", bp.HitCount)
	}
	if bp.SkipCount != 3 {
		t.Fatalf("пропусков %d, ожидалось 3", bp.SkipCount)
	}
}

// Условие на строке, которую само же условие и выполняет: `Удвоить(1) > 0` на
// теле функции Удвоить. Вычисление обязано идти без точек останова — иначе
// проверка условия входит в ту же строку и уходит в бесконечную рекурсию.
func TestBreakpointCondition_SelfReferencingConditionDoesNotRecurse(t *testing.T) {
	d := newDebugSession(t, debugLoopModuleCallingFunc)
	if code, resp := d.setBreakpoint(t, debugFuncBodyLine, "Удвоить(1) > 0"); code != 200 {
		t.Fatalf("установка точки: код %d, ответ %v", code, resp)
	}

	done := d.run(t, "Итог: 30")
	for i := 1; i <= 5; i++ {
		snap := d.waitPause(t)
		if snap.Location == nil || snap.Location.Line != debugFuncBodyLine {
			t.Fatalf("остановка %d не на строке %d: %+v", i, debugFuncBodyLine, snap.Location)
		}
		d.sess.Continue()
	}
	d.waitFinish(t, done)

	bp := d.breakpoint(t, debugFuncBodyLine)
	if bp.HitCount != 5 {
		t.Fatalf("срабатываний %d, ожидалось 5 (по одному вызову функции на проход цикла)", bp.HitCount)
	}
	if bp.CondError != "" {
		t.Fatalf("условие не вычислилось: %s", bp.CondError)
	}
}

// Условие, которое не вычисляется, не должно ни ронять отлаживаемый прогон, ни
// молча выключать точку: останавливаемся и показываем текст ошибки.
func TestBreakpointCondition_BrokenConditionStopsAndReportsError(t *testing.T) {
	d := newDebugSession(t, debugLoopModule)
	if code, resp := d.setBreakpoint(t, debugLoopBodyLine, "Сч / 0 > 1"); code != 200 {
		t.Fatalf("установка точки: код %d, ответ %v", code, resp)
	}

	done := d.run(t, "Итог: 15")
	snap := d.waitPause(t)
	if got, ok := snapshotVar(snap, "Сч"); !ok || got != "1" {
		t.Fatalf("Сч = %q (найдено=%v), ожидалось 1: на сломанном условии останавливаемся сразу", got, ok)
	}

	bp := d.breakpoint(t, debugLoopBodyLine)
	if bp.CondError == "" {
		t.Fatal("ошибка условия не сохранена — человеку нечем объяснить остановку")
	}
	if !strings.Contains(strings.ToLower(bp.CondError), "деление на ноль") {
		t.Fatalf("текст ошибки условия %q не про деление на ноль", bp.CondError)
	}

	// Прогон продолжается: точка срабатывает на каждом проходе, обработка
	// доходит до конца, а не падает из-за выражения отладчика.
	for i := 0; i < 4; i++ {
		d.sess.Continue()
		d.waitPause(t)
	}
	d.sess.Continue()
	d.waitFinish(t, done)

	if bp := d.breakpoint(t, debugLoopBodyLine); bp.HitCount != 5 {
		t.Fatalf("срабатываний %d, ожидалось 5", bp.HitCount)
	}
}

// Синтаксически неверное условие обработчик отклоняет сразу, а не откладывает
// сюрприз до первого прохода строки.
func TestBreakpointCondition_SyntaxCheckedOnSet(t *testing.T) {
	d := newDebugSession(t, debugLoopModule)
	for _, condition := range []string{
		"Сч >",
		"Сч = 4; Лишнее",
		"Сч = 4 Лишнее",
		"Сч = 4;;",
	} {
		code, resp := d.setBreakpoint(t, debugLoopBodyLine, condition)
		if code != 400 {
			t.Errorf("условие %q: код %d, ожидалось 400; ответ %v", condition, code, resp)
			continue
		}
		if msg, _ := resp["error"].(string); !strings.Contains(msg, "условие точки останова") {
			t.Errorf("условие %q: невнятная ошибка: %v", condition, resp)
		}
	}
	if bp := d.sess.FindBreakpoint("циклотладки.proc.os", debugLoopBodyLine); bp != nil {
		t.Fatal("точка с неразбираемым условием всё-таки создана")
	}
}

// Точка без условия работает как раньше — останавливает на первом же проходе.
func TestBreakpointCondition_EmptyConditionStopsAlways(t *testing.T) {
	d := newDebugSession(t, debugLoopModule)
	if code, resp := d.setBreakpoint(t, debugLoopBodyLine, ""); code != 200 {
		t.Fatalf("установка точки: код %d, ответ %v", code, resp)
	}

	done := d.run(t, "Итог: 15")
	snap := d.waitPause(t)
	if got, ok := snapshotVar(snap, "Сч"); !ok || got != "1" {
		t.Fatalf("Сч = %q (найдено=%v), ожидалось 1", got, ok)
	}
	for i := 0; i < 4; i++ {
		d.sess.Continue()
		d.waitPause(t)
	}
	d.sess.Continue()
	d.waitFinish(t, done)

	if bp := d.breakpoint(t, debugLoopBodyLine); bp.SkipCount != 0 {
		t.Fatalf("пропуски у безусловной точки: %d", bp.SkipCount)
	}
}
