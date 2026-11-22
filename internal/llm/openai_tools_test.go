// internal/llm/openai_tools_test.go
package llm

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"regexp"
	"slices"
	"strings"
	"testing"
)

// Один ответ модели вызывает каждую объявленную функцию и одну неизвестную.
func TestRunWithToolsOpenAI(t *testing.T) {
	localNames := []string{"остаток", strings.Repeat("a", 65), "onebase_tool_1", strings.Repeat("Z", 64)}
	validName := regexp.MustCompile(`^[a-zA-Z0-9_-]{1,64}$`)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Tools []struct {
				Function struct{ Name, Description string }
			}
			Messages []struct {
				Role, Content string
				ToolCallID    string                                     `json:"tool_call_id"`
				ToolCalls     []struct{ Function struct{ Name string } } `json:"tool_calls"`
			}
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil || len(req.Tools) != len(localNames) {
			t.Errorf("tools: %d, err: %v", len(req.Tools), err)
			http.Error(w, "invalid request", http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		var wireNames []string
		for i, tool := range req.Tools {
			name := tool.Function.Name
			if !validName.MatchString(name) || slices.Contains(wireNames, name) {
				t.Errorf("недопустимое или повторное имя %q", name)
			}
			if validName.MatchString(localNames[i]) {
				if name != localNames[i] {
					t.Errorf("имя %q изменено на %q", localNames[i], name)
				}
			} else if !strings.Contains(tool.Function.Description, localNames[i]) {
				t.Errorf("в описании %q потеряно имя %q из инструкций", name, localNames[i])
			}
			wireNames = append(wireNames, name)
		}
		wireNames = append(wireNames, "not_listed")
		if len(req.Messages) == 1 {
			var calls []string
			for i, name := range wireNames {
				calls = append(calls, fmt.Sprintf(`{"id":"c%d","type":"function","function":{"name":%q,"arguments":"{\"товар\":\"гвозди\"}"}}`, i, name))
			}
			_, _ = fmt.Fprintf(w, `{"choices":[{"message":{"tool_calls":[%s]}}]}`, strings.Join(calls, ","))
			return
		}
		if len(req.Messages) != 2+len(wireNames) || len(req.Messages[1].ToolCalls) != len(wireNames) {
			t.Errorf("незавершённый обмен с инструментами: %+v", req.Messages)
		} else {
			for i, call := range req.Messages[1].ToolCalls {
				if call.Function.Name != wireNames[i] {
					t.Errorf("модели вернулось имя %q вместо %q", call.Function.Name, wireNames[i])
				}
				result := req.Messages[i+2]
				if result.Role != "tool" || result.ToolCallID != fmt.Sprintf("c%d", i) || result.Content == "" {
					t.Errorf("неверный ответ инструмента %q: %+v", wireNames[i], result)
				}
			}
		}
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"Остаток: 42"}}]}`))
	}))
	defer srv.Close()

	cfg := Config{
		Enabled:   true,
		Endpoints: []Endpoint{{Name: "o", Kind: KindOpenAI, BaseURL: srv.URL, APIKey: "k"}},
		Models:    []Model{{Name: "m", Endpoint: "o"}},
		Profiles:  []Profile{{Task: "чат", Models: []string{"m"}}},
	}
	var tools []Tool
	for _, name := range localNames {
		tools = append(tools, Tool{Name: name, Description: "Остаток товара", Schema: map[string]any{
			"type": "object", "properties": map[string]any{"товар": map[string]any{"type": "string"}},
		}})
	}
	var executed []string
	exec := func(ctx context.Context, call ToolCall) ToolResult {
		if call.Input["товар"] != "гвозди" {
			t.Errorf("аргумент не распознан: %+v", call.Input)
		}
		executed = append(executed, call.Name)
		return ToolResult{ID: call.ID, Content: "42"}
	}
	resp, err := New(cfg, nil).RunWithTools(t.Context(), "чат",
		ChatRequest{Messages: []Message{UserText("сколько гвоздей?")}}, tools, exec)
	if err != nil || resp.Text != "Остаток: 42" {
		t.Fatalf("ответ %q, ошибка %v", resp.Text, err)
	}
	if !slices.Equal(executed, localNames) {
		t.Fatalf("вызванные инструменты %q, ожидались %q", executed, localNames)
	}
}

// TestRunWithToolsOpenAI_StopFinishReason — регрессия: OpenAI-совместимые
// провайдеры (Ollama, LM Studio, прокси) присылают tool_calls с
// finish_reason:"stop". Инструмент всё равно обязан исполниться, а не
// проглатываться (раньше гейт на == "tool_calls" терял вызов).
func TestRunWithToolsOpenAI_StopFinishReason(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		w.Header().Set("Content-Type", "application/json")
		if strings.Contains(string(body), `"role":"tool"`) {
			_, _ = w.Write([]byte(`{"choices":[{"finish_reason":"stop","message":{"content":"Остаток: 42"}}],"usage":{}}`))
			return
		}
		// tool_calls присутствуют, но finish_reason — "stop", а не "tool_calls".
		_, _ = w.Write([]byte(`{"choices":[{"finish_reason":"stop","message":{"content":"","tool_calls":[{"id":"c1","type":"function","function":{"name":"stock","arguments":"{\"товар\":\"гвозди\"}"}}]}}],"usage":{}}`))
	}))
	defer srv.Close()
	cfg := Config{
		Enabled:   true,
		Endpoints: []Endpoint{{Name: "o", Kind: KindOpenAI, BaseURL: srv.URL, APIKey: "k"}},
		Models:    []Model{{Name: "m", Endpoint: "o"}},
		Profiles:  []Profile{{Task: "чат", Models: []string{"m"}}},
	}
	var gotCall ToolCall
	exec := func(ctx context.Context, call ToolCall) ToolResult {
		gotCall = call
		return ToolResult{ID: call.ID, Content: "42"}
	}
	tools := []Tool{{Name: "stock", Description: "остаток товара", Schema: map[string]any{
		"type": "object", "properties": map[string]any{"товар": map[string]any{"type": "string"}},
	}}}
	r := New(cfg, nil)
	resp, err := r.RunWithTools(context.Background(), "чат",
		ChatRequest{Messages: []Message{UserText("сколько гвоздей?")}}, tools, exec)
	if err != nil {
		t.Fatalf("RunWithTools: %v", err)
	}
	if gotCall.Name != "stock" {
		t.Fatalf("инструмент не исполнен при finish_reason=stop: %+v", gotCall)
	}
	if resp.Text != "Остаток: 42" {
		t.Fatalf("неожиданный текст: %q", resp.Text)
	}
}

// TestRunWithToolsOpenAI_MalformedToolCall — регрессия #21: OpenAI требует ровно
// один ответ role=tool на каждый tool_call, иначе следующий POST вернёт 400.
// Раньше при битом tool_call (json.Unmarshal падал) делался continue → для него
// tool-ответа не было, инвариант нарушался. Теперь добавляем tool-ответ с
// текстом ошибки и для битого вызова: число tool-сообщений == числу tool_calls.
func TestRunWithToolsOpenAI_MalformedToolCall(t *testing.T) {
	var toolMsgCount int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		w.Header().Set("Content-Type", "application/json")
		if strings.Contains(string(body), `"role":"tool"`) {
			// Считаем tool-сообщения во втором запросе (по числу tool_call_id).
			toolMsgCount = strings.Count(string(body), `"tool_call_id"`)
			_, _ = w.Write([]byte(`{"choices":[{"finish_reason":"stop","message":{"content":"Готово"}}],"usage":{}}`))
			return
		}
		// Два tool_call: валидный c1 и битый элемент (JSON-строка, а не объект) —
		// json.Unmarshal в структуру для него падает.
		_, _ = w.Write([]byte(`{"choices":[{"finish_reason":"tool_calls","message":{"content":"","tool_calls":[` +
			`{"id":"c1","type":"function","function":{"name":"stock","arguments":"{\"товар\":\"гвозди\"}"}},` +
			`"СЛОМАНО"` +
			`]}}],"usage":{}}`))
	}))
	defer srv.Close()
	cfg := Config{
		Enabled:   true,
		Endpoints: []Endpoint{{Name: "o", Kind: KindOpenAI, BaseURL: srv.URL, APIKey: "k"}},
		Models:    []Model{{Name: "m", Endpoint: "o"}},
		Profiles:  []Profile{{Task: "чат", Models: []string{"m"}}},
	}
	exec := func(ctx context.Context, call ToolCall) ToolResult {
		return ToolResult{ID: call.ID, Content: "42"}
	}
	tools := []Tool{{Name: "stock", Description: "остаток товара", Schema: map[string]any{
		"type": "object", "properties": map[string]any{"товар": map[string]any{"type": "string"}},
	}}}
	r := New(cfg, nil)
	resp, err := r.RunWithTools(context.Background(), "чат",
		ChatRequest{Messages: []Message{UserText("сколько гвоздей?")}}, tools, exec)
	if err != nil {
		t.Fatalf("RunWithTools: %v", err)
	}
	if resp.Text != "Готово" {
		t.Fatalf("неожиданный текст: %q", resp.Text)
	}
	// Инвариант: ответ role=tool на КАЖДЫЙ из 2 tool_call (включая битый).
	if toolMsgCount != 2 {
		t.Fatalf("ожидалось 2 tool-сообщения (по одному на каждый tool_call), получено %d", toolMsgCount)
	}
}
