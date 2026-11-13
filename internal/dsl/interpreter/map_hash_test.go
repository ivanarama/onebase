package interpreter

import (
	"fmt"
	"math/rand"
	"testing"
	"time"

	"github.com/shopspring/decimal"
)

// refMap — прежняя реализация Соответствия (два среза и полный перебор через
// equal с запасным сопоставлением ссылки и строки по наименованию). Эталон:
// индекс обязан находить ровно те же ключи в том же порядке.
type refMap struct{ keys, vals []any }

func (m *refMap) find(key any) int {
	for i, k := range m.keys {
		if equal(k, key) {
			return i
		}
	}
	if ref, ok := key.(*Ref); ok {
		for i, k := range m.keys {
			if s, ok2 := k.(string); ok2 && s == ref.Name {
				return i
			}
		}
	}
	if s, ok := key.(string); ok {
		for i, k := range m.keys {
			if ref, ok2 := k.(*Ref); ok2 && ref.Name == s {
				return i
			}
		}
	}
	return -1
}

func (m *refMap) insert(k, v any) {
	if i := m.find(k); i >= 0 {
		m.vals[i] = v
		return
	}
	m.keys, m.vals = append(m.keys, k), append(m.vals, v)
}

func (m *refMap) remove(k any) {
	if i := m.find(k); i >= 0 {
		m.keys = append(m.keys[:i], m.keys[i+1:]...)
		m.vals = append(m.vals[:i], m.vals[i+1:]...)
	}
}

func mapKeyPool() []any {
	moscow := time.FixedZone("MSK", 3*3600)
	at := time.Date(2026, 11, 8, 9, 0, 0, 0, time.UTC)
	return []any{
		"а", "б", "ключ|7|мастер", "5", "Иванов", "", "0.1",
		5.0, int64(5), decimal.NewFromInt(5), decimal.RequireFromString("5.00"), 0.1, decimal.RequireFromString("0.10"), int64(7),
		at, at.In(moscow), at.Add(time.Hour), fmt.Sprintf("%v", at),
		&Ref{UUID: "u-1", Name: "Иванов", Type: "Клиенты"}, &Ref{UUID: "u-1", Name: "Иванов", Type: "Клиенты"},
		&Ref{UUID: "u-2", Name: "Петров", Type: "Клиенты"}, &Ref{UUID: "u-3", Name: "", Type: "Клиенты"},
		true, false, "true", nil,
	}
}

func TestMapIndexMatchesLinearScan(t *testing.T) {
	pool := mapKeyPool()
	for seed := int64(1); seed <= 300; seed++ {
		rnd := rand.New(rand.NewSource(seed))
		got, want := &Map{}, &refMap{}
		for step := 0; step < 400; step++ {
			k := pool[rnd.Intn(len(pool))]
			switch op := rnd.Intn(10); {
			case op < 5:
				v := float64(step)
				got.CallMethod("вставить", []any{k, v})
				want.insert(k, v)
			case op < 7:
				got.CallMethod("удалить", []any{k})
				want.remove(k)
			default:
				g := got.CallMethod("получить", []any{k})
				w := any(nil)
				if i := want.find(k); i >= 0 {
					w = want.vals[i]
				}
				if g != w {
					t.Fatalf("seed %d step %d: Получить(%#v) = %v, эталон %v", seed, step, k, g, w)
				}
				if c := got.CallMethod("содержитключ", []any{k}); c != (want.find(k) >= 0) {
					t.Fatalf("seed %d step %d: СодержитКлюч(%#v) = %v", seed, step, k, c)
				}
			}
			if c := got.CallMethod("количество", nil); c != float64(len(want.keys)) {
				t.Fatalf("seed %d step %d: Количество = %v, эталон %d", seed, step, c, len(want.keys))
			}
		}
		keys := got.Keys()
		if len(keys) != len(want.keys) {
			t.Fatalf("seed %d: ключей %d, эталон %d", seed, len(keys), len(want.keys))
		}
		for i := range keys {
			if keys[i] != want.keys[i] || got.vals[i] != want.vals[i] {
				t.Fatalf("seed %d: порядок обхода разошёлся на %d: %#v=%v, эталон %#v=%v", seed, i, keys[i], got.vals[i], want.keys[i], want.vals[i])
			}
		}
	}
}

// Удаление не сдвигает срезы: много удалений подряд не делают поиск
// квадратичным, порядок оставшихся сохраняется, после уплотнения поиск верен.
func TestMapDeleteKeepsOrderAndLookups(t *testing.T) {
	m := &Map{}
	for i := 0; i < 1000; i++ {
		m.CallMethod("вставить", []any{fmt.Sprintf("к%d", i), float64(i)})
	}
	for i := 0; i < 1000; i += 2 {
		m.CallMethod("удалить", []any{fmt.Sprintf("к%d", i)})
	}
	if c := m.CallMethod("количество", nil); c != float64(500) {
		t.Fatalf("Количество = %v", c)
	}
	for i := 0; i < 1000; i++ {
		got := m.CallMethod("получить", []any{fmt.Sprintf("к%d", i)})
		if i%2 == 0 && got != nil || i%2 == 1 && got != float64(i) {
			t.Fatalf("Получить(к%d) = %v", i, got)
		}
	}
	keys := m.Keys()
	for i, k := range keys {
		if k != fmt.Sprintf("к%d", 2*i+1) {
			t.Fatalf("порядок после удалений: [%d] = %v", i, k)
		}
	}
}
