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
//     через LockCollector в конце Save.
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
type LockCollector struct {
	mu      sync.Mutex
	keys    map[string]struct{}
	objects []*LockObject
}

func NewLockCollector() *LockCollector {
	return &LockCollector{keys: map[string]struct{}{}}
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
		keys := lo.buildKeys()
		lo.mgr.Acquire(keys)
		lo.held = normalizeLockKeys(keys)
		if lo.collector != nil {
			lo.collector.Add(lo.held)
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
		lo.mgr.Release(lo.held)
		lo.held = nil
	}
}

// buildKeys формирует ключи блокировок в виде "регистр|изм1=знач&изм2=знач..."
// Значения отсортированы по имени измерения для детерминированности.
func (lo *LockObject) buildKeys() []string {
	keys := make([]string, 0, len(lo.elements))
	for _, el := range lo.elements {
		var pairs []string
		for k, v := range el.values {
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
