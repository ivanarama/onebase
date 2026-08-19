package ui

import (
	"testing"

	"github.com/ivantit66/onebase/internal/dsl/interpreter"
	"github.com/ivantit66/onebase/internal/runtime"
)

// Сервер штатно поднимается без БД (ui.New(reg, nil, ...) — см. тесты
// обработчиков событий), и сборка сервиса обязана положить в поле-порт
// нетипизированный nil.
//
// Грабля, ради которой существует тест: Service.Store — интерфейс
// (entityservice.Storage), а интерфейс, хранящий nil-указатель *storage.DB, сам
// по себе НЕ равен nil. Прямое `Store: s.store` компилируется, выглядит
// правильно и молча обезвреживает проверки `s.Store == nil` внутри сервиса —
// вместо внятной ошибки «не задано хранилище» получается паника на первом
// обращении к базе. Компилятор такое не ловит, поэтому ловит тест.
func TestNewEntityServiceKeepsNilStoreUntyped(t *testing.T) {
	s := &Server{reg: runtime.NewRegistry(), interp: interpreter.New()}

	svc := s.newEntityService(nil)

	if svc.Store != nil {
		t.Fatal("Store != nil при сервере без БД: в поле-порт попал типизированный nil, " +
			"проверки хранилища внутри сервиса перестали срабатывать")
	}
}
