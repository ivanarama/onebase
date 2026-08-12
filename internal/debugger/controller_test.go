package debugger

import (
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type testArray []any

func (a testArray) Iterate() []any { return []any(a) }

func TestBreakpointLifecycleNormalizesFiles(t *testing.T) {
	dc := NewDebugController()
	s := dc.StartSession(`C:\cfg\заказ.posting.os`)
	require.NotNil(t, s)
	assert.Same(t, s, dc.GetSession(s.ID))

	bp := s.SetBreakpoint(`/tmp/заказ.posting.os`, 12, "Amount > 0")
	assert.Equal(t, "post-Заказ", bp.File)
	assert.Equal(t, 1, bp.MapLen)
	assert.Equal(t, 1, bp.EntryLen)

	// Без вычислителя условие не проверить — точка срабатывает, а не молчит.
	got := s.CheckBreakpoint("post-Заказ", 12, nil)
	require.NotNil(t, got)
	assert.Equal(t, 1, got.HitCount)
	assert.True(t, s.HasBreakpointsForFile("заказ.posting.os"))
	assert.Len(t, s.GetBreakpointsForFile("заказ.posting.os"), 1)

	// Поиск точки счётчик не трогает: клик по колонке — не попадание.
	found := s.FindBreakpoint("заказ.posting.os", 12)
	require.NotNil(t, found)
	assert.Equal(t, 1, found.HitCount)

	toggled := s.ToggleBreakpoint("заказ.posting.os", 12)
	require.NotNil(t, toggled)
	assert.False(t, toggled.Enabled)
	assert.Nil(t, s.CheckBreakpoint("заказ.posting.os", 12, nil))
	assert.NotNil(t, s.FindBreakpoint("заказ.posting.os", 12))

	assert.True(t, s.RemoveBreakpoint("заказ.posting.os", 12))
	assert.False(t, s.RemoveBreakpoint("заказ.posting.os", 12))
	assert.Empty(t, s.GetBreakpoints())

	dc.RemoveSession(s.ID)
	assert.Nil(t, dc.GetSession(s.ID))
	assert.Equal(t, StateStopped, s.State)
}

// Контракт условия на уровне сессии: ложное условие пропускает строку, ошибка
// вычисления останавливает и остаётся видимой, смена условия обнуляет прежние
// показания.
func TestBreakpointConditionBookkeeping(t *testing.T) {
	s := NewDebugController().StartSession("demo.proc.os")
	s.SetBreakpoint("demo.proc.os", 7, "Сч = 4")

	falsy := func(string) (bool, error) { return false, nil }
	assert.Nil(t, s.CheckBreakpoint("demo.proc.os", 7, falsy))
	assert.Nil(t, s.CheckBreakpoint("demo.proc.os", 7, falsy))

	bp := s.FindBreakpoint("demo.proc.os", 7)
	require.NotNil(t, bp)
	assert.Equal(t, 0, bp.HitCount)
	assert.Equal(t, 2, bp.SkipCount)

	truthy := func(string) (bool, error) { return true, nil }
	require.NotNil(t, s.CheckBreakpoint("demo.proc.os", 7, truthy))
	bp = s.FindBreakpoint("demo.proc.os", 7)
	assert.Equal(t, 1, bp.HitCount)
	assert.Empty(t, bp.CondError)

	// Ошибка вычисления — останавливаемся и запоминаем причину.
	broken := func(string) (bool, error) { return false, errors.New("деление на ноль") }
	require.NotNil(t, s.CheckBreakpoint("demo.proc.os", 7, broken))
	bp = s.FindBreakpoint("demo.proc.os", 7)
	assert.Equal(t, 2, bp.HitCount)
	assert.Equal(t, "деление на ноль", bp.CondError)

	// Новое условие — прежние ошибка и счётчик пропусков не про него.
	s.SetBreakpoint("demo.proc.os", 7, "Сч = 5")
	bp = s.FindBreakpoint("demo.proc.os", 7)
	assert.Empty(t, bp.CondError)
	assert.Equal(t, 0, bp.SkipCount)

	// Условие пустое — вычислитель не зовём вовсе.
	s.SetBreakpoint("demo.proc.os", 7, "")
	require.NotNil(t, s.CheckBreakpoint("demo.proc.os", 7, func(string) (bool, error) {
		t.Fatal("вычислитель не должен вызываться для безусловной точки")
		return false, nil
	}))
}

// Public breakpoint accessors return detached snapshots. HTTP handlers inspect
// them after the session mutex is released, while an interpreter may update the
// live counters concurrently; returning the stored pointer would race.
func TestBreakpointAccessorsReturnSnapshots(t *testing.T) {
	s := NewDebugController().StartSession("demo.proc.os")
	created := s.SetBreakpoint("demo.proc.os", 7, "Сч = 1")
	found := s.FindBreakpoint("demo.proc.os", 7)
	all := s.GetBreakpoints()
	forFile := s.GetBreakpointsForFile("demo.proc.os")

	s.SetBreakpoint("demo.proc.os", 7, "Сч = 2")
	require.NotNil(t, s.CheckBreakpoint("demo.proc.os", 7, func(string) (bool, error) {
		return true, nil
	}))

	for name, bp := range map[string]*Breakpoint{
		"created": created,
		"found":   found,
		"all":     all[0],
		"forFile": forFile[0],
	} {
		assert.Equal(t, "Сч = 1", bp.Condition, "%s shares mutable session storage", name)
		assert.Zero(t, bp.HitCount, "%s shares mutable session storage", name)
	}
	live := s.FindBreakpoint("demo.proc.os", 7)
	require.NotNil(t, live)
	assert.Equal(t, "Сч = 2", live.Condition)
	assert.Equal(t, 1, live.HitCount)
}

// Вычислитель работает без мьютекса сессии. Если за это время человек сменил
// условие, результат старого выражения не должен ни остановить уже другой
// breakpoint, ни загрязнить его ошибку/счётчики.
func TestBreakpointConditionDiscardsResultAfterConcurrentEdit(t *testing.T) {
	s := NewDebugController().StartSession("demo.proc.os")
	s.SetBreakpoint("demo.proc.os", 7, "СтароеУсловие()")

	started := make(chan struct{})
	release := make(chan struct{})
	result := make(chan *Breakpoint, 1)
	go func() {
		result <- s.CheckBreakpoint("demo.proc.os", 7, func(string) (bool, error) {
			close(started)
			<-release
			return false, errors.New("ошибка старого условия")
		})
	}()

	select {
	case <-started:
	case <-time.After(2 * time.Second):
		t.Fatal("вычисление старого условия не началось")
	}
	s.SetBreakpoint("demo.proc.os", 7, "НовоеУсловие()")
	close(release)

	select {
	case got := <-result:
		assert.Nil(t, got, "устаревший результат всё-таки остановил исполнение")
	case <-time.After(2 * time.Second):
		t.Fatal("вычисление старого условия не завершилось")
	}

	bp := s.FindBreakpoint("demo.proc.os", 7)
	require.NotNil(t, bp)
	assert.Equal(t, "НовоеУсловие()", bp.Condition)
	assert.Empty(t, bp.CondError)
	assert.Zero(t, bp.HitCount)
	assert.Zero(t, bp.SkipCount)
}

// resumeChan буферизован, чтобы «Продолжить» не терялось в окне между сигналом
// паузы и входом потока интерпретатора в ожидание. Continue/Step кладут сигнал
// только при атомарном переходе Paused -> Running, поэтому команда во время
// исполнения не может снять следующую остановку.
func TestPauseDropsStaleResume(t *testing.T) {
	s := NewDebugController().StartSession("demo.proc.os")

	s.Continue() // никто не остановлен — сигнал не должен попасть в канал
	select {
	case <-s.resumeChan:
		t.Fatal("Continue поставил сигнал для работающей сессии")
	default:
	}
	s.Step(StepInto)
	select {
	case <-s.resumeChan:
		t.Fatal("Step поставил сигнал для работающей сессии")
	default:
	}
	s.mu.Lock()
	assert.Equal(t, StepNone, s.stepMode, "Step во время исполнения не должен вооружать следующий оператор")
	s.mu.Unlock()

	done := make(chan struct{})
	go func() {
		s.Pause(Location{File: "demo.proc.os", Line: 3}, nil, nil, nil, "breakpoint")
		close(done)
	}()

	select {
	case <-s.PauseChan():
	case <-time.After(2 * time.Second):
		t.Fatal("остановка не произошла")
	}
	select {
	case <-done:
		t.Fatal("команда во время исполнения сняла следующую остановку")
	case <-time.After(200 * time.Millisecond):
	}

	s.Continue()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("«Продолжить» не сняло остановку")
	}
}

func TestCallStackSteppingAndSnapshot(t *testing.T) {
	s := NewDebugController().StartSession("demo.proc.os")
	s.PushFrame("Outer", 10)
	s.PushFrame("Inner", 20)
	assert.Equal(t, 2, s.StackDepth())
	assert.Equal(t, []StackFrame{{Procedure: "Outer", Line: 10}, {Procedure: "Inner", Line: 20}}, s.GetCallStack())
	s.PopFrame()
	assert.Equal(t, 1, s.StackDepth())

	s.mu.Lock()
	s.State = StatePaused
	s.currentLoc = &Location{File: "demo.proc.os", Line: 21}
	s.lastDepth = 2
	s.vars = map[string]any{
		"Name":            "short",
		"Ok":              true,
		"Items":           testArray{"a", float64(2)},
		"__debug_session": "hidden",
	}
	s.pauseReason = "breakpoint"
	s.mu.Unlock()

	s.Step(StepOver)
	assert.False(t, s.ShouldStep("other.proc.os", 1))
	assert.False(t, s.ShouldStep("demo.proc.os", 3))
	assert.True(t, s.ShouldStep("demo.proc.os", 2))

	s.mu.Lock()
	s.State = StatePaused // HookOnPause после срабатывания предыдущего шага
	s.mu.Unlock()
	s.Step(StepInto)
	assert.True(t, s.ShouldStep("demo.proc.os", 99))

	s.mu.Lock()
	s.State = StatePaused // следующая остановка перед командой StepOut
	s.mu.Unlock()
	s.Step(StepOut)
	assert.True(t, s.ShouldStep("demo.proc.os", 1))

	snap := s.Snapshot()
	assert.Equal(t, StateRunning, snap.State)
	assert.Equal(t, "breakpoint", snap.PauseReason)
	assert.Len(t, snap.Stack, 1)
	assert.NotEmpty(t, snap.Variables)
	for _, v := range snap.Variables {
		assert.NotEqual(t, "__debug_session", v.Name)
	}
}

func TestFormatValueGetTypeNameAndParseUserValue(t *testing.T) {
	assert.Equal(t, "Неопределено", FormatValue(nil))
	assert.Equal(t, "Истина", FormatValue(true))
	assert.Equal(t, "42", FormatValue(float64(42)))
	assert.Equal(t, "3.14", FormatValue(float64(3.14159)))
	assert.Equal(t, "Массив[2]{0: a, 1: 2}", FormatValue(testArray{"a", float64(2)}))
	assert.Equal(t, "Строка", GetTypeName("x"))
	assert.Equal(t, "Число", GetTypeName(10))
	assert.Equal(t, "Булево", GetTypeName(false))

	n, err := ParseUserValue("10.5", "Число")
	require.NoError(t, err)
	assert.Equal(t, 10.5, n)
	b, err := ParseUserValue("Истина", "Булево")
	require.NoError(t, err)
	assert.Equal(t, true, b)
	s, err := ParseUserValue("abc", "Строка")
	require.NoError(t, err)
	assert.Equal(t, "abc", s)
	_, err = ParseUserValue("maybe", "Булево")
	assert.Error(t, err)
	_, err = ParseUserValue("x", "Дата")
	assert.Error(t, err)
}

func TestGlobalDebugControllerLifecycle(t *testing.T) {
	g := NewGlobalDebugController()
	assert.False(t, g.IsEnabled())
	assert.Nil(t, g.Session())

	first := g.Enable()
	assert.True(t, g.IsEnabled())
	assert.Same(t, first, g.Session())

	second := g.Enable()
	assert.Equal(t, StateStopped, first.State)
	assert.Same(t, second, g.Session())

	g.SetSession(nil)
	assert.False(t, g.IsEnabled())
	assert.Nil(t, g.Session())
	assert.Equal(t, StateStopped, second.State)

	third := NewDebugController().StartSession("module.os")
	g.SetSession(third)
	assert.True(t, g.IsEnabled())
	assert.Same(t, third, g.Session())
	g.Disable()
	assert.False(t, g.IsEnabled())
	assert.Nil(t, g.Session())
	assert.Equal(t, StateStopped, third.State)
}
