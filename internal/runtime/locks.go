package runtime

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"sync"
)

// LockManager — глобальный менеджер блокировок уровня процесса.
//
// Блокировки гранулярные по (регистр, набор измерений): ключ имеет вид
// "регистр|изм1=знач&изм2=знач...". Два параллельных проведения с
// пересекающимся набором (одна и та же Номенклатура+Склад) сериализуются.
//
// Ограничения:
//   - Сам LockManager работает только в пределах одного процесса. При запуске
//     через entityservice.Save собранные ключи дополнительно берутся как
//     PostgreSQL advisory transaction locks на storage-слое.
//   - Гранулярность ровно та, что задал DSL.
//   - Освобождение происходит явно через Разблокировать или автоматически
//     через LockCollector: в конце Save, а вне записи объекта — в конце
//     исполнения DSL (сборщик исполнения, ContextWithExecutionLockCollector).
type LockManager struct {
	mu    sync.Mutex
	locks map[string]*lockEntry
}

// lockEntry хранит мьютекс и счётчик горутин, удерживающих или ожидающих
// блокировку. Когда refs падает до 0, запись удаляется из карты.
type lockEntry struct {
	mu   sync.Mutex
	refs int
}

func NewLockManager() *LockManager {
	return &LockManager{locks: map[string]*lockEntry{}}
}

type lockCollectorKey struct{}

// LockCollector tracks DSL data-lock requests made during one hook run. The
// service layer uses the collected keys to take DB-scoped locks inside the
// later storage transaction, while still releasing any process-local locks that
// the DSL code forgot to unlock explicitly.
//
// Внутри одной области (сборщика) ключ берётся в менеджере один раз: повторный
// Заблокировать того же ключа — другим объектом той же транзакции или
// вложенной записью — только увеличивает счётчик. Раньше он ждал мьютекс,
// который держит сама же операция, и процесс висел.
type LockCollector struct {
	mu      sync.Mutex
	keys    map[string]struct{}
	objects []*LockObject
	// parent — внешняя область той же операции: запись внутри обработки,
	// проведение внутри хука. Её ключи вложенная область не берёт заново.
	parent *LockCollector
	// holds — сколько объектов этой области держат ключ; owned — ключ взят в
	// менеджере именно этой областью (а не унаследован от внешней).
	holds map[string]int
	owned map[string]bool
}

func NewLockCollector() *LockCollector {
	return &LockCollector{keys: map[string]struct{}{}, holds: map[string]int{}, owned: map[string]bool{}}
}

// NewLockCollectorIn — сборщик области записи внутри операции из ctx: ключи,
// которые уже держит внешняя область (сборщик хука или сборщик исполнения),
// не берутся заново — иначе операция ждала бы сама себя.
func NewLockCollectorIn(ctx context.Context) *LockCollector {
	c := NewLockCollector()
	c.parent = LockCollectorFromContext(ctx)
	if c.parent == nil {
		c.parent = ExecutionLockCollectorFromContext(ctx)
	}
	return c
}

type executionLockCollectorKey struct{}

// ContextWithExecutionLockCollector кладёт в ctx сборщик исполнения DSL
// (обработка, событие формы, регламентное задание, тест конфигурации).
// Блокировки, взятые таким кодом вне записи документа, отпускаются в конце
// исполнения, а не живут до конца процесса. Ключ отдельный от обычного
// сборщика: запись документа внутри исполнения по-прежнему заводит свою
// область и отпускает её блокировки в конце записи.
func ContextWithExecutionLockCollector(ctx context.Context, c *LockCollector) context.Context {
	if c == nil {
		return ctx
	}
	return context.WithValue(ctx, executionLockCollectorKey{}, c)
}

func ExecutionLockCollectorFromContext(ctx context.Context) *LockCollector {
	if c, ok := ctx.Value(executionLockCollectorKey{}).(*LockCollector); ok {
		return c
	}
	return nil
}

// acquire берёт в менеджере ключи, которых ещё не держат эта область и
// внешние области той же операции; остальные только учитывает.
func (c *LockCollector) acquire(mgr *LockManager, keys []string) {
	need := make([]string, 0, len(keys))
	c.mu.Lock()
	for _, k := range keys {
		if c.holds[k] > 0 || c.heldAboveLocked(k) {
			c.holds[k]++
			continue
		}
		need = append(need, k)
	}
	c.mu.Unlock()
	if len(need) > 0 {
		mgr.Acquire(need)
	}
	c.mu.Lock()
	for _, k := range need {
		c.holds[k]++
		c.owned[k] = true
	}
	c.mu.Unlock()
}

// release уменьшает счётчики и отпускает в менеджере ключи, которые эта
// область взяла сама и больше не держит.
func (c *LockCollector) release(mgr *LockManager, keys []string) {
	var free []string
	c.mu.Lock()
	for _, k := range keys {
		if c.holds[k] == 0 {
			continue
		}
		c.holds[k]--
		if c.holds[k] > 0 {
			continue
		}
		delete(c.holds, k)
		if c.owned[k] {
			delete(c.owned, k)
			free = append(free, k)
		}
	}
	c.mu.Unlock()
	if len(free) > 0 {
		mgr.Release(free)
	}
}

// heldAboveLocked — держит ли ключ внешняя область. Вызывается под c.mu;
// порядок блокировок всегда «вложенная → внешняя», поэтому взаимных ожиданий
// между областями нет.
func (c *LockCollector) heldAboveLocked(k string) bool {
	for p := c.parent; p != nil; p = p.parent {
		p.mu.Lock()
		held := p.holds[k] > 0
		p.mu.Unlock()
		if held {
			return true
		}
	}
	return false
}

func ContextWithLockCollector(ctx context.Context, c *LockCollector) context.Context {
	if c == nil {
		return ctx
	}
	return context.WithValue(ctx, lockCollectorKey{}, c)
}

func LockCollectorFromContext(ctx context.Context) *LockCollector {
	if c, ok := ctx.Value(lockCollectorKey{}).(*LockCollector); ok {
		return c
	}
	return nil
}

func (c *LockCollector) Track(obj *LockObject) {
	if c == nil || obj == nil {
		return
	}
	c.mu.Lock()
	c.objects = append(c.objects, obj)
	c.mu.Unlock()
}

func (c *LockCollector) Add(keys []string) {
	if c == nil {
		return
	}
	c.mu.Lock()
	for _, k := range normalizeLockKeys(keys) {
		c.keys[k] = struct{}{}
	}
	c.mu.Unlock()
}

func (c *LockCollector) Keys() []string {
	if c == nil {
		return nil
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	keys := make([]string, 0, len(c.keys))
	for k := range c.keys {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

func (c *LockCollector) ReleaseAll() {
	if c == nil {
		return
	}
	c.mu.Lock()
	objects := append([]*LockObject{}, c.objects...)
	c.objects = nil
	c.mu.Unlock()
	for _, obj := range objects {
		obj.ReleaseAll()
	}
}

func normalizeLockKeys(keys []string) []string {
	seen := make(map[string]struct{}, len(keys))
	out := make([]string, 0, len(keys))
	for _, k := range keys {
		k = strings.TrimSpace(k)
		if k == "" {
			continue
		}
		if _, ok := seen[k]; ok {
			continue
		}
		seen[k] = struct{}{}
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// Acquire берёт мьютексы по всем ключам в детерминированном порядке
// (отсортированы), чтобы избежать кросс-deadlock'а между двумя
// проведениями, запросившими разный набор в разном порядке.
func (lm *LockManager) Acquire(keys []string) {
	for _, k := range normalizeLockKeys(keys) {
		lm.mu.Lock()
		e, ok := lm.locks[k]
		if !ok {
			e = &lockEntry{}
			lm.locks[k] = e
		}
		e.refs++
		lm.mu.Unlock()
		e.mu.Lock()
	}
}

// Release отпускает мьютексы в обратном порядке и удаляет записи с refs==0.
func (lm *LockManager) Release(keys []string) {
	sorted := normalizeLockKeys(keys)
	for i := len(sorted) - 1; i >= 0; i-- {
		k := sorted[i]
		lm.mu.Lock()
		e := lm.locks[k]
		lm.mu.Unlock()
		if e == nil {
			continue
		}
		e.mu.Unlock()
		lm.mu.Lock()
		e.refs--
		if e.refs == 0 {
			delete(lm.locks, k)
		}
		lm.mu.Unlock()
	}
}

// LockObject — DSL-обёртка над LockManager (этап «БлокировкаДанных»).
// Аккумулирует «элементы» (Добавить), для каждого собирает значения
// измерений (УстановитьЗначение), при Заблокировать — берёт мьютексы.
//
// Реализует interpreter.MethodCallable + interpreter.This.
type LockObject struct {
	mgr       *LockManager
	collector *LockCollector
	advisory  func(keys []string)
	elements  []*LockElement
	held      []string // ключи которые удерживаем
}

// NewLockObject — фабрика для DSL builtin БлокировкаДанных().
func NewLockObject(mgr *LockManager) *LockObject {
	return &LockObject{mgr: mgr}
}

func NewLockObjectWithCollector(mgr *LockManager, collector *LockCollector) *LockObject {
	obj := &LockObject{mgr: mgr, collector: collector}
	if collector != nil {
		collector.Track(obj)
	}
	return obj
}

// WithAdvisory задаёт функцию, которую Заблокировать() вызывает сразу после
// взятия внутрипроцессных мьютексов. Через неё storage-слой берёт
// pg_advisory_xact_lock ещё ДО чтения остатков DSL-кодом — иначе между
// «прочитал остатки» и «взял блокировку после хука» остаётся окно гонки
// (двойное списание партий, issue #458). Функция вправе паниковать
// RaiseUserError — Заблокировать освободит уже взятые внутрипроцессные
// мьютексы перед пробросом паники.
func (lo *LockObject) WithAdvisory(fn func(keys []string)) *LockObject {
	lo.advisory = fn
	return lo
}

func (lo *LockObject) Get(name string) any    { return nil }
func (lo *LockObject) Set(name string, v any) {}

// CallMethod implements interpreter.MethodCallable.
func (lo *LockObject) CallMethod(method string, args []any) any {
	switch strings.ToLower(method) {
	case "добавить", "add":
		if len(args) == 0 {
			return nil
		}
		reg, _ := args[0].(string)
		el := &LockElement{registerName: reg, values: map[string]any{}}
		lo.elements = append(lo.elements, el)
		return el
	case "заблокировать", "lock":
		if lo.mgr == nil {
			return nil
		}
		lo.ReleaseAll()
		keys := normalizeLockKeys(lo.buildKeys())
		if lo.collector != nil {
			lo.collector.acquire(lo.mgr, keys)
			lo.collector.Add(keys)
		} else {
			lo.mgr.Acquire(keys)
		}
		lo.held = keys
		if lo.advisory != nil {
			func() {
				defer func() {
					if r := recover(); r != nil {
						lo.ReleaseAll()
						panic(r)
					}
				}()
				lo.advisory(lo.held)
			}()
		}
		return nil
	case "разблокировать", "unlock":
		lo.ReleaseAll()
		return nil
	}
	return nil
}

// ReleaseAll отпускает удерживаемые мьютексы. Безопасно вызывать
// несколько раз. Используется как defer в handlers.go на случай если
// DSL забыл .Разблокировать().
func (lo *LockObject) ReleaseAll() {
	if lo.mgr != nil && len(lo.held) > 0 {
		if lo.collector != nil {
			lo.collector.release(lo.mgr, lo.held)
		} else {
			lo.mgr.Release(lo.held)
		}
		lo.held = nil
	}
}

// buildKeys формирует ключи блокировок в виде "регистр|изм1=знач&изм2=знач..."
// Значения отсортированы по имени измерения для детерминированности.
// Ссылочные значения нормализуются к UUID: у *interpreter.Ref метод String()
// возвращает отображаемое имя, которое в разных путях проведения бывает то
// заполненным, то пустым — ключи расходились бы и блокировка не пересекалась.
func (lo *LockObject) buildKeys() []string {
	keys := make([]string, 0, len(lo.elements))
	for _, el := range lo.elements {
		var pairs []string
		for k, v := range el.values {
			if r, ok := v.(interface{ GetRefUUID() string }); ok {
				if id := r.GetRefUUID(); id != "" {
					v = id
				}
			}
			pairs = append(pairs, fmt.Sprintf("%s=%v", k, v))
		}
		sort.Strings(pairs)
		keys = append(keys, el.registerName+"|"+strings.Join(pairs, "&"))
	}
	return keys
}

// LockElement — отдельный элемент блокировки (соответствует
// БлокировкаДанных.Добавить()).
type LockElement struct {
	registerName string
	values       map[string]any
}

func (le *LockElement) Get(name string) any    { return nil }
func (le *LockElement) Set(name string, v any) {}

// CallMethod implements interpreter.MethodCallable.
func (le *LockElement) CallMethod(method string, args []any) any {
	switch strings.ToLower(method) {
	case "установитьзначение", "setvalue":
		if len(args) >= 2 {
			name, ok := args[0].(string)
			if !ok {
				return nil
			}
			le.values[strings.ToLower(name)] = args[1]
		}
	}
	return nil
}
