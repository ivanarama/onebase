// Package httpservice описывает HTTP-сервисы конфигурации onebase — аналог
// объекта «HTTPСервис» в 1С. Разработчик публикует собственные REST-эндпоинты,
// обработчики которых пишутся на встроенном языке (DSL). Этот пакет — чистые
// метаданные (YAML-загрузчик + сопоставление URL-шаблонов); исполнение
// обработчиков и маршрутизация живут в internal/ui (рядом с обработками), а
// объекты запроса/ответа — в internal/dsl/interpreter.
//
// Структура каталога проекта:
//
//	services/<имя>.yaml      — метаданные сервиса (корневой URL, шаблоны, методы)
//	src/<имя>.service.os     — DSL-обработчики (Функция ИмяОбработчика(Запрос) Экспорт …)
package httpservice

import (
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

// URLTemplate — один шаблон ресурса внутри сервиса. Аналог «Шаблона URL» 1С.
// Шаблон может содержать сегменты-параметры в фигурных скобках: «/{id}»,
// «/orders/{id}/items». Жадный «хвостовой» параметр «{*путь}» захватывает все
// оставшиеся сегменты (удобно для проксирующих сервисов).
type URLTemplate struct {
	Template string `yaml:"template"`
	// Methods: HTTP-метод (GET/POST/PUT/DELETE/PATCH/…) → имя процедуры-обработчика
	// в соответствующем .service.os. Ключи нормализуются в верхний регистр.
	Methods map[string]string `yaml:"methods"`
}

// Service — опубликованный HTTP-сервис. Монтируется по адресу /hs/<RootURL>/…
// (префикс /hs/ повторяет соглашение 1С для http-сервисов).
type Service struct {
	Name    string            `yaml:"name"`
	Title   string            `yaml:"title"`
	Titles  map[string]string `yaml:"titles"`
	RootURL string            `yaml:"root_url"`
	// Auth — способ аутентификации: «none» (аноним, по умолчанию — удобно для
	// приёма вебхуков), «basic» (HTTP Basic против пользователей базы),
	// «session» (cookie/токен, как у веб-интерфейса), «token» (постоянный
	// секрет в заголовке X-Webhook-Token) или «hmac» (подпись тела:
	// X-Webhook-Signature = hex(HMAC-SHA256(тело, secret)) — формат платёжек
	// и Telegram). token/hmac поглощены из плана 58.
	Auth string `yaml:"auth"`
	// Secret — секрет для auth token/hmac. Задавайте через ${env:VAR} —
	// значение живёт в окружении, не в YAML/git/.obz.
	Secret string `yaml:"secret"`
	// SecretRaw — секрет в исходном виде, ДО раскрытия ${env:VAR} загрузчиком.
	// Захватывается в Normalize, не читается из YAML. Нужен валидатору
	// (onebase check), чтобы отличить «секрет не задан» от «секрет вынесен в
	// окружение»: незаданная при линте переменная даёт пустой Secret, но
	// конфигурация при этом корректна — наличие переменной это забота рантайма.
	SecretRaw string `yaml:"-"`
	// RateLimit — максимум запросов в минуту на сервис; 0 = без лимита.
	RateLimit int `yaml:"rate_limit"`
	// Roles — если непусто, вызов разрешён только аутентифицированному
	// пользователю с одной из перечисленных ролей (администратор — всегда).
	// Подразумевает auth basic/session: анонимный вызов отклоняется (403).
	Roles []string `yaml:"roles"`
	// CORS — необязательная политика CORS уровня сервиса (для браузерных клиентов).
	CORS      *CORSConfig   `yaml:"cors"`
	Templates []URLTemplate `yaml:"templates"`
}

// CORSConfig — политика Cross-Origin Resource Sharing для сервиса.
type CORSConfig struct {
	Origins     []string `yaml:"origins"`     // ["*"] или конкретные источники
	Headers     []string `yaml:"headers"`     // разрешённые заголовки запроса (preflight)
	Credentials bool     `yaml:"credentials"` // разрешить cookie/Authorization
	MaxAge      int      `yaml:"max_age"`     // кэш preflight, секунд
}

// DisplayName возвращает заголовок сервиса с учётом языка.
func (s *Service) DisplayName(lang string) string {
	if lang != "" {
		if v, ok := s.Titles[lang]; ok && v != "" {
			return v
		}
	}
	if s.Title != "" {
		return s.Title
	}
	return s.Name
}

// Normalize приводит сервис к каноничному виду: корневой URL без ведущих/
// замыкающих слэшей, методы в верхнем регистре, дефолтная аутентификация.
// Вызывается загрузчиком; экспортирован для программного построения сервисов.
func (s *Service) Normalize() {
	// Сохраняем сырой секрет один раз — до того, как загрузчик раскроет
	// ${env:VAR}. Захват один раз (по пустому SecretRaw) защищает от затирания,
	// если Normalize вызовут повторно уже после раскрытия.
	if s.SecretRaw == "" {
		s.SecretRaw = s.Secret
	}
	s.RootURL = strings.Trim(strings.TrimSpace(s.RootURL), "/")
	if s.Auth == "" {
		s.Auth = "none"
	}
	s.Auth = strings.ToLower(strings.TrimSpace(s.Auth))
	for i := range s.Templates {
		t := &s.Templates[i]
		t.Template = "/" + strings.Trim(strings.TrimSpace(t.Template), "/")
		up := make(map[string]string, len(t.Methods))
		for m, handler := range t.Methods {
			up[strings.ToUpper(strings.TrimSpace(m))] = handler
		}
		t.Methods = up
	}
}

// HasSecret сообщает, сконфигурирован ли секрет для auth token/hmac. Смотрит на
// СЫРОЕ значение (SecretRaw, до раскрытия ${env:VAR}), поэтому секрет, вынесенный
// в окружение, считается заданным даже если переменная не экспортирована в
// момент проверки (onebase check). Запасной взгляд на Secret покрывает сервисы,
// собранные программно без Normalize.
func (s *Service) HasSecret() bool {
	return strings.TrimSpace(s.SecretRaw) != "" || strings.TrimSpace(s.Secret) != ""
}

// Match сопоставляет путь ресурса (часть URL ПОСЛЕ корня сервиса, например
// «/42/items») с шаблонами сервиса. Возвращает найденный шаблон, карту
// извлечённых параметров пути и признак успеха. Литеральные сегменты
// сравниваются регистронезависимо; «{имя}» захватывает один сегмент; «{*имя}»
// (только последним) — все оставшиеся сегменты как одну строку.
func (s *Service) Match(resourcePath string) (*URLTemplate, map[string]string, bool) {
	reqSegs := splitPath(resourcePath)
	for i := range s.Templates {
		t := &s.Templates[i]
		if params, ok := matchSegments(splitPath(t.Template), reqSegs); ok {
			return t, params, true
		}
	}
	return nil, nil, false
}

// splitPath разбивает путь на непустые сегменты. «/» → [], «/a/b» → [a b].
func splitPath(p string) []string {
	p = strings.Trim(p, "/")
	if p == "" {
		return nil
	}
	return strings.Split(p, "/")
}

func matchSegments(tmpl, req []string) (map[string]string, bool) {
	params := map[string]string{}
	for ti := 0; ti < len(tmpl); ti++ {
		seg := tmpl[ti]
		// Жадный хвостовой параметр {*имя}: должен быть последним в шаблоне.
		if strings.HasPrefix(seg, "{*") && strings.HasSuffix(seg, "}") {
			name := strings.TrimSuffix(strings.TrimPrefix(seg, "{*"), "}")
			params[name] = strings.Join(req[ti:], "/")
			return params, true
		}
		if ti >= len(req) {
			return nil, false
		}
		if strings.HasPrefix(seg, "{") && strings.HasSuffix(seg, "}") {
			name := strings.TrimSuffix(strings.TrimPrefix(seg, "{"), "}")
			params[name] = req[ti]
			continue
		}
		if !strings.EqualFold(seg, req[ti]) {
			return nil, false
		}
	}
	if len(req) != len(tmpl) {
		return nil, false
	}
	return params, true
}

func LoadFile(path string) (*Service, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var svc Service
	if err := yaml.Unmarshal(data, &svc); err != nil {
		return nil, err
	}
	svc.Normalize()
	return &svc, nil
}

func LoadDir(dir string) ([]*Service, error) {
	items, err := os.ReadDir(dir)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var services []*Service
	for _, item := range items {
		if item.IsDir() || !strings.HasSuffix(item.Name(), ".yaml") {
			continue
		}
		svc, err := LoadFile(filepath.Join(dir, item.Name()))
		if err != nil {
			return nil, err
		}
		services = append(services, svc)
	}
	return services, nil
}
