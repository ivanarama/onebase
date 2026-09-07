package ui

import (
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ivantit66/onebase/internal/auth"
	"github.com/ivantit66/onebase/internal/project"
	"github.com/ivantit66/onebase/internal/storage"
)

// TestCMSCartCookieIsScopedToShopService идёт через поставляемый HTTP-сервис
// examples/cms: товар добавляется настоящим POST /hs/shop/cart/add, а затем
// cookie jar применяет браузерные правила Path к магазину и к витрине.
func TestCMSCartCookieIsScopedToShopService(t *testing.T) {
	ctx := t.Context()
	proj, err := project.Load(filepath.Join("..", "..", "examples", "cms"))
	if err != nil {
		t.Fatalf("загрузка examples/cms: %v", err)
	}
	t.Cleanup(proj.Close)

	db, err := storage.ConnectSQLite(ctx, filepath.Join(t.TempDir(), "cms-cart.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(db.Close)
	if err := db.EnsureServiceSchema(ctx); err != nil {
		t.Fatalf("служебная схема: %v", err)
	}
	if err := db.Migrate(ctx, proj.Entities); err != nil {
		t.Fatalf("схема CMS: %v", err)
	}
	if err := db.SaveNetworkEnabled(ctx, true); err != nil {
		t.Fatalf("включение HTTP-сервисов: %v", err)
	}

	s, reg, err := NewOfflineServer(proj, db)
	if err != nil {
		t.Fatalf("runtime CMS: %v", err)
	}
	reg.LoadHTTPServices(proj.HTTPServices)
	s.authRepo = auth.NewRepo(db)
	if err := s.authRepo.EnsureSchema(ctx); err != nil {
		t.Fatalf("схема авторизации: %v", err)
	}
	s.maxFileSizeBytes = 1 << 20
	s.loginLimit = auth.NewLoginLimiter(5, time.Minute)

	siteID, err := db.WriteCatalogRecord(ctx, cmsEntity(t, proj, "Сайты"), "", map[string]any{
		"Наименование":   "Cookie path test",
		"Домен":          "shop.test",
		"ПрефиксПути":    "/hs/site",
		"ПриёмЗаказов":   true,
		"ПочтаМенеджера": "",
		"Активен":        true,
	})
	if err != nil {
		t.Fatalf("создание сайта: %v", err)
	}
	productID, err := db.WriteCatalogRecord(ctx, cmsEntity(t, proj, "Товары"), "", map[string]any{
		"Наименование":     "Тестовый товар",
		"Артикул":          "COOKIE-1",
		"Слаг":             "cookie-product",
		"Цена":             100,
		"Валюта":           "RUB",
		"СтатусПубликации": "Опубликовано",
		"Сайт":             siteID,
	})
	if err != nil {
		t.Fatalf("создание товара: %v", err)
	}

	form := url.Values{"product": {productID}, "qty": {"1"}}
	req := httptest.NewRequest(http.MethodPost, "http://shop.test/hs/shop/cart/add", strings.NewReader(form.Encode()))
	req.Host = "shop.test"
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Origin", "http://shop.test")
	rec := httptest.NewRecorder()
	s.serviceDispatch(rec, req)
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("добавление в корзину: status=%d body=%s", rec.Code, rec.Body.String())
	}

	result := rec.Result()
	t.Cleanup(func() { _ = result.Body.Close() })
	var cartCookie *http.Cookie
	for _, cookie := range result.Cookies() {
		if cookie.Name == "ob_cart" {
			cartCookie = cookie
			break
		}
	}
	if cartCookie == nil {
		t.Fatalf("ответ не установил ob_cart: %v", result.Header.Values("Set-Cookie"))
	}
	if cartCookie.Path != "/hs/shop" {
		t.Fatalf("Path ob_cart=%q, ожидался /hs/shop", cartCookie.Path)
	}

	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatal(err)
	}
	addURL, _ := url.Parse("http://shop.test/hs/shop/cart/add")
	shopURL, _ := url.Parse("http://shop.test/hs/shop/cart")
	siteURL, _ := url.Parse("http://shop.test/hs/site/product/cookie-product")
	jar.SetCookies(addURL, result.Cookies())
	if !hasCookie(jar.Cookies(shopURL), "ob_cart") {
		t.Fatal("браузер не отправил ob_cart обратно сервису магазина")
	}
	if hasCookie(jar.Cookies(siteURL), "ob_cart") {
		t.Fatal("браузер отправил ob_cart к кэшируемому сервису сайта")
	}

	cartReq := httptest.NewRequest(http.MethodGet, shopURL.String(), nil)
	cartReq.Host = "shop.test"
	for _, cookie := range jar.Cookies(shopURL) {
		cartReq.AddCookie(cookie)
	}
	cartRec := httptest.NewRecorder()
	s.serviceDispatch(cartRec, cartReq)
	if cartRec.Code != http.StatusOK {
		t.Fatalf("чтение корзины с суженной cookie: status=%d body=%s", cartRec.Code, cartRec.Body.String())
	}
}

func hasCookie(cookies []*http.Cookie, name string) bool {
	for _, cookie := range cookies {
		if cookie.Name == name {
			return true
		}
	}
	return false
}
