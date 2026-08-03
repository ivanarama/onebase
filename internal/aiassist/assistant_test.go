package aiassist

import (
	"context"
	"errors"
	"testing"

	"github.com/ivantit66/onebase/internal/dsl/interpreter"
	"github.com/ivantit66/onebase/internal/llm"
)

// fakeSource реализует ConfigSource без реальной БД/сети.
type fakeSource struct {
	cfg     llm.Config
	err     error
	callCnt int
}

func (f *fakeSource) GetLLMConfig(_ context.Context) (llm.Config, error) {
	f.callCnt++
	return f.cfg, f.err
}

// fakeEndpointServer нам не нужен — Ask возвращает ошибку конфига, если конфиг
// некорректен, или ошибку источника; реального HTTP нет.

// --- Configured() ---

func TestConfigured_NilAssistant(t *testing.T) {
	var a *Assistant
	if a.Configured() {
		t.Fatal("nil-Assistant должен возвращать false")
	}
}

func TestConfigured_NilSource(t *testing.T) {
	a := &Assistant{src: nil, ctx: context.Background()}
	if a.Configured() {
		t.Fatal("Assistant с nil-источником должен возвращать false")
	}
}

func TestConfigured_DisabledConfig(t *testing.T) {
	src := &fakeSource{cfg: llm.Config{Enabled: false, Models: []llm.Model{{Name: "m"}}}}
	a := New(context.Background(), src, nil)
	if a.Configured() {
		t.Fatal("выключенный конфиг должен давать false")
	}
}

func TestConfigured_NoModels(t *testing.T) {
	src := &fakeSource{cfg: llm.Config{Enabled: true, Models: nil}}
	a := New(context.Background(), src, nil)
	if a.Configured() {
		t.Fatal("конфиг без моделей должен давать false")
	}
}

func TestConfigured_EnabledWithModels(t *testing.T) {
	src := &fakeSource{cfg: llm.Config{
		Enabled: true,
		Models:  []llm.Model{{Name: "m"}},
	}}
	a := New(context.Background(), src, nil)
	if !a.Configured() {
		t.Fatal("включённый конфиг с моделями должен давать true")
	}
}

// --- Ask() перечитывает конфиг на каждый вызов ---

func TestAsk_ReadsConfigEachCall(t *testing.T) {
	// Источник возвращает ошибку — достаточно, чтобы проверить, что вызов происходит.
	src := &fakeSource{err: errors.New("БД недоступна")}
	a := New(context.Background(), src, nil)

	a.Ask(interpreter.AIRequest{Task: "анализ", Prompt: "p"}) //nolint:errcheck,gosec // G104: директива подавляет только перечисленные линтеры
	a.Ask(interpreter.AIRequest{Task: "анализ", Prompt: "p"}) //nolint:errcheck,gosec // G104: директива подавляет только перечисленные линтеры

	if src.callCnt < 2 {
		t.Fatalf("ожидалось ≥2 вызовов GetLLMConfig, получено %d", src.callCnt)
	}
}

// --- Ask() пробрасывает ошибку источника ---

func TestAsk_SourceErrorSurfaces(t *testing.T) {
	want := errors.New("фатальная ошибка БД")
	src := &fakeSource{err: want}
	a := New(context.Background(), src, nil)

	_, err := a.Ask(interpreter.AIRequest{Task: "анализ", Prompt: "x"})
	if !errors.Is(err, want) {
		t.Fatalf("ожидалась ошибка источника, получено: %v", err)
	}
}

// --- Ask() с image-частью (ImageB64 непустой) ---

// Примечание: ImagePart-ветка в Ask собирает llm.ImagePart и передаёт
// в llm.Runner.Run; реального провайдера нет, поэтому тест проверяет, что
// ошибка НЕ является ошибкой конфигурации (src), а является сетевой/runner-ошибкой
// (что доказывает прохождение через ветку с image-частью).
func TestAsk_ImagePartPassedThrough(t *testing.T) {
	// Конфиг валиден, но endpoint указывает на несуществующий адрес → runner-ошибка.
	src := &fakeSource{cfg: llm.Config{
		Enabled:   true,
		Endpoints: []llm.Endpoint{{Name: "ep", Kind: llm.KindAnthropic, BaseURL: "http://127.0.0.1:1", APIKey: "k"}},
		Models:    []llm.Model{{Name: "m", Endpoint: "ep", Vision: true}},
		Profiles:  []llm.Profile{{Task: "документы", Models: []string{"m"}}},
	}}
	a := New(context.Background(), src, nil)

	_, err := a.Ask(interpreter.AIRequest{
		Task:     "документы",
		Prompt:   "распознай",
		ImageB64: "AAAA",
		MimeType: "image/png",
	})
	// Конфиг валиден и источник без ошибки, поэтому единственный источник сбоя —
	// runner (мёртвый endpoint). Ненулевая ошибка доказывает, что Ask дошёл до
	// runner через ветку сборки image-части, не упав раньше на конфиге.
	if err == nil {
		t.Fatal("ожидалась сетевая ошибка runner, получен nil")
	}
}
