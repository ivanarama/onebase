package configcheck

// Тесты валидации HTTP-сервисов (план 61).

import (
	"strings"
	"testing"

	"github.com/ivantit66/onebase/internal/dsl/ast"
	"github.com/ivantit66/onebase/internal/dsl/lexer"
	"github.com/ivantit66/onebase/internal/dsl/parser"
	"github.com/ivantit66/onebase/internal/httpservice"
	"github.com/ivantit66/onebase/internal/project"
)

func parseServiceProg(t *testing.T, src string) *ast.Program {
	t.Helper()
	prog, err := parser.New(lexer.New(src, "test.service.os")).ParseProgram()
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	return prog
}

func TestCheckHTTPServices(t *testing.T) {
	good := parseServiceProg(t, `Функция ПолучитьЗаказ(Запрос) Экспорт
  Возврат "ok";
КонецФункции`)

	mk := func(name, root, auth, secret string, tmpl httpservice.URLTemplate) *httpservice.Service {
		s := &httpservice.Service{Name: name, RootURL: root, Auth: auth, Secret: secret,
			Templates: []httpservice.URLTemplate{tmpl}}
		s.Normalize()
		return s
	}
	okTmpl := httpservice.URLTemplate{Template: "/{id}", Methods: map[string]string{"GET": "ПолучитьЗаказ"}}
	badTmpl := httpservice.URLTemplate{Template: "/{id}", Methods: map[string]string{"GET": "НетТакого"}}

	proj := &project.Project{
		HTTPServices: []*httpservice.Service{
			mk("Заказы", "orders", "none", "", okTmpl),
			mk("Дубль", "orders", "none", "", okTmpl),       // дубль root_url
			mk("БезМодуля", "nomod", "none", "", okTmpl),    // нет src-модуля
			mk("Заказы2", "orders2", "none", "", badTmpl),   // нет процедуры
			mk("БезСекрета", "tok", "token", "", okTmpl),    // token без секрета
			mk("Странный", "weird", "странный", "", okTmpl), // неизвестный auth
		},
		ServicePrograms: map[string]*ast.Program{
			"Заказы":     good,
			"Дубль":      good,
			"Заказы2":    good,
			"БезСекрета": good,
			"Странный":   good,
		},
	}

	issues := CheckHTTPServices(proj)
	for _, want := range []string{
		"уже занят",
		"не найден модуль",
		"не найден в src/заказы2.service.os",
		"требует secret",
		"неизвестный auth",
	} {
		found := false
		for _, is := range issues {
			if strings.Contains(is.Message, want) {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("не найдена ожидаемая ошибка %q среди: %+v", want, issues)
		}
	}
	if len(issues) != 5 {
		t.Errorf("ожидалось ровно 5 ошибок, получено %d: %+v", len(issues), issues)
	}
}

// Секрет, вынесенный в ${env:VAR}, считается ЗАДАННЫМ даже если переменная не
// экспортирована в момент onebase check: загрузчик раскрывает ${env:…} в пустую
// строку, но валидатор смотрит на сырое (до раскрытия) значение. Иначе
// идиоматически правильный конфиг (секрет в окружении, не в git) не проходит
// собственный линтер — план 75: examples/callcenter с auth hmac должен
// проходить check без TELEPHONY_SECRET в среде.
func TestCheckHTTPServices_EnvSourcedSecretIsConfigured(t *testing.T) {
	good := parseServiceProg(t, `Функция Событие(Запрос) Экспорт
  Возврат "ok";
КонецФункции`)
	tmpl := httpservice.URLTemplate{Template: "/event", Methods: map[string]string{"POST": "Событие"}}

	svc := &httpservice.Service{Name: "Тел", RootURL: "tel", Auth: "hmac",
		Secret: "${env:TELE_TEST_SECRET}", Templates: []httpservice.URLTemplate{tmpl}}
	svc.Normalize() // сохраняет сырой секрет (${env:…}) до раскрытия
	svc.Secret = "" // имитируем раскрытие незаданной переменной загрузчиком

	proj := &project.Project{
		HTTPServices:    []*httpservice.Service{svc},
		ServicePrograms: map[string]*ast.Program{"Тел": good},
	}

	for _, is := range CheckHTTPServices(proj) {
		if strings.Contains(is.Message, "требует secret") {
			t.Errorf("env-секрет ошибочно помечен отсутствующим: %+v", is)
		}
	}
}

// План 128: гейт на блок security_headers.
func TestCheckHTTPServices_SecurityHeaders(t *testing.T) {
	prog := parseServiceProg(t, `Функция Главная(Запрос) Экспорт
  Возврат "ok";
КонецФункции`)
	tmpl := httpservice.URLTemplate{Template: "/", Methods: map[string]string{"GET": "Главная"}}

	mk := func(h *httpservice.SecurityHeadersConfig) *project.Project {
		s := &httpservice.Service{Name: "Site", RootURL: "site", Auth: "none",
			SecurityHeaders: h, Templates: []httpservice.URLTemplate{tmpl}}
		s.Normalize()
		return &project.Project{
			HTTPServices:    []*httpservice.Service{s},
			ServicePrograms: map[string]*ast.Program{"Site": prog},
		}
	}

	t.Run("корректный блок проходит", func(t *testing.T) {
		issues := CheckHTTPServices(mk(&httpservice.SecurityHeadersConfig{
			CSP: "default-src 'self'", FrameOptions: "SAMEORIGIN", HSTS: 3600,
		}))
		if len(issues) != 0 {
			t.Fatalf("неожиданные замечания: %v", issues)
		}
	})

	t.Run("неизвестный frame_options", func(t *testing.T) {
		issues := CheckHTTPServices(mk(&httpservice.SecurityHeadersConfig{FrameOptions: "ALLOWALL"}))
		if len(issues) != 1 || !strings.Contains(issues[0].Message, "frame_options") {
			t.Fatalf("ожидалось замечание про frame_options, получено: %v", issues)
		}
	})

	t.Run("отрицательный hsts", func(t *testing.T) {
		issues := CheckHTTPServices(mk(&httpservice.SecurityHeadersConfig{HSTS: -1}))
		if len(issues) != 1 || !strings.Contains(issues[0].Message, "hsts") {
			t.Fatalf("ожидалось замечание про hsts, получено: %v", issues)
		}
	})

	// nosniff и CORS через extra не задаются: первый отключать незачем, второй
	// живёт блоком cors: и разъедется с preflight-ответом платформы.
	t.Run("запрещённые заголовки в extra", func(t *testing.T) {
		issues := CheckHTTPServices(mk(&httpservice.SecurityHeadersConfig{
			Extra: map[string]string{"X-Content-Type-Options": "none"},
		}))
		if len(issues) != 1 || !strings.Contains(issues[0].Message, "extra") {
			t.Fatalf("ожидалось замечание про extra, получено: %v", issues)
		}
		issues = CheckHTTPServices(mk(&httpservice.SecurityHeadersConfig{
			Extra: map[string]string{"Access-Control-Allow-Origin": "*"},
		}))
		if len(issues) != 1 {
			t.Fatalf("ожидалось замечание про Access-Control-*, получено: %v", issues)
		}
	})
}

// План 126: гейт на блок cache.
func TestCheckHTTPServices_Cache(t *testing.T) {
	prog := parseServiceProg(t, `Функция Главная(Запрос) Экспорт
  Возврат "ok";
КонецФункции`)
	getTmpl := httpservice.URLTemplate{Template: "/", Methods: map[string]string{"GET": "Главная"}}
	postTmpl := httpservice.URLTemplate{Template: "/", Methods: map[string]string{"POST": "Главная"}}

	mk := func(auth string, cache *httpservice.CacheConfig, tmpl httpservice.URLTemplate) *project.Project {
		s := &httpservice.Service{Name: "Site", RootURL: "site", Auth: auth,
			Cache: cache, Templates: []httpservice.URLTemplate{tmpl}}
		s.Normalize()
		return &project.Project{
			HTTPServices:    []*httpservice.Service{s},
			ServicePrograms: map[string]*ast.Program{"Site": prog},
		}
	}

	t.Run("корректный блок проходит", func(t *testing.T) {
		issues := CheckHTTPServices(mk("none", &httpservice.CacheConfig{TTL: 60, Vary: []string{"query", "host"}}, getTmpl))
		if len(issues) != 0 {
			t.Fatalf("неожиданные замечания: %v", issues)
		}
	})

	// Главный гейт плана: кэш ответа при auth ≠ none раздал бы ответ одного
	// пользователя другому.
	t.Run("кэш при auth basic — ошибка", func(t *testing.T) {
		issues := CheckHTTPServices(mk("basic", &httpservice.CacheConfig{TTL: 60}, getTmpl))
		if len(issues) != 1 || !strings.Contains(issues[0].Message, "auth: none") {
			t.Fatalf("ожидалось замечание про auth: none, получено: %v", issues)
		}
	})

	t.Run("неизвестное значение vary", func(t *testing.T) {
		issues := CheckHTTPServices(mk("none", &httpservice.CacheConfig{TTL: 60, Vary: []string{"cookie"}}, getTmpl))
		if len(issues) != 1 || !strings.Contains(issues[0].Message, "vary") {
			t.Fatalf("ожидалось замечание про vary, получено: %v", issues)
		}
	})

	t.Run("отрицательный ttl", func(t *testing.T) {
		issues := CheckHTTPServices(mk("none", &httpservice.CacheConfig{TTL: -5}, getTmpl))
		if len(issues) != 1 || !strings.Contains(issues[0].Message, "ttl") {
			t.Fatalf("ожидалось замечание про ttl, получено: %v", issues)
		}
	})

	// Кэшируются только GET/HEAD — на сервисе без них блок cache бесполезен.
	t.Run("кэш без GET-методов", func(t *testing.T) {
		issues := CheckHTTPServices(mk("none", &httpservice.CacheConfig{TTL: 60}, postTmpl))
		if len(issues) != 1 || !strings.Contains(issues[0].Message, "GET") {
			t.Fatalf("ожидалось замечание про GET/HEAD, получено: %v", issues)
		}
	})
}
