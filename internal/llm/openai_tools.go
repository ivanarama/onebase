// internal/llm/openai_tools.go
package llm

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"regexp"
	"strconv"
	"strings"
)

var openAIFunctionName = regexp.MustCompile(`^[A-Za-z0-9_-]{1,64}$`)

// OpenAI-совместимые API ограничивают имена функций. Исполнитель и журнал
// используют имена OneBase, на которые ссылаются инструкции конфигуратора.
func openAIToolDefinitions(tools []Tool) ([]map[string]any, map[string]string) {
	logicalNames := make(map[string]string, len(tools))
	for _, tool := range tools {
		if openAIFunctionName.MatchString(tool.Name) {
			logicalNames[tool.Name] = tool.Name
		}
	}
	defs := make([]map[string]any, 0, len(tools))
	nextAlias := 1
	for _, tool := range tools {
		name := tool.Name
		description := tool.Description
		if !openAIFunctionName.MatchString(name) {
			for {
				name = "onebase_tool_" + strconv.Itoa(nextAlias)
				nextAlias++
				if _, reserved := logicalNames[name]; !reserved {
					break
				}
			}
			logicalNames[name] = tool.Name
			description += fmt.Sprintf("\nВ инструкциях OneBase «%s» означает вызов %s.", tool.Name, name)
		}
		schema := tool.Schema
		if schema == nil {
			schema = map[string]any{"type": "object", "properties": map[string]any{}}
		}
		defs = append(defs, map[string]any{
			"type": "function",
			"function": map[string]any{
				"name": name, "description": description, "parameters": schema,
			},
		})
	}
	return defs, logicalNames
}

// completeOpenAITools — агентный цикл tool-use по OpenAI chat/completions
// (покрывает OpenAI и openai-совместимые). Изображения в tool-путь не передаются
// (как и в anthropic-цикле). Аргументы инструмента у OpenAI приходят JSON-строкой.
func completeOpenAITools(ctx context.Context, hc *http.Client, rm ResolvedModel, req ChatRequest, tools []Tool, exec ToolExecutor, maxRounds int) (ChatResponse, error) {
	base := rm.Endpoint.BaseURL
	if base == "" {
		base = "https://api.openai.com/v1"
	}
	url := strings.TrimRight(base, "/") + "/chat/completions"
	headers := map[string]string{"Authorization": "Bearer " + rm.Endpoint.APIKey}

	toolDefs, logicalNames := openAIToolDefinitions(tools)

	messages := make([]map[string]any, 0, len(req.Messages)+4)
	if req.System != "" {
		messages = append(messages, map[string]any{"role": "system", "content": req.System})
	}
	for _, m := range req.Messages {
		var sb strings.Builder
		for _, p := range m.Parts {
			if !p.isImage() {
				sb.WriteString(p.Text)
			}
		}
		role := "user"
		if m.Role == RoleAssistant {
			role = "assistant"
		}
		messages = append(messages, map[string]any{"role": role, "content": sb.String()})
	}

	var totalIn, totalOut int
	for iter := 0; iter < maxRounds; iter++ {
		body := map[string]any{
			"model":      rm.Model.Name,
			"max_tokens": maxTokens(rm.Model, req),
			"messages":   messages,
			"tools":      toolDefs,
		}
		data, err := postJSON(ctx, hc, "openai", url, body, headers, rm.Endpoint.Headers)
		if err != nil {
			return ChatResponse{}, err
		}
		var out struct {
			Choices []struct {
				Message struct {
					Content          string            `json:"content"`
					ReasoningContent *string           `json:"reasoning_content"`
					ToolCalls        []json.RawMessage `json:"tool_calls"`
				} `json:"message"`
				FinishReason string `json:"finish_reason"`
			} `json:"choices"`
			Usage struct {
				PromptTokens     int `json:"prompt_tokens"`
				CompletionTokens int `json:"completion_tokens"`
			} `json:"usage"`
		}
		if err := json.Unmarshal(data, &out); err != nil {
			return ChatResponse{}, fmt.Errorf("openai: разбор ответа: %w", err)
		}
		totalIn += out.Usage.PromptTokens
		totalOut += out.Usage.CompletionTokens
		if len(out.Choices) == 0 {
			return ChatResponse{}, fmt.Errorf("openai: пустой ответ (нет choices)")
		}
		ch := out.Choices[0]
		// Исполняем инструменты при наличии tool_calls независимо от finish_reason:
		// OpenAI-совместимые провайдеры (Ollama, LM Studio, прокси) нередко ставят
		// finish_reason:"stop" даже с заполненным tool_calls — привязка к
		// "tool_calls" молча проглатывала бы запрос модели на инструмент.
		if len(ch.Message.ToolCalls) == 0 {
			return ChatResponse{Text: ch.Message.Content, Model: rm.Model.Name, InputTokens: totalIn, OutputTokens: totalOut}, nil
		}

		// Ход ассистента с tool_calls возвращаем модели как есть.
		assistant := map[string]any{
			"role": "assistant", "content": ch.Message.Content, "tool_calls": ch.Message.ToolCalls,
		}
		// DeepSeek требует вернуть контекст рассуждения в следующий раунд с tools.
		if ch.Message.ReasoningContent != nil {
			assistant["reasoning_content"] = *ch.Message.ReasoningContent
		}
		messages = append(messages, assistant)
		for _, raw := range ch.Message.ToolCalls {
			var tc struct {
				ID       string `json:"id"`
				Function struct {
					Name      string `json:"name"`
					Arguments string `json:"arguments"`
				} `json:"function"`
			}
			if err := json.Unmarshal(raw, &tc); err != nil {
				// Инвариант OpenAI: ровно один ответ role=tool на каждый tool_call —
				// иначе следующий POST вернёт 400. Раньше continue пропускал битый
				// вызов, нарушая инвариант. Пытаемся достать id отдельно (структура
				// могла сломаться лишь на function/arguments) и всё равно отвечаем
				// сообщением об ошибке.
				var idOnly struct {
					ID string `json:"id"`
				}
				_ = json.Unmarshal(raw, &idOnly)
				messages = append(messages, map[string]any{
					"role": "tool", "tool_call_id": idOnly.ID,
					"content": "ошибка разбора вызова инструмента: " + err.Error(),
				})
				continue
			}
			logicalName, ok := logicalNames[tc.Function.Name]
			if !ok {
				messages = append(messages, map[string]any{
					"role": "tool", "tool_call_id": tc.ID,
					"content": "неизвестный инструмент: " + tc.Function.Name,
				})
				continue
			}
			var input map[string]any
			if tc.Function.Arguments != "" {
				_ = json.Unmarshal([]byte(tc.Function.Arguments), &input)
			}
			res := exec(ctx, ToolCall{ID: tc.ID, Name: logicalName, Input: input})
			messages = append(messages, map[string]any{
				"role": "tool", "tool_call_id": tc.ID, "content": res.Content,
			})
		}
	}
	return ChatResponse{}, fmt.Errorf("openai: превышен лимит раундов инструментов (%d)", maxRounds)
}
