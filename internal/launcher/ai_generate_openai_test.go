package launcher

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/ivantit66/onebase/internal/llm"
)

const scaffoldReasoning = "opaque-test-value"

// Тестовый провайдер выбирает инструмент по его схеме и требует вернуть
// reasoning_content своего предыдущего сообщения вместе с результатом вызова.
func scaffoldProvider() *httptest.Server {
	validName := regexp.MustCompile("^[a-zA-Z0-9_-]{1,64}$")
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Tools []struct {
				Function struct {
					Name       string
					Parameters struct{ Properties map[string]json.RawMessage }
				}
			}
			Messages []struct {
				Role      string
				Content   string
				Reasoning string `json:"reasoning_content"`
				ToolID    string `json:"tool_call_id"`
			}
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		var createObject string
		for i, tool := range req.Tools {
			if !validName.MatchString(tool.Function.Name) {
				http.Error(w, fmt.Sprintf("Invalid tools[%d].function.name: %q", i, tool.Function.Name), http.StatusBadRequest)
				return
			}
			if _, ok := tool.Function.Parameters.Properties["yaml"]; ok {
				createObject = tool.Function.Name
			}
		}
		if createObject == "" || len(req.Messages) == 0 {
			http.Error(w, "missing create-object tool or messages", http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		last := req.Messages[len(req.Messages)-1]
		if last.Role == "tool" {
			if last.ToolID != "call-1" || !strings.Contains(last.Content, "Клиент") {
				http.Error(w, "missing tool result", http.StatusBadRequest)
				return
			}
			if len(req.Messages) < 2 || req.Messages[len(req.Messages)-2].Reasoning != scaffoldReasoning {
				http.Error(w, "missing assistant reasoning_content", http.StatusBadRequest)
				return
			}
			_, _ = fmt.Fprint(w, `{"choices":[{"message":{"content":"Готово"}}]}`)
			return
		}
		_, _ = fmt.Fprintf(w, `{"choices":[{"message":{"reasoning_content":%q,"tool_calls":[{"id":"call-1","type":"function","function":{"name":%q,"arguments":"{\"тип\":\"справочник\",\"имя\":\"Клиент\",\"yaml\":\"name: Клиент\\n\"}"}}]}}]}`, scaffoldReasoning, createObject)
	}))
}

func TestConfiguratorAIGenerateOpenAIToolRoundTrip(t *testing.T) {
	provider := scaffoldProvider()
	defer provider.Close()
	base := &Base{Path: t.TempDir(), ConfigSource: "file", DBType: "sqlite", DBPath: filepath.Join(t.TempDir(), "base.db")}
	store := &Store{path: filepath.Join(t.TempDir(), "ibases.yaml")}
	if err := store.Add(base); err != nil {
		t.Fatal(err)
	}
	db, err := OpenDB(t.Context(), base)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	cfg := llm.Config{
		Enabled:    true,
		LogHistory: true,
		Endpoints:  []llm.Endpoint{{Name: "deepseek", Kind: llm.KindOpenAI, BaseURL: provider.URL, APIKey: "test-key"}},
		Models:     []llm.Model{{Name: "test-model", Endpoint: "deepseek"}},
		Profiles:   []llm.Profile{{Task: "конфигуратор", Models: []string{"test-model"}}},
	}
	if err := db.SaveLLMConfig(t.Context(), cfg); err != nil {
		t.Fatal(err)
	}

	savedBundle := launcherBundle
	t.Cleanup(func() { launcherBundle = savedBundle })
	srv, err := NewServer(store, NewRunner())
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- srv.ListenAndServe() }()
	t.Cleanup(func() { srv.Close(); <-done })
	url := srv.URL() + "/bases/" + base.ID + "/configurator/ai-generate"
	req, err := http.NewRequestWithContext(t.Context(), http.MethodPost, url, strings.NewReader(`{"prompt":"Создай справочник Клиент"}`))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	var result struct {
		OK        bool
		Text      string
		Error     string
		Changes   []GenChange
		ToolTrace []GenToolTrace
	}
	if err := json.Unmarshal(raw, &result); err != nil {
		t.Fatalf("генерация каркаса: HTTP %d: %v", resp.StatusCode, err)
	}
	if !result.OK || result.Text != "Готово" || result.Error != "" {
		t.Fatalf("генерация каркаса: %+v", result)
	}
	if len(result.Changes) != 1 || result.Changes[0].Path != "catalogs/клиент.yaml" || result.Changes[0].NewContent != "name: Клиент\n" {
		t.Fatalf("неверный черновик справочника: %+v", result.Changes)
	}
	if len(result.ToolTrace) != 1 || result.ToolTrace[0].Name != "создать_объект" || strings.Contains(string(raw), scaffoldReasoning) {
		t.Fatalf("неверная трассировка в ответе конфигуратора: %s", raw)
	}
	history, err := db.ListAIAudit(t.Context(), 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(history) != 1 || !strings.Contains(history[0].Response, "создать_объект [ok]") || strings.Contains(history[0].Response, scaffoldReasoning) {
		t.Fatalf("неверная запись в журнале конфигуратора: %+v", history)
	}
}
