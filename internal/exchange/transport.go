package exchange

// Онлайн-транспорт (план 86, фаза 2): доставка пакетов между базами по HTTP с
// Bearer-токеном. Приёмные эндпоинты монтирует сервер (internal/ui):
//
//	POST <base>/exchange/<план>/push       — принять и загрузить пакет;
//	GET  <base>/exchange/<план>/pull?to=X  — отдать пакет для узла X.
//
// Здесь только клиент; сам движок (BuildPackage/ApplyPackage) от HTTP не зависит.

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// maxPackageBytes — потолок на размер принимаемого/скачиваемого пакета (защита
// от OOM и «бесконечного» ответа).
var transportClient = &http.Client{Timeout: 60 * time.Second}

func readLimited(r io.Reader) ([]byte, error) {
	b, err := io.ReadAll(io.LimitReader(r, MaxPackageBytes+1))
	if err != nil {
		return nil, err
	}
	if len(b) > MaxPackageBytes {
		return nil, fmt.Errorf("ответ превышает лимит %d байт", MaxPackageBytes)
	}
	return b, nil
}

// endpoint строит адрес эндпоинта обмена: <base>/exchange/<план>/<op>.
func endpoint(baseURL, plan, op string) string {
	return strings.TrimRight(baseURL, "/") + "/exchange/" + url.PathEscape(plan) + "/" + op
}

// PushPackage доставляет пакет на узел (POST .../push) и возвращает итог загрузки.
func PushPackage(ctx context.Context, baseURL, plan, token string, data []byte) (LoadResult, error) {
	if len(data) > MaxPackageBytes {
		return LoadResult{}, fmt.Errorf("push на %s: пакет превышает лимит %d байт", baseURL, MaxPackageBytes)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint(baseURL, plan, "push"), bytes.NewReader(data))
	if err != nil {
		return LoadResult{}, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := transportClient.Do(req)
	if err != nil {
		return LoadResult{}, fmt.Errorf("push на %s: %w", baseURL, err)
	}
	defer resp.Body.Close() //nolint:errcheck,gosec // G104: bodyclose распознаёт только прямой вызов; тело прочитано, закрытие вторично
	body, readErr := readLimited(resp.Body)
	if readErr != nil {
		return LoadResult{}, fmt.Errorf("push на %s: %w", baseURL, readErr)
	}
	if resp.StatusCode != http.StatusOK {
		return LoadResult{}, fmt.Errorf("push на %s: %s: %s", baseURL, resp.Status, strings.TrimSpace(string(body)))
	}
	var lr LoadResult
	if err := json.Unmarshal(body, &lr); err != nil {
		return LoadResult{}, fmt.Errorf("push на %s: разбор ответа: %w", baseURL, err)
	}
	return lr, nil
}

// PullPackage скачивает с узла пакет, адресованный toNode (GET .../pull?to=…).
func PullPackage(ctx context.Context, baseURL, plan, token, toNode string) ([]byte, error) {
	u := endpoint(baseURL, plan, "pull") + "?to=" + url.QueryEscape(toNode)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := transportClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("pull с %s: %w", baseURL, err)
	}
	defer resp.Body.Close() //nolint:errcheck,gosec // G104: bodyclose распознаёт только прямой вызов; тело прочитано, закрытие вторично
	body, readErr := readLimited(resp.Body)
	if readErr != nil {
		return nil, fmt.Errorf("pull с %s: %w", baseURL, readErr)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("pull с %s: %s: %s", baseURL, resp.Status, strings.TrimSpace(string(body)))
	}
	return body, nil
}
