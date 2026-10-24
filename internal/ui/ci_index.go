package ui

import (
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/ivantit66/onebase/internal/runtime"
)

// Поиск ключа карты без учёта регистра перебором (maskCIKeyValue,
// snapshotValueCI, runtime.Object.Get) стоит O(ключей), а циклы по полям
// сущности зовут его на каждое поле: у заявки колл-центра (сотни реквизитов,
// ключи в двух регистрах) такие циклы занимали сотни мс на событие формы. В
// горячих циклах карту индексируют один раз.
//
// Индекс годится, пока карта не меняется; цикл, добавляющий ключи, обязан
// дописывать их в индекс сам (add).

type ciIndex struct {
	fold func(string) string
	keys map[string][]string
}

func newCIIndex[V any](m map[string]V, fold func(string) string) ciIndex {
	ix := ciIndex{fold: fold, keys: make(map[string][]string, len(m))}
	for k := range m {
		ix.add(k)
	}
	return ix
}

func (ix ciIndex) add(k string) {
	f := ix.fold(k)
	ix.keys[f] = append(ix.keys[f], k)
}

// ciGet — значение под одним из ключей, равных name по правилу индекса. Если
// таких ключей несколько, возвращает любой из них — как и перебор, порядок
// которого задаёт случайный обход карты.
func ciGet[V any](m map[string]V, ix ciIndex, name string) (string, V, bool) {
	for _, k := range ix.keys[ix.fold(name)] {
		if v, ok := m[k]; ok {
			return k, v, true
		}
	}
	var zero V
	return "", zero, false
}

// equalFoldKey — ключ класса строк, равных по strings.EqualFold: каждая руна
// заменяется наименьшей руной своей орбиты unicode.SimpleFold. EqualFold
// сравнивает строки по рунам именно принадлежностью одной орбите, поэтому
// EqualFold(a, b) == (equalFoldKey(a) == equalFoldKey(b)).
func equalFoldKey(s string) string {
	ascii := true
	for i := 0; i < len(s); i++ {
		if s[i] >= utf8.RuneSelf {
			ascii = false
			break
		}
	}
	if ascii {
		// Орбита ASCII-буквы — {A, a} (у k и s есть ещё не-ASCII K и ſ, но
		// наименьшая руна орбиты всё равно заглавная ASCII).
		return strings.ToUpper(s)
	}
	var b strings.Builder
	b.Grow(len(s))
	for _, r := range s {
		least := r
		for f := unicode.SimpleFold(r); f != r; f = unicode.SimpleFold(f) {
			if f < least {
				least = f
			}
		}
		b.WriteRune(least)
	}
	return b.String()
}

// objectGetIndexed — runtime.Object.Get с индексом полей по strings.ToLower
// (то же сравнение, что в Get): табличная часть с таким именем, как и там,
// важнее поля.
func objectGetIndexed(obj *runtime.Object, fields ciIndex, name string) any {
	if _, ok := tableRowsLower(obj.TablePartRows, name); ok {
		return obj.Get(name)
	}
	_, v, _ := ciGet(obj.Fields, fields, name)
	return v
}

func tableRowsLower(tables map[string][]map[string]any, name string) ([]map[string]any, bool) {
	low := strings.ToLower(name)
	for k, rows := range tables {
		if strings.ToLower(k) == low {
			return rows, true
		}
	}
	return nil, false
}
