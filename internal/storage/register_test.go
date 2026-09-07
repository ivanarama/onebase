package storage

import "testing"

// fakeRef имитирует *interpreter.Ref для теста — мы не можем импортировать
// сам interpreter (циклическая зависимость), но контракт тот же:
// GetRefUUID() string + String() string.
type fakeRef struct {
	uuid string
	name string
}

func (r *fakeRef) GetRefUUID() string { return r.uuid }
func (r *fakeRef) String() string     { return r.name }

// writeMovements должен правильно нормализовать Ref-значение
// для reference:*-измерения (→ UUID).
func TestNormalizeRegArg_RefField_UUID(t *testing.T) {
	d := SQLiteDialect{}
	ref := &fakeRef{uuid: "11111111-1111-1111-1111-111111111111", name: "Тумбочка"}
	got := normalizeRegArg(d, ref, true /*isRef*/)
	// Для SQLite UUID лежит как text — idArg в SQLite вернёт строку.
	s, ok := got.(string)
	if !ok {
		t.Fatalf("ожидалась string-сериализация UUID, получили %T (%v)", got, got)
	}
	if s != "11111111-1111-1111-1111-111111111111" {
		t.Errorf("неверный UUID: %s", s)
	}
}

// writeMovements не должен падать на *Ref в string-измерении —
// падает в pgx «unsupported type interpreter.Ref, a struct». Должен
// сериализоваться через display-имя.
func TestNormalizeRegArg_StringField_DisplayName(t *testing.T) {
	d := SQLiteDialect{}
	ref := &fakeRef{uuid: "11111111-1111-1111-1111-111111111111", name: "Тумбочка"}
	got := normalizeRegArg(d, ref, false /*isRef=false → string column*/)
	s, ok := got.(string)
	if !ok {
		t.Fatalf("ожидалась string, получили %T (%v)", got, got)
	}
	if s != "Тумбочка" {
		t.Errorf("ожидалось display-имя «Тумбочка», получили %q", s)
	}
}

// Не-Ref значения не должны меняться.
func TestNormalizeRegArg_PlainString(t *testing.T) {
	d := SQLiteDialect{}
	got := normalizeRegArg(d, "обычная строка", false)
	if got != "обычная строка" {
		t.Errorf("plain string изменилась: %v", got)
	}
}

func TestNormalizeRegArg_Number(t *testing.T) {
	d := SQLiteDialect{}
	got := normalizeRegArg(d, float64(42), false)
	if got != float64(42) {
		t.Errorf("число изменилось: %v", got)
	}
}

func TestNormalizeRegArg_Nil(t *testing.T) {
	d := SQLiteDialect{}
	if got := normalizeRegArg(d, nil, false); got != nil {
		t.Errorf("nil → %v", got)
	}
	if got := normalizeRegArg(d, nil, true); got != nil {
		t.Errorf("nil (ref) → %v", got)
	}
}

func TestNormalizeRegArg_EmptyReferenceIsNull(t *testing.T) {
	d := SQLiteDialect{}
	if got := normalizeRegArg(d, &fakeRef{}, true); got != nil {
		t.Errorf("пустой Ref → %T(%v), ожидался nil", got, got)
	}
	if got := normalizeRegArg(d, "", true); got != nil {
		t.Errorf("пустая строка ссылки → %T(%v), ожидался nil", got, got)
	}
}

// UUID-строка в reference-поле должна пройти через idArg.
func TestNormalizeRegArg_UUIDString_AsRef(t *testing.T) {
	d := SQLiteDialect{}
	got := normalizeRegArg(d, "22222222-2222-2222-2222-222222222222", true)
	s, ok := got.(string)
	if !ok {
		t.Fatalf("ожидалась строка (SQLite дайалект), получили %T", got)
	}
	if s != "22222222-2222-2222-2222-222222222222" {
		t.Errorf("UUID не сохранился: %s", s)
	}
}
