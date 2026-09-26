package ui

import (
	"testing"

	"github.com/google/uuid"
	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/ivantit66/onebase/internal/runtime"
)

// Объект управляемой формы должен годиться в основание для Заполнить().
//
// docWriter.fill принимает объект-основание через неэкспортируемый интерфейс
// runtimeObject(). Модуль документа (entityHookThis) его реализует, а форма —
// нет, и Заполнить(Объект) в обработчике кнопки падал с «ожидается ссылка или
// объект, получено *ui.formObjectThis». Тест фиксирует сам контракт: тот же
// интерфейс, что и у fill.
func TestFormObjectIsFillSource(t *testing.T) {
	id := uuid.New()
	обj := &runtime.Object{Type: "А_Звонок", ID: id}
	var источник any = &formObjectThis{
		obj:    обj,
		entity: &metadata.Entity{Name: "А_Звонок", Kind: metadata.KindDocument},
	}

	v, ok := источник.(interface{ runtimeObject() *runtime.Object })
	if !ok {
		t.Fatalf("объект формы не годится в основание для Заполнить: %T", источник)
	}
	got := v.runtimeObject()
	if got == nil {
		t.Fatal("основание пустое: fill сообщит «объект-основание пустой»")
	}
	if got.Type != "А_Звонок" || got.ID != id {
		t.Errorf("основание не та запись: type=%q id=%v", got.Type, got.ID)
	}
}

// Нулевой приёмник не должен ронять интерпретатор: fill отличает пустое
// основание от отсутствующего метода и отвечает понятной ошибкой.
func TestFormObjectFillSourceNilSafe(t *testing.T) {
	var f *formObjectThis
	// Через интерфейс, а не напрямую: прямой вызов не компилировался бы без
	// метода, и тест краснел бы сборкой вместо внятного сообщения.
	var источник any = f
	v, ok := источник.(interface{ runtimeObject() *runtime.Object })
	if !ok {
		t.Fatalf("объект формы не годится в основание для Заполнить: %T", источник)
	}
	if v.runtimeObject() != nil {
		t.Error("нулевой объект формы вернул непустое основание")
	}
}
