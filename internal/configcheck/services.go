package configcheck

// Валидация HTTP-сервисов (план 61): дубли корневых URL, наличие модуля и
// процедур-обработчиков, согласованность аутентификации (token/hmac требуют
// секрет — поглощено из плана 58).

import (
	"fmt"
	"strings"

	"github.com/ivantit66/onebase/internal/dsl/ast"
	"github.com/ivantit66/onebase/internal/project"
)

// CheckHTTPServiceAuthWarnings — advisory-предупреждения (issue #786): у сервиса
// с ИЗМЕНЯЮЩИМИ методами (POST/PUT/DELETE/PATCH) не задан auth (пустое поле
// молча нормализуется к «none») и нет rate_limit. Это не ошибка — публичный
// сервис бывает осознанным, — но такой endpoint стоит либо защитить, либо
// пометить публичным ЯВНО (auth: none). Явный auth: none предупреждения не
// вызывает.
func CheckHTTPServiceAuthWarnings(proj *project.Project) []Issue {
	var warnings []Issue
	for _, svc := range proj.HTTPServices {
		if svc.AuthExplicit || svc.RateLimit > 0 {
			continue
		}
		mutating := false
		for _, t := range svc.Templates {
			for m := range t.Methods {
				if m == "POST" || m == "PUT" || m == "DELETE" || m == "PATCH" {
					mutating = true
				}
			}
		}
		if !mutating {
			continue
		}
		warnings = append(warnings, Issue{
			File:   "services",
			Object: svc.Name,
			Kind:   "HTTP-сервис",
			Message: "изменяющий endpoint без явного auth и без rate_limit: пустой auth молча " +
				"трактуется как публичный (none). Укажите auth (token/hmac/basic/session) или, " +
				"если сервис действительно публичный, задайте auth: none явно и/или rate_limit.",
		})
	}
	return warnings
}

// forbiddenSecurityExtraHeader — заголовки, которые нельзя переопределять через
// security_headers.extra. Отключаемый nosniff нужен только чтобы навредить себе,
// а CORS задаётся блоком cors: — правка его заголовков вручную разъедется с
// preflight-ответом, который платформа формирует сама.
func forbiddenSecurityExtraHeader(name string) bool {
	n := strings.ToLower(strings.TrimSpace(name))
	return n == "x-content-type-options" || strings.HasPrefix(n, "access-control-")
}

// CheckHTTPServices проверяет services/*.yaml против загруженных модулей.
func CheckHTTPServices(proj *project.Project) []Issue {
	var issues []Issue
	add := func(object, msg string) {
		issues = append(issues, Issue{File: "services", Object: object, Kind: "HTTP-сервис", Message: msg})
	}

	// ServicePrograms ключуется капитализированным именем файла — ищем
	// регистронезависимо. Сервисы хранятся отдельно от Programs (план 61),
	// чтобы не конфликтовать с модулем одноимённого документа.
	progByLower := map[string]*ast.Program{}
	for name, prog := range proj.ServicePrograms {
		progByLower[strings.ToLower(name)] = prog
	}

	seenRoot := map[string]string{}
	for _, svc := range proj.HTTPServices {
		if strings.TrimSpace(svc.Name) == "" {
			add("(без имени)", "не задано имя сервиса (name)")
			continue
		}
		if strings.TrimSpace(svc.RootURL) == "" {
			add(svc.Name, "не задан корневой URL (root_url)")
			continue
		}
		low := strings.ToLower(svc.RootURL)
		if prev, dup := seenRoot[low]; dup {
			add(svc.Name, fmt.Sprintf("корневой URL %q уже занят сервисом %q", svc.RootURL, prev))
		} else {
			seenRoot[low] = svc.Name
		}

		switch svc.Auth {
		case "", "none", "basic", "session":
		case "token", "hmac":
			// Секрет, вынесенный в ${env:VAR}, считается заданным, даже если
			// переменная не экспортирована при линте (HasSecret смотрит на
			// сырое значение) — наличие переменной это забота рантайма.
			if !svc.HasSecret() {
				add(svc.Name, fmt.Sprintf("auth %q требует secret (используйте ${env:VAR})", svc.Auth))
			}
		default:
			add(svc.Name, fmt.Sprintf("неизвестный auth %q (none|basic|session|token|hmac)", svc.Auth))
		}

		// roles требует аутентифицированного пользователя в контексте, а его
		// кладут только basic/session. При none/token/hmac ветка roles в рантайме
		// всегда даёт 403 (UserFromContext==nil) — сервис нерабочий молча.
		if len(svc.Roles) > 0 {
			switch strings.ToLower(strings.TrimSpace(svc.Auth)) {
			case "basic", "session":
			default:
				add(svc.Name, fmt.Sprintf("roles заданы при auth %q — отбор по ролям требует basic или session (иначе всегда 403)", svc.Auth))
			}
		}

		// План 126: кэш ответов. Разрешён только анонимным сервисам — иначе
		// ответ, собранный под правами одного пользователя (RLS, маскирование,
		// роли), достанется другому. Это ошибка конфигурации, а не
		// предупреждение: рантайм такой кэш игнорирует, и владелец останется в
		// уверенности, что кэш работает.
		if svc.Cache != nil {
			if svc.Cache.TTL < 0 {
				add(svc.Name, "cache.ttl не может быть отрицательным")
			}
			if svc.Cache.TTL > 0 && !svc.CacheUsable() {
				add(svc.Name, fmt.Sprintf("cache задан при auth %q — кэш допустим только при auth: none, "+
					"иначе ответ одного пользователя достанется другому. Вынесите публичную часть в отдельный сервис", svc.Auth))
			}
			for _, v := range svc.Cache.Vary {
				switch strings.ToLower(strings.TrimSpace(v)) {
				case "query", "host", "lang":
				default:
					add(svc.Name, fmt.Sprintf("cache.vary: неизвестное значение %q (допустимы query, host, lang)", v))
				}
			}
			hasGet := false
			for _, t := range svc.Templates {
				for m := range t.Methods {
					if m == "GET" || m == "HEAD" {
						hasGet = true
					}
				}
			}
			if svc.Cache.TTL > 0 && !hasGet {
				add(svc.Name, "cache задан, но у сервиса нет ни одного GET/HEAD-метода — кэшируются только они")
			}
		}

		// План 128: заголовки безопасности уровня сервиса.
		if h := svc.SecurityHeaders; h != nil {
			switch h.FrameOptions {
			case "", "DENY", "SAMEORIGIN":
			default:
				add(svc.Name, fmt.Sprintf("security_headers.frame_options %q — допустимы DENY, SAMEORIGIN или пусто", h.FrameOptions))
			}
			if h.HSTS < 0 {
				add(svc.Name, "security_headers.hsts не может быть отрицательным")
			}
			for name := range h.Extra {
				if forbiddenSecurityExtraHeader(name) {
					add(svc.Name, fmt.Sprintf("security_headers.extra: заголовок %q задавать нельзя "+
						"(X-Content-Type-Options ставится всегда, Access-Control-* — это блок cors)", name))
				}
			}
		}

		prog, ok := progByLower[strings.ToLower(svc.Name)]
		if !ok {
			add(svc.Name, fmt.Sprintf("не найден модуль обработчиков src/%s.service.os", strings.ToLower(svc.Name)))
			continue
		}
		procs := map[string]bool{}
		for _, p := range prog.Procedures {
			procs[strings.ToLower(p.Name.Literal)] = true
		}
		for _, t := range svc.Templates {
			for method, handler := range t.Methods {
				if handler == "" || !procs[strings.ToLower(handler)] {
					add(svc.Name, fmt.Sprintf("шаблон %q (%s): обработчик %q не найден в src/%s.service.os",
						t.Template, method, handler, strings.ToLower(svc.Name)))
				}
			}
		}
	}
	return issues
}
