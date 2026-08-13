package ui

// НайтиПоКоду и соседи объясняют отказ вместо «Неопределено» (план 117, Д3).
//
// Тесты идут через ту же точку, что и модуль конфигурации: разбор текста и
// исполнение интерпретатором с реальными объектами доступа. Дёргать
// CatalogProxy.findByField напрямую здесь было бы бессмысленно — проверять надо
// то, что увидит автор конфигурации.

import (
	"context"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/ivantit66/onebase/internal/runtime"
)

// runFindDSL исполняет функцию и возвращает результат и ошибку (а не валит
// тест): отказ здесь — ожидаемый исход половины проверок.
func runFindDSL(t *testing.T, s *Server, ctx context.Context, src string) (any, error) {
	t.Helper()
	prog := mustParse(t, src)
	var result any
	vars, _ := s.buildDSLVarsTx(ctx, runtime.NewMovementsCollector("test", uuid.Nil))
	err := s.interp.RunWithResult(prog.Procedures[0], nil, &result, vars)
	return result, err
}

func findByCodeServer(t *testing.T) (*Server, context.Context, *metadata.Entity) {
	t.Helper()
	withCode := &metadata.Entity{
		Name: "Контрагенты", Kind: metadata.KindCatalog,
		Fields: []metadata.Field{
			{Name: metadata.StandardCodeField, Type: metadata.FieldTypeString},
			{Name: "Наименование", Type: metadata.FieldTypeString},
		},
		Numerator: &metadata.Numerator{Prefix: "К-", Length: 6, Period: "none"},
	}
	// Справочник без нумератора: «Кода» у него нет вовсе — именно на нём
	// НайтиПоКоду молчал.
	noCode := &metadata.Entity{
		Name: "Склады", Kind: metadata.KindCatalog,
		Fields: []metadata.Field{{Name: "Наименование", Type: metadata.FieldTypeString}},
	}
	s, ctx := newSubmitTestServer(t, []*metadata.Entity{withCode, noCode})
	if err := s.store.Upsert(ctx, withCode.Name, uuid.New(),
		map[string]any{metadata.StandardCodeField: "К-000042", "Наименование": "Альфа"}, withCode); err != nil {
		t.Fatalf("вставка: %v", err)
	}
	return s, ctx, withCode
}

// Д3: у справочника без «Кода» поиск по коду возвращал «Неопределено» — модуль
// молча уходил в ветку «не нашли», и автор конфигурации искал причину в данных,
// а не в метаданных.
func TestFindByCode_MissingFieldExplains(t *testing.T) {
	s, ctx, _ := findByCodeServer(t)
	_, err := runFindDSL(t, s, ctx, `Функция Проверка() Экспорт
  Возврат Справочники.Склады.НайтиПоКоду("К-000042");
КонецФункции`)
	if err == nil {
		t.Fatal("поиск по несуществующему реквизиту прошёл молча")
	}
	text := err.Error()
	for _, want := range []string{"НайтиПоКоду", "Склады", "numerator"} {
		if !strings.Contains(text, want) {
			t.Errorf("в сообщении нет %q: %s", want, text)
		}
	}
}

// Произвольный реквизит объясняется иначе: там причина — опечатка или не тот
// справочник, про numerator: говорить незачем.
func TestFindByAttribute_MissingFieldExplains(t *testing.T) {
	s, ctx, _ := findByCodeServer(t)
	_, err := runFindDSL(t, s, ctx, `Функция Проверка() Экспорт
  Возврат Справочники.Контрагенты.НайтиПоРеквизиту("ИНН", "7701234567");
КонецФункции`)
	if err == nil {
		t.Fatal("поиск по опечатке в имени реквизита прошёл молча")
	}
	text := err.Error()
	if !strings.Contains(text, "ИНН") || !strings.Contains(text, "НайтиПоРеквизиту") {
		t.Errorf("сообщение не называет ни метод, ни реквизит: %s", text)
	}
	if strings.Contains(text, "numerator") {
		t.Errorf("подсказка про numerator: неуместна для произвольного реквизита: %s", text)
	}
}

// «Не нашли» осталось «Неопределено»: это единственный случай, когда пустой
// результат — нормальный ответ, и ломать его нельзя.
func TestFindByCode_NotFoundStaysUndefined(t *testing.T) {
	s, ctx, _ := findByCodeServer(t)
	got, err := runFindDSL(t, s, ctx, `Функция Проверка() Экспорт
  Возврат Справочники.Контрагенты.НайтиПоКоду("К-999999") = Неопределено;
КонецФункции`)
	if err != nil {
		t.Fatalf("отсутствие записи стало ошибкой: %v", err)
	}
	if got != true {
		t.Errorf("НайтиПоКоду для несуществующего кода вернул не Неопределено: %v", got)
	}
}

// Существующий код по-прежнему находится — фикс не должен сломать основной путь.
func TestFindByCode_FindsExisting(t *testing.T) {
	s, ctx, _ := findByCodeServer(t)
	got, err := runFindDSL(t, s, ctx, `Функция Проверка() Экспорт
  Ссылка = Справочники.Контрагенты.НайтиПоКоду("К-000042");
  Возврат Ссылка.ПолучитьОбъект().Наименование;
КонецФункции`)
	if err != nil {
		t.Fatalf("поиск существующего кода: %v", err)
	}
	if s, _ := got.(string); !strings.Contains(s, "Альфа") {
		t.Errorf("найдено не то: %v", got)
	}
}

// Число вместо строки больше не молчит: код «42» и число 42 для пользователя
// одно и то же, и раньше второй вариант возвращал «Неопределено» без причины.
func TestFindByCode_NumericArgumentIsConverted(t *testing.T) {
	s, ctx, ent := findByCodeServer(t)
	// Сущность берём ту же, что зарегистрирована: выбирать её из реестра по
	// индексу нельзя — порядок там не гарантирован, и тест то писал бы строку
	// не в тот справочник, то нет.
	if err := s.store.Upsert(ctx, ent.Name, uuid.New(),
		map[string]any{metadata.StandardCodeField: "42", "Наименование": "Сорок два"}, ent); err != nil {
		t.Fatalf("вставка: %v", err)
	}
	got, err := runFindDSL(t, s, ctx, `Функция Проверка() Экспорт
  Возврат Справочники.Контрагенты.НайтиПоКоду(42).ПолучитьОбъект().Наименование;
КонецФункции`)
	if err != nil {
		t.Fatalf("числовой аргумент: %v", err)
	}
	if s, _ := got.(string); !strings.Contains(s, "Сорок два") {
		t.Errorf("по числовому коду не найдено: %v", got)
	}
}

// Вызов без аргумента — ошибка автора конфигурации, а не пустой результат.
func TestFindByCode_NoArgumentIsError(t *testing.T) {
	s, ctx, _ := findByCodeServer(t)
	_, err := runFindDSL(t, s, ctx, `Функция Проверка() Экспорт
  Возврат Справочники.Контрагенты.НайтиПоКоду();
КонецФункции`)
	if err == nil {
		t.Fatal("вызов без аргумента прошёл молча")
	}
	if !strings.Contains(err.Error(), "не указано значение") {
		t.Errorf("невнятное сообщение: %v", err)
	}
}
