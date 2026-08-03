package llm

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
)

const maxRespBytes = 8 << 20 // 8 MiB — потолок тела ответа провайдера (защита от OOM)

// postJSON отправляет body как JSON на url и возвращает тело ответа. Не-2xx
// статусы возвращаются как *APIError (его классифицирует фолбэк-движок).
// extraHeaders дополняют/перекрывают базовые; headers endpoint'а применяются поверх.
func postJSON(ctx context.Context, hc *http.Client, provider, url string, body any, headers map[string]string, epHeaders map[string]string) ([]byte, error) {
	raw, err := json.Marshal(body)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(raw))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	for k, v := range epHeaders {
		req.Header.Set(k, v)
	}
	resp, err := hc.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close() //nolint:errcheck,gosec // G104: bodyclose распознаёт только прямой вызов; тело прочитано, закрытие вторично
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxRespBytes))
	if err != nil {
		return nil, fmt.Errorf("%s: чтение ответа: %w", provider, err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, &APIError{StatusCode: resp.StatusCode, Provider: provider, Body: string(data)}
	}
	return data, nil
}
