package ui

// Генерация OpenAPI 3.0 спецификации из метаданных HTTP-сервисов (план 61).
// Спека собирается вручную как дерево map'ов — без внешних зависимостей; этого
// достаточно для импорта в Swagger UI / Postman / кодогенераторы клиентов.

import (
	"net/http"
	"strings"

	"github.com/ivantit66/onebase/internal/httpservice"
)

// serviceOpenAPI — GET /hs/openapi.json.
func (s *Server) serviceOpenAPI(w http.ResponseWriter, r *http.Request) {
	doc := buildOpenAPI(s.reg.HTTPServices(), s.cfg.AppName, s.cfg.AppVersion)
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	respondJSONTo(w, doc)
}

func buildOpenAPI(services []*httpservice.Service, title, version string) map[string]any {
	if title == "" {
		title = "onebase HTTP services"
	}
	if version == "" {
		version = "1.0.0"
	}

	paths := map[string]any{}
	usesBasic, usesSession := false, false

	for _, svc := range services {
		for _, t := range svc.Templates {
			oapiPath := "/" + svc.RootURL + oapiTemplatePath(t.Template)
			item, _ := paths[oapiPath].(map[string]any)
			if item == nil {
				item = map[string]any{}
			}
			params := oapiPathParams(t.Template)
			for method, handler := range t.Methods {
				op := map[string]any{
					"operationId": svc.Name + "_" + handler,
					"summary":     handler,
					"tags":        []string{svc.Name},
					"responses": map[string]any{
						"200": map[string]any{"description": "OK"},
					},
				}
				if len(params) > 0 {
					op["parameters"] = params
				}
				switch svc.Auth {
				case "basic":
					op["security"] = []any{map[string]any{"basicAuth": []any{}}}
					usesBasic = true
				case "session":
					op["security"] = []any{map[string]any{"sessionCookie": []any{}}}
					usesSession = true
				}
				if method == http.MethodPost || method == http.MethodPut || method == http.MethodPatch {
					op["requestBody"] = map[string]any{
						"required": false,
						"content": map[string]any{
							"application/json": map[string]any{
								"schema": map[string]any{"type": "object"},
							},
						},
					}
				}
				item[strings.ToLower(method)] = op
			}
			paths[oapiPath] = item
		}
	}

	doc := map[string]any{
		"openapi": "3.0.3",
		"info":    map[string]any{"title": title, "version": version},
		"servers": []any{map[string]any{"url": "/hs"}},
		"paths":   paths,
	}

	schemes := map[string]any{}
	if usesBasic {
		schemes["basicAuth"] = map[string]any{"type": "http", "scheme": "basic"}
	}
	if usesSession {
		schemes["sessionCookie"] = map[string]any{"type": "apiKey", "in": "cookie", "name": "onebase_session"}
	}
	if len(schemes) > 0 {
		doc["components"] = map[string]any{"securitySchemes": schemes}
	}
	return doc
}

// oapiTemplatePath приводит шаблон onebase к синтаксису пути OpenAPI: жадный
// «{*путь}» становится обычным «{путь}» (в OpenAPI нет greedy-параметров).
func oapiTemplatePath(template string) string {
	if !strings.Contains(template, "{*") {
		return template
	}
	return strings.ReplaceAll(template, "{*", "{")
}

// oapiPathParams извлекает path-параметры (строкового типа) из шаблона.
func oapiPathParams(template string) []any {
	var params []any
	for _, seg := range strings.Split(template, "/") {
		if !strings.HasPrefix(seg, "{") || !strings.HasSuffix(seg, "}") {
			continue
		}
		name := strings.TrimSuffix(strings.TrimPrefix(seg, "{"), "}")
		name = strings.TrimPrefix(name, "*")
		params = append(params, map[string]any{
			"name":     name,
			"in":       "path",
			"required": true,
			"schema":   map[string]any{"type": "string"},
		})
	}
	return params
}
