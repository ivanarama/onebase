package ui

// Номер, выданный из модуля, обязан совпадать по формату с выданным платформой.
//
// `Нумераторы.СледующийНомер()` собирал номер сам и терял префикс базы (план
// 117D): объект, созданный обработкой, получал «Д-000001», а такой же через
// UI — «Ф-Д-000001». В разных базах номера, выданные из модулей, при этом
// совпадали — ровно то, ради предотвращения чего префикс базы и заведён.
//
// Тест идёт через настоящее хранилище и настоящий путь DSL: заглушка стора,
// которая собирает номер сама, проверяла бы копию формулы, а не платформу.

import (
	"context"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/ivantit66/onebase/internal/runtime"
)

func numeratorDSLServer(t *testing.T, basePrefix bool) (*Server, context.Context, *metadata.Entity) {
	t.Helper()
	doc := &metadata.Entity{
		Name: "Договор", Kind: metadata.KindDocument,
		Fields: []metadata.Field{
			{Name: "Номер", Type: metadata.FieldTypeString},
			{Name: "Дата", Type: metadata.FieldTypeDate},
		},
		Numerator: &metadata.Numerator{Prefix: "Д-", Length: 6, Period: "none", BasePrefix: basePrefix},
	}
	s, ctx := newSubmitTestServer(t, []*metadata.Entity{doc})
	if err := s.store.EnsureNumeratorSchema(ctx); err != nil {
		t.Fatalf("схема нумератора: %v", err)
	}
	if err := s.store.EnsureSettingsSchema(ctx); err != nil {
		t.Fatalf("схема настроек: %v", err)
	}
	return s, ctx, doc
}

func nextNumberFromDSL(t *testing.T, s *Server, ctx context.Context) string {
	t.Helper()
	prog := mustParse(t, `Функция Проверка() Экспорт
  Возврат Нумераторы.СледующийНомер("Договор");
КонецФункции`)
	var result any
	vars, _ := s.buildDSLVarsTx(ctx, runtime.NewMovementsCollector("test", uuid.Nil))
	if err := s.interp.RunWithResult(prog.Procedures[0], nil, &result, vars); err != nil {
		t.Fatalf("СледующийНомер: %v", err)
	}
	got, _ := result.(string)
	return got
}

// Префикс базы попадает в номер, выданный из модуля.
func TestDSLNumerator_UsesBasePrefix(t *testing.T) {
	s, ctx, ent := numeratorDSLServer(t, true)
	if err := s.store.SaveBasePrefix(ctx, "Ф-"); err != nil {
		t.Fatalf("префикс базы: %v", err)
	}

	got := nextNumberFromDSL(t, s, ctx)
	if !strings.HasPrefix(got, "Ф-Д-") {
		t.Errorf("номер из модуля = %q, ожидался с префиксом базы «Ф-Д-»", got)
	}

	// И совпадает по формату с тем, что выдаёт сама платформа.
	platform, err := s.store.GenerateNumber(ctx, ent, map[string]any{})
	if err != nil {
		t.Fatalf("GenerateNumber: %v", err)
	}
	if trimNumberTail(got) != trimNumberTail(platform) {
		t.Errorf("формат разошёлся: из модуля %q, из платформы %q", got, platform)
	}
}

// Без base_prefix: true формат не меняется — включение префикса на базе не
// должно молча переформатировать номера тех объектов, где он не объявлен.
func TestDSLNumerator_WithoutBasePrefixUnchanged(t *testing.T) {
	s, ctx, _ := numeratorDSLServer(t, false)
	if err := s.store.SaveBasePrefix(ctx, "Ф-"); err != nil {
		t.Fatalf("префикс базы: %v", err)
	}
	got := nextNumberFromDSL(t, s, ctx)
	if !strings.HasPrefix(got, "Д-") {
		t.Errorf("номер = %q, ожидался «Д-…» без префикса базы", got)
	}
}

// Счётчик у модуля и платформы ОБЩИЙ: иначе два пути выдали бы один номер
// дважды, и уникальность кода ловила бы это уже при записи.
func TestDSLNumerator_SharesCounterWithPlatform(t *testing.T) {
	s, ctx, ent := numeratorDSLServer(t, false)

	first := nextNumberFromDSL(t, s, ctx)
	second, err := s.store.GenerateNumber(ctx, ent, map[string]any{})
	if err != nil {
		t.Fatalf("GenerateNumber: %v", err)
	}
	if first == second {
		t.Errorf("модуль и платформа выдали один номер %q — счётчик разошёлся", first)
	}
}

// trimNumberTail отрезает числовой хвост, оставляя префиксную часть.
func trimNumberTail(v string) string {
	i := len(v)
	for i > 0 && v[i-1] >= '0' && v[i-1] <= '9' {
		i--
	}
	return v[:i]
}
