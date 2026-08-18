package httpservice

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestMatch(t *testing.T) {
	svc := &Service{
		Name:    "API",
		RootURL: "api",
		Templates: []URLTemplate{
			{Template: "/", Methods: map[string]string{"GET": "Корень"}},
			{Template: "/orders/{id}", Methods: map[string]string{"GET": "Заказ"}},
			{Template: "/orders/{id}/items", Methods: map[string]string{"GET": "Позиции"}},
			{Template: "/files/{*path}", Methods: map[string]string{"GET": "Файл"}},
		},
	}
	svc.Normalize()

	cases := []struct {
		path       string
		wantTmpl   string
		wantParams map[string]string
		wantOK     bool
	}{
		{"/", "/", map[string]string{}, true},
		{"/orders/42", "/orders/{id}", map[string]string{"id": "42"}, true},
		{"/orders/42/items", "/orders/{id}/items", map[string]string{"id": "42"}, true},
		{"/files/a/b/c.txt", "/files/{*path}", map[string]string{"path": "a/b/c.txt"}, true},
		{"/orders", "", nil, false},          // нет шаблона /orders
		{"/orders/42/extra", "", nil, false}, // лишний сегмент
	}
	for _, c := range cases {
		tmpl, params, ok := svc.Match(c.path)
		if ok != c.wantOK {
			t.Errorf("Match(%q) ok=%v, want %v", c.path, ok, c.wantOK)
			continue
		}
		if !ok {
			continue
		}
		if tmpl.Template != c.wantTmpl {
			t.Errorf("Match(%q) tmpl=%q, want %q", c.path, tmpl.Template, c.wantTmpl)
		}
		if !reflect.DeepEqual(params, c.wantParams) {
			t.Errorf("Match(%q) params=%v, want %v", c.path, params, c.wantParams)
		}
	}
}

func TestLoadDir_Normalizes(t *testing.T) {
	dir := t.TempDir()
	yaml := `name: ЗаказыAPI
title: Заказы
root_url: /orders/
templates:
  - template: "{id}"
    methods:
      get: Получить
      post: Создать
`
	if err := os.WriteFile(filepath.Join(dir, "orders.yaml"), []byte(yaml), 0o644); err != nil {
		t.Fatal(err)
	}
	services, err := LoadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(services) != 1 {
		t.Fatalf("want 1 service, got %d", len(services))
	}
	svc := services[0]
	if svc.RootURL != "orders" {
		t.Errorf("RootURL=%q, want %q", svc.RootURL, "orders")
	}
	if svc.Auth != "none" {
		t.Errorf("Auth=%q, want default none", svc.Auth)
	}
	if got := svc.Templates[0].Template; got != "/{id}" {
		t.Errorf("template normalized=%q, want /{id}", got)
	}
	if _, ok := svc.Templates[0].Methods["GET"]; !ok {
		t.Errorf("method GET not uppercased: %v", svc.Templates[0].Methods)
	}
	if _, ok := svc.Templates[0].Methods["POST"]; !ok {
		t.Errorf("method POST not uppercased: %v", svc.Templates[0].Methods)
	}
}

func TestLoadDir_Missing(t *testing.T) {
	services, err := LoadDir(filepath.Join(t.TempDir(), "nope"))
	if err != nil {
		t.Fatalf("missing dir should be nil error, got %v", err)
	}
	if services != nil {
		t.Errorf("want nil services, got %v", services)
	}
}

// План 128: сжатие и заголовки безопасности уровня сервиса.
func TestLoadFile_CompressAndSecurityHeaders(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "site.yaml")
	yaml := `name: Site
root_url: site
auth: none
compress: false
security_headers:
  csp: "default-src 'self'"
  frame_options: deny
  referrer_policy: no-referrer
  hsts: 15552000
  extra:
    Permissions-Policy: "geolocation=()"
templates:
  - template: /
    methods:
      get: Главная
`
	if err := os.WriteFile(path, []byte(yaml), 0o600); err != nil {
		t.Fatal(err)
	}
	svc, err := LoadFile(path)
	if err != nil {
		t.Fatalf("LoadFile: %v", err)
	}
	if svc.Compress == nil || *svc.Compress {
		t.Errorf("compress: false не прочитан (%v)", svc.Compress)
	}
	if svc.CompressEnabled() {
		t.Errorf("явный compress: false должен перекрывать умолчание для auth: none")
	}
	h := svc.SecurityHeaders
	if h == nil {
		t.Fatal("security_headers не прочитаны")
	}
	if h.FrameOptions != "DENY" {
		t.Errorf("frame_options=%q, ожидался нормализованный DENY", h.FrameOptions)
	}
	if h.CSP != "default-src 'self'" || h.ReferrerPolicy != "no-referrer" || h.HSTS != 15552000 {
		t.Errorf("прочитано неверно: %+v", h)
	}
	if h.Extra["Permissions-Policy"] != "geolocation=()" {
		t.Errorf("extra=%v", h.Extra)
	}
}

// Умолчание сжатия зависит от аутентификации: у анонимного сервиса секретов
// нет, у остальных сжатие включается только явно (BREACH).
func TestCompressEnabled_DefaultsByAuth(t *testing.T) {
	cases := []struct {
		auth string
		want bool
	}{
		{"", true}, {"none", true}, {"basic", false}, {"session", false}, {"token", false}, {"hmac", false},
	}
	for _, c := range cases {
		svc := &Service{Name: "S", RootURL: "s", Auth: c.auth}
		svc.Normalize()
		if got := svc.CompressEnabled(); got != c.want {
			t.Errorf("auth=%q: CompressEnabled()=%v, ожидалось %v", c.auth, got, c.want)
		}
	}
	yes := true
	svc := &Service{Name: "S", RootURL: "s", Auth: "basic", Compress: &yes}
	svc.Normalize()
	if !svc.CompressEnabled() {
		t.Errorf("явный compress: true при auth: basic должен включать сжатие")
	}
}

// План 126: разбор блока cache и умолчание vary.
func TestLoadFile_Cache(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "site.yaml")
	yaml := `name: Site
root_url: site
auth: none
cache:
  ttl: 60
  public: true
templates:
  - template: /
    methods:
      get: Главная
`
	if err := os.WriteFile(path, []byte(yaml), 0o600); err != nil {
		t.Fatal(err)
	}
	svc, err := LoadFile(path)
	if err != nil {
		t.Fatalf("LoadFile: %v", err)
	}
	if !svc.Cache.Enabled() || svc.Cache.TTL != 60 || !svc.Cache.Public {
		t.Fatalf("блок cache прочитан неверно: %+v", svc.Cache)
	}
	// Отсутствующий vary — умолчание [query]; пустой список значит другое.
	if !svc.Cache.VaryBy("query") {
		t.Errorf("vary по умолчанию должен включать query")
	}
	if svc.Cache.VaryBy("host") {
		t.Errorf("host не должен входить в ключ без явного указания")
	}
	if svc.Cache.BodyLimit() != DefaultCacheMaxBody {
		t.Errorf("BodyLimit()=%d, ожидался дефолт %d", svc.Cache.BodyLimit(), DefaultCacheMaxBody)
	}
	if !svc.CacheUsable() {
		t.Errorf("при auth: none кэш должен быть применим")
	}
}

// Пустой vary — «одна страница для всех», а не умолчание.
func TestCache_EmptyVaryIsNotDefault(t *testing.T) {
	svc := &Service{Name: "S", RootURL: "s", Auth: "none",
		Cache: &CacheConfig{TTL: 30, Vary: []string{}}}
	svc.Normalize()
	if svc.Cache.VaryBy("query") {
		t.Errorf("явный пустой vary не должен превращаться в [query]")
	}
}

// Кэш при auth ≠ none неприменим: ответ одного пользователя достался бы другому.
func TestCacheUsable_RequiresAnonymous(t *testing.T) {
	for _, auth := range []string{"basic", "session", "token", "hmac"} {
		svc := &Service{Name: "S", RootURL: "s", Auth: auth, Cache: &CacheConfig{TTL: 60}}
		svc.Normalize()
		if svc.CacheUsable() {
			t.Errorf("auth=%q: кэш считается применимым", auth)
		}
	}
}
