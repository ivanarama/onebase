package debugger

import (
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/google/uuid"
)

// DebugController manages all debug sessions. Thread-safe.
type DebugController struct {
	mu       sync.Mutex
	sessions map[string]*ActiveSession
}

// NewDebugController creates a new controller
func NewDebugController() *DebugController {
	return &DebugController{
		sessions: make(map[string]*ActiveSession),
	}
}

// ActiveSession is a live debug session with channel-based pause/resume.
// The interpreter goroutine blocks inside Pause(); the HTTP handler unblocks
// it via Continue() or Step().
type ActiveSession struct {
	mu         sync.Mutex
	ID         string
	ModulePath string
	State      DebugState

	// Breakpoints indexed by file -> line -> bp
	breakpoints map[string]map[int]*Breakpoint

	// Snapshot captured at pause time
	currentLoc  *Location
	callStack   []StackFrame
	vars        map[string]any
	pauseReason string // "breakpoint" or "step"

	// Diagnostics — last check info
	diagLastFile string
	diagLastLine int
	diagMessages []string

	// Stepping
	stepMode  StepMode
	stepDepth int
	stepFile  string // normalized file the step was initiated from; stepping stays within it
	lastDepth int    // interpreter's actual stack depth from last HookShouldStep call

	// evaluating — сессия вычисляет выражение отладчика (условие точки останова
	// или выражение табло/консоли) на потоке интерпретатора. Пока флаг стоит,
	// точки останова и шаги игнорируются: вычисление само исполняет DSL и
	// иначе вошло бы в beforeStmt → CheckBreakpoint по кругу (условие
	// `РассчитатьСумму() > 100` на своей же строке — бесконечная рекурсия,
	// а на уже захваченном мьютексе — взаимная блокировка).
	evaluating bool

	// Channels: interpreter goroutine uses pauseChan/resumeChan,
	// HTTP handlers signal via methods.
	pauseChan  chan struct{} // signaled when interpreter pauses
	resumeChan chan struct{} // signaled by HTTP handler to resume
	doneChan   chan struct{} // closed when session ends
	stopOnce   sync.Once     // ensures doneChan is closed only once

	// Expression evaluation during pause
	evalReq chan evalRequest
}

type evalRequest struct {
	expr   string
	result chan EvaluateResult
}

// StartSession creates and registers a new debug session
func (dc *DebugController) StartSession(modulePath string) *ActiveSession {
	dc.mu.Lock()
	defer dc.mu.Unlock()

	id := uuid.New().String()[:8]
	s := &ActiveSession{
		ID:          id,
		ModulePath:  modulePath,
		State:       StateRunning,
		breakpoints: make(map[string]map[int]*Breakpoint),
		vars:        make(map[string]any),
		pauseChan:   make(chan struct{}, 1),
		resumeChan:  make(chan struct{}),
		doneChan:    make(chan struct{}),
		evalReq:     make(chan evalRequest),
	}
	dc.sessions[id] = s
	return s
}

// GetSession returns a session by ID
func (dc *DebugController) GetSession(id string) *ActiveSession {
	dc.mu.Lock()
	defer dc.mu.Unlock()
	return dc.sessions[id]
}

// RemoveSession removes and stops a session
func (dc *DebugController) RemoveSession(id string) {
	dc.mu.Lock()
	s := dc.sessions[id]
	if s != nil {
		delete(dc.sessions, id)
	}
	dc.mu.Unlock()

	if s != nil {
		s.Stop()
	}
}

// ── Breakpoint management ────────────────────────────────────────

// SetBreakpoint creates or updates a breakpoint. The file key is normalized
// so all operations (set/remove/check) hit the same map entry regardless of
// whether the caller passes "X.posting.os" or the editor id "post-X".
func (s *ActiveSession) SetBreakpoint(file string, line int, condition string) *Breakpoint {
	s.mu.Lock()
	defer s.mu.Unlock()

	key := normalizeFilePath(file)
	if s.breakpoints[key] == nil {
		s.breakpoints[key] = make(map[int]*Breakpoint)
	}
	if bp, ok := s.breakpoints[key][line]; ok {
		if bp.Condition != condition {
			// Условие переписали — прежние «ошибка условия» и счётчик пропусков
			// относятся к старому выражению и ввели бы в заблуждение.
			bp.CondError = ""
			bp.SkipCount = 0
		}
		bp.Condition = condition
		bp.Enabled = true
		return bp
	}
	bp := &Breakpoint{
		ID:        fmt.Sprintf("bp-%d-%s", line, s.ID),
		File:      key,
		Line:      line,
		Enabled:   true,
		Condition: condition,
		CreatedAt: time.Now(),
	}
	s.breakpoints[key][line] = bp
	bp.MapLen = len(s.breakpoints)
	bp.EntryLen = len(s.breakpoints[key])
	return bp
}

// RemoveBreakpoint deletes a breakpoint by normalized file key.
func (s *ActiveSession) RemoveBreakpoint(file string, line int) bool {
	s.mu.Lock()
	defer s.mu.Unlock()

	key := normalizeFilePath(file)
	if locMap, ok := s.breakpoints[key]; ok {
		if _, ok := locMap[line]; ok {
			delete(locMap, line)
			if len(locMap) == 0 {
				delete(s.breakpoints, key)
			}
			return true
		}
	}
	return false
}

// ToggleBreakpoint enables or disables a breakpoint at file:line.
func (s *ActiveSession) ToggleBreakpoint(file string, line int) *Breakpoint {
	s.mu.Lock()
	defer s.mu.Unlock()

	key := normalizeFilePath(file)
	if locMap, ok := s.breakpoints[key]; ok {
		if bp, ok := locMap[line]; ok {
			bp.Enabled = !bp.Enabled
			return bp
		}
	}
	return nil
}

// FindBreakpoint returns the breakpoint at file:line, enabled or not, without
// touching counters. Для запросов «есть ли здесь точка» (переключение из UI):
// раньше для этого звали CheckBreakpoint, и клик по колонке накручивал счётчик
// попаданий, которого не было.
func (s *ActiveSession) FindBreakpoint(file string, line int) *Breakpoint {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.lookupBreakpoint(normalizeFilePath(file), line)
}

// lookupBreakpoint ищет точку по уже нормализованному ключу. Вызывается под s.mu.
func (s *ActiveSession) lookupBreakpoint(key string, line int) *Breakpoint {
	locMap, ok := s.breakpoints[key]
	if !ok {
		// Case-insensitive fallback in case some legacy ID slipped in.
		for k, m := range s.breakpoints {
			if strings.EqualFold(k, key) {
				locMap = m
				break
			}
		}
	}
	if locMap == nil {
		return nil
	}
	return locMap[line]
}

// CheckBreakpoint returns the breakpoint if execution must stop at file:line.
// Keys in the breakpoints map are already normalized (see SetBreakpoint), so a
// direct map lookup is enough. Exact line match only — no fuzzy ±1, which used
// to cause stops on the line above the breakpoint.
//
// Условие (bp.Condition) вычисляется через cond — колбэк интерпретатора,
// который считает выражение в окружении текущего оператора. Правила:
//
//   - условие пустое или вычислителя нет → останавливаемся, как раньше;
//   - условие ложно → не останавливаемся, растёт SkipCount;
//   - условие не вычислилось (опечатка, неизвестное имя) → останавливаемся и
//     показываем ошибку в CondError. Молча не останавливаться на сломанном
//     условии — худший из вариантов: точка стоит, отладчик её игнорирует, и
//     человек ищет ошибку в своём коде, а не в условии.
func (s *ActiveSession) CheckBreakpoint(file string, line int, cond func(expr string) (bool, error)) *Breakpoint {
	key := normalizeFilePath(file)

	s.mu.Lock()
	s.diagLastFile = file
	s.diagLastLine = line
	s.appendDiag(fmt.Sprintf("check raw=%q line=%d norm=%q", file, line, key))
	if s.evaluating {
		// Внутри вычисления выражения отладчика точки не работают.
		s.mu.Unlock()
		return nil
	}
	bp := s.lookupBreakpoint(key, line)
	if bp == nil || !bp.Enabled {
		s.mu.Unlock()
		return nil
	}
	expr := bp.Condition
	if expr == "" || cond == nil {
		bp.CondError = ""
		bp.HitCount++
		s.mu.Unlock()
		return bp
	}
	s.evaluating = true
	s.mu.Unlock()

	// Вычисляем вне мьютекса: колбэк исполняет DSL и может вернуться в сессию.
	ok, err := cond(expr)

	s.mu.Lock()
	defer s.mu.Unlock()
	s.evaluating = false
	switch {
	case err != nil:
		bp.CondError = err.Error()
		bp.HitCount++
		s.appendDiag(fmt.Sprintf("cond %q line=%d error=%v (остановка)", expr, line, err))
		return bp
	case !ok:
		bp.CondError = ""
		bp.SkipCount++
		s.appendDiag(fmt.Sprintf("cond %q line=%d = Ложь (пропуск %d)", expr, line, bp.SkipCount))
		return nil
	default:
		bp.CondError = ""
		bp.HitCount++
		s.appendDiag(fmt.Sprintf("cond %q line=%d = Истина (остановка)", expr, line))
		return bp
	}
}

// appendDiag добавляет строку в кольцевой буфер диагностики. Вызывается под s.mu.
func (s *ActiveSession) appendDiag(msg string) {
	s.diagMessages = append(s.diagMessages, msg)
	if len(s.diagMessages) > 50 {
		s.diagMessages = s.diagMessages[len(s.diagMessages)-50:]
	}
}

// normalizeFilePath converts a file path or editor ID to a canonical form
// for case-insensitive breakpoint matching.
// Preserves original casing of the entity name so UI can match editor IDs.
func normalizeFilePath(file string) string {
	base := filepath.Base(file)
	baseLow := strings.ToLower(base)

	if strings.HasSuffix(baseLow, ".posting.os") {
		name := base[:len(base)-len(".posting.os")]
		return "post-" + capitalizeFirst(name)
	}
	if strings.HasSuffix(baseLow, ".module.os") {
		name := base[:len(base)-len(".module.os")]
		return "mod-" + capitalizeFirst(name)
	}
	if strings.HasSuffix(baseLow, ".proc.os") {
		name := base[:len(base)-len(".proc.os")]
		return "proc-" + capitalizeFirst(name)
	}
	if strings.HasSuffix(baseLow, ".os") {
		name := base[:len(base)-len(".os")]
		return capitalizeFirst(name)
	}
	// Already an editor ID like "post-ПоступлениеТоваров" — keep as-is
	return file
}

func capitalizeFirst(s string) string {
	if s == "" {
		return s
	}
	r, size := utf8.DecodeRuneInString(s)
	return string(unicode.ToUpper(r)) + s[size:]
}

// GetBreakpoints returns all breakpoints
func (s *ActiveSession) GetBreakpoints() []*Breakpoint {
	s.mu.Lock()
	defer s.mu.Unlock()

	var result []*Breakpoint
	for _, locMap := range s.breakpoints {
		for _, bp := range locMap {
			result = append(result, bp)
		}
	}
	return result
}

// GetBreakpointsForFile returns breakpoints for a file (normalized lookup).
func (s *ActiveSession) GetBreakpointsForFile(file string) []*Breakpoint {
	s.mu.Lock()
	defer s.mu.Unlock()

	locMap := s.breakpoints[normalizeFilePath(file)]
	result := make([]*Breakpoint, 0, len(locMap))
	for _, bp := range locMap {
		result = append(result, bp)
	}
	return result
}

// HasBreakpointsForFile checks if there are any breakpoints for a file.
func (s *ActiveSession) HasBreakpointsForFile(file string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.breakpoints[normalizeFilePath(file)]) > 0
}

// ── Call stack ───────────────────────────────────────────────────

// PushFrame pushes a call stack frame (implements DebugHook)
func (s *ActiveSession) PushFrame(procedure string, line int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.callStack = append(s.callStack, StackFrame{Procedure: procedure, Line: line})
}

// PopFrame pops a call stack frame
func (s *ActiveSession) PopFrame() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.callStack) > 0 {
		s.callStack = s.callStack[:len(s.callStack)-1]
	}
}

// GetCallStack returns the current call stack
func (s *ActiveSession) GetCallStack() []StackFrame {
	s.mu.Lock()
	defer s.mu.Unlock()
	result := make([]StackFrame, len(s.callStack))
	copy(result, s.callStack)
	return result
}

// StackDepth returns current call stack depth
func (s *ActiveSession) StackDepth() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.callStack)
}

// ── Pause / Resume / Step ────────────────────────────────────────

// Pause is called by the interpreter goroutine when it hits a breakpoint or step.
// It blocks until Continue/Step/Stop is called from an HTTP handler.
// The evalFn callback is called for expression evaluation requests during pause.
func (s *ActiveSession) Pause(loc Location, vars map[string]any, stack []StackFrame, evalFn func(string) (any, error), reason string) {
	s.mu.Lock()
	s.State = StatePaused
	s.currentLoc = &loc
	s.vars = vars
	s.callStack = stack
	s.pauseReason = reason
	s.mu.Unlock()

	// Signal that we're paused (non-blocking write to buffered channel)
	select {
	case s.pauseChan <- struct{}{}:
	default:
	}

	// Block until resume/stop or evaluate request
	for {
		select {
		case <-s.resumeChan:
			return
		case <-s.doneChan:
			return
		case req := <-s.evalReq:
			// Тот же предохранитель, что и у условий: выражение табло может
			// вызвать процедуру, внутри которой стоит точка останова, — вложенная
			// остановка на уже остановленном потоке не выйдет из Pause никогда.
			s.mu.Lock()
			s.evaluating = true
			s.mu.Unlock()
			val, err := evalFn(req.expr)
			s.mu.Lock()
			s.evaluating = false
			s.mu.Unlock()
			if err != nil {
				req.result <- EvaluateResult{IsError: true, Error: err.Error()}
			} else {
				req.result <- EvaluateResult{Value: val, Type: GetTypeName(val)}
			}
		}
	}
}

// PauseChan returns the channel that is signaled when the interpreter pauses.
// Used by HTTP handler to wait for pause events.
func (s *ActiveSession) PauseChan() <-chan struct{} {
	return s.pauseChan
}

// Continue unblocks the interpreter goroutine (called from HTTP handler)
func (s *ActiveSession) Continue() {
	s.mu.Lock()
	s.State = StateRunning
	s.stepMode = StepNone
	s.stepFile = ""
	ch := s.resumeChan
	s.mu.Unlock()

	select {
	case ch <- struct{}{}:
	default:
	}
}

// Step sets stepping mode and resumes
func (s *ActiveSession) Step(mode StepMode) {
	s.mu.Lock()
	s.State = StateRunning
	s.stepMode = mode
	s.stepDepth = s.lastDepth // use interpreter's actual depth from last pause
	// Restrict stepping to the module we paused in, so background work
	// (scheduled jobs, posting handlers in other modules) can't hijack the cursor.
	if s.currentLoc != nil {
		s.stepFile = normalizeFilePath(s.currentLoc.File)
	} else {
		s.stepFile = ""
	}
	ch := s.resumeChan
	s.mu.Unlock()

	select {
	case ch <- struct{}{}:
	default:
	}
}

// Stop terminates the session. Safe to call multiple times.
func (s *ActiveSession) Stop() {
	s.mu.Lock()
	s.State = StateStopped
	s.mu.Unlock()

	s.stopOnce.Do(func() { close(s.doneChan) })
}

// ShouldStep checks if execution should pause for stepping at the current position.
// currentDepth is the interpreter's actual call stack depth (from env parent chain).
// file is the source file of the statement about to execute; stepping is confined to
// the module the user paused in (s.stepFile) so unrelated code running on the same
// global debug session (scheduled jobs, posting in other modules) is ignored.
func (s *ActiveSession) ShouldStep(file string, currentDepth int) bool {
	normFile := normalizeFilePath(file)

	s.mu.Lock()
	if s.evaluating {
		// Шаги внутри вычисления выражения отладчика не считаем: иначе шаг
		// уводил бы курсор в код, который выполняет условие точки останова,
		// а lastDepth ушёл бы в глубину этого вычисления.
		s.mu.Unlock()
		return false
	}
	mode := s.stepMode
	sd := s.stepDepth
	sf := s.stepFile
	inScope := sf == "" || strings.EqualFold(normFile, sf)
	if inScope {
		// Only track depth for code inside our step scope, otherwise a
		// background goroutine's depth would corrupt the next Step().
		s.lastDepth = currentDepth
	}
	s.mu.Unlock()

	if mode == StepNone || !inScope {
		return false
	}
	var result bool
	switch mode {
	case StepOver:
		result = currentDepth <= sd
	case StepInto:
		result = true
	case StepOut:
		result = currentDepth < sd
	}
	s.mu.Lock()
	s.diagMessages = append(s.diagMessages, fmt.Sprintf("step mode=%d file=%s depth=%d stepDepth=%d result=%v", mode, normFile, currentDepth, sd, result))
	if len(s.diagMessages) > 50 {
		s.diagMessages = s.diagMessages[len(s.diagMessages)-50:]
	}
	s.mu.Unlock()
	return result
}

// ── State queries (for HTTP API) ─────────────────────────────────

// Snapshot returns the current debug state for the status polling API
func (s *ActiveSession) Snapshot() StatusSnapshot {
	s.mu.Lock()
	defer s.mu.Unlock()

	snap := StatusSnapshot{
		State:        s.State,
		Location:     s.currentLoc,
		Stack:        make([]StackFrame, len(s.callStack)),
		Breakpoints:  make([]Breakpoint, 0),
		PauseReason:  s.pauseReason,
		DiagLastFile: s.diagLastFile,
		DiagLastLine: s.diagLastLine,
		DiagMessages: s.diagMessages,
	}
	// Collect breakpoint keys for diagnostics
	for key, locMap := range s.breakpoints {
		snap.DiagBPKeys = append(snap.DiagBPKeys, key)
		snap.DiagBPCount += len(locMap)
	}
	copy(snap.Stack, s.callStack)

	for _, v := range s.vars {
		_ = v // skip internal vars
	}
	for name, val := range s.vars {
		if name == "__debug_session" {
			continue
		}
		snap.Variables = append(snap.Variables, VarEntry{
			Name:  name,
			Value: FormatValue(val),
			Type:  GetTypeName(val),
		})
	}

	for _, locMap := range s.breakpoints {
		for _, bp := range locMap {
			snap.Breakpoints = append(snap.Breakpoints, *bp)
		}
	}

	return snap
}

// Evaluate sends an expression to the paused interpreter and waits for the result.
// Called from HTTP handler. Times out after 5 seconds.
func (s *ActiveSession) Evaluate(expr string, evalFn func(string) (any, error)) EvaluateResult {
	req := evalRequest{
		expr:   expr,
		result: make(chan EvaluateResult, 1),
	}

	select {
	case s.evalReq <- req:
	case <-time.After(5 * time.Second):
		return EvaluateResult{IsError: true, Error: "evaluation timed out (not paused?)"}
	}

	select {
	case r := <-req.result:
		return r
	case <-time.After(5 * time.Second):
		return EvaluateResult{IsError: true, Error: "evaluation timed out"}
	}
}

// ── DebugHook interface implementation ───────────────────────────
// These methods satisfy interpreter.DebugHook interface.
// Named HookXxx to avoid collision with ActiveSession's own methods.

func (s *ActiveSession) HookCheckBreakpoint(file string, line int, cond func(expr string) (bool, error)) bool {
	return s.CheckBreakpoint(file, line, cond) != nil
}

func (s *ActiveSession) HookShouldStep(file string, depth int) bool {
	return s.ShouldStep(file, depth)
}

func (s *ActiveSession) HookOnPause(file string, line int, vars map[string]any, evalFn func(string) (any, error), reason string) {
	loc := Location{File: normalizeFilePath(file), Line: line}
	stack := s.GetCallStack()
	s.Pause(loc, vars, stack, evalFn, reason)
}

func (s *ActiveSession) HookPushFrame(procedure string, line int) {
	s.PushFrame(procedure, line)
}

func (s *ActiveSession) HookPopFrame() {
	s.PopFrame()
}

// ── GlobalDebugController ─────────────────────────────────────────

// GlobalDebugController manages a single global debug session used for
// debugging DSL modules across the entire application.
type GlobalDebugController struct {
	mu      sync.Mutex
	enabled bool
	session *ActiveSession
}

// NewGlobalDebugController creates a new global debug controller (disabled by default).
func NewGlobalDebugController() *GlobalDebugController {
	return &GlobalDebugController{}
}

// Enable creates a new ActiveSession with ID "global" and ModulePath "*",
// then marks the controller as enabled. If there was an existing session
// it is stopped first.
func (g *GlobalDebugController) Enable() *ActiveSession {
	g.mu.Lock()
	defer g.mu.Unlock()

	if g.session != nil {
		g.session.Stop()
	}

	s := &ActiveSession{
		ID:          "global",
		ModulePath:  "*",
		State:       StateRunning,
		breakpoints: make(map[string]map[int]*Breakpoint),
		vars:        make(map[string]any),
		pauseChan:   make(chan struct{}, 1),
		resumeChan:  make(chan struct{}),
		doneChan:    make(chan struct{}),
		evalReq:     make(chan evalRequest),
	}
	g.session = s
	g.enabled = true
	return s
}

// Disable stops the current session (if any), clears it, and marks the controller as disabled.
func (g *GlobalDebugController) Disable() {
	g.mu.Lock()
	defer g.mu.Unlock()

	if g.session != nil {
		g.session.Stop()
		g.session = nil
	}
	g.enabled = false
}

// Session returns the current active session, or nil if disabled.
func (g *GlobalDebugController) Session() *ActiveSession {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.session
}

// IsEnabled returns whether the global debug controller is enabled.
func (g *GlobalDebugController) IsEnabled() bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.enabled
}

// SetSession sets the session directly (used when wiring up from ui.Server).
func (g *GlobalDebugController) SetSession(sess *ActiveSession) {
	g.mu.Lock()
	defer g.mu.Unlock()

	if g.session != nil {
		g.session.Stop()
	}
	g.session = sess
	g.enabled = sess != nil
}
