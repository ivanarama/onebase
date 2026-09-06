package ui

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/ivantit66/onebase/internal/dbtest"
	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/ivantit66/onebase/internal/project"
	"github.com/ivantit66/onebase/internal/storage"
)

type cmsCheckoutFixture struct {
	db       *storage.DB
	servers  []*Server
	site     *metadata.Entity
	product  *metadata.Entity
	basket   *metadata.Entity
	order    *metadata.Entity
	basketID uuid.UUID
}

func newCMSCheckoutFixture(t *testing.T, proj *project.Project, db *storage.DB) *cmsCheckoutFixture {
	t.Helper()
	ctx := context.Background()
	if err := db.Migrate(ctx, proj.Entities); err != nil {
		t.Fatalf("migrate CMS: %v", err)
	}
	if err := db.SaveNetworkEnabled(ctx, true); err != nil {
		t.Fatalf("enable HTTP services: %v", err)
	}

	f := &cmsCheckoutFixture{
		db:      db,
		site:    checkoutEntity(t, proj, "Сайты"),
		product: checkoutEntity(t, proj, "Товары"),
		basket:  checkoutEntity(t, proj, "Корзины"),
		order:   checkoutEntity(t, proj, "ЗаказПокупателя"),
	}
	// Два независимых runtime моделируют два процесса. На PostgreSQL их
	// сериализует advisory transaction lock; на SQLite — транзакция записи.
	for range 2 {
		s, reg, err := NewOfflineServer(proj, db)
		if err != nil {
			t.Fatalf("CMS runtime: %v", err)
		}
		reg.LoadHTTPServices(proj.HTTPServices)
		// Убираем ленивую инициализацию из конкурентной части самого теста.
		s.ops = newOperationLimiter()
		s.maxFileSizeBytes = 1 << 20
		f.servers = append(f.servers, s)
	}

	siteID := uuid.New()
	if err := db.Upsert(ctx, f.site.Name, siteID, map[string]any{
		"Наименование": "Checkout test site",
		"Домен":        "checkout.test",
		"Язык":         "ru",
		"ПриёмЗаказов": true,
		"Активен":      true,
	}, f.site); err != nil {
		t.Fatalf("seed site: %v", err)
	}
	productID := uuid.New()
	if err := db.Upsert(ctx, f.product.Name, productID, map[string]any{
		"Наименование":     "Тестовый товар",
		"Артикул":          "ATOMIC-1",
		"Слаг":             "atomic-product",
		"Цена":             125.0,
		"Валюта":           "RUB",
		"СтатусПубликации": "Опубликовано",
		"Сайт":             siteID.String(),
	}, f.product); err != nil {
		t.Fatalf("seed product: %v", err)
	}
	f.basketID = uuid.New()
	if err := db.Upsert(ctx, f.basket.Name, f.basketID, map[string]any{
		"Наименование": "Atomic checkout basket",
		"Сайт":         siteID.String(),
		"Дата":         time.Now().UTC(),
		"Оформлена":    false,
	}, f.basket); err != nil {
		t.Fatalf("seed basket: %v", err)
	}
	part := checkoutTablePart(t, f.basket, "Строки")
	if err := db.UpsertTablePartRows(ctx, f.basket.Name, part.Name, f.basketID,
		[]map[string]any{{"Товар": productID.String(), "Количество": 2.0}}, part); err != nil {
		t.Fatalf("seed basket rows: %v", err)
	}
	return f
}

func checkoutEntity(t *testing.T, proj *project.Project, name string) *metadata.Entity {
	t.Helper()
	for _, entity := range proj.Entities {
		if entity.Name == name {
			return entity
		}
	}
	t.Fatalf("CMS entity %q not found", name)
	return nil
}

func checkoutTablePart(t *testing.T, entity *metadata.Entity, name string) metadata.TablePart {
	t.Helper()
	for _, part := range entity.TableParts {
		if part.Name == name {
			return part
		}
	}
	t.Fatalf("table part %s.%s not found", entity.Name, name)
	return metadata.TablePart{}
}

func (f *cmsCheckoutFixture) post(t *testing.T, server *Server) *httptest.ResponseRecorder {
	t.Helper()
	form := url.Values{
		"name":    {"Иван"},
		"contact": {"+7 999 000-11-22"}, // телефон не запускает SMTP после commit
		"comment": {"atomic checkout"},
	}
	req := httptest.NewRequest(http.MethodPost, "http://checkout.test/hs/shop/checkout",
		strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Origin", "http://checkout.test")
	req.AddCookie(&http.Cookie{Name: "ob_cart", Value: f.basketID.String()})
	w := httptest.NewRecorder()
	server.serviceDispatch(w, req)
	return w
}

func (f *cmsCheckoutFixture) orderCount(t *testing.T) int {
	t.Helper()
	var count int
	if err := f.db.QueryRow(context.Background(),
		"SELECT COUNT(*) FROM "+metadata.TableName(f.order.Name)).Scan(&count); err != nil {
		t.Fatalf("count orders: %v", err)
	}
	return count
}

func TestCMSCheckout_ConcurrentPostsCreateOneOrder(t *testing.T) {
	proj, err := project.Load(filepath.Join("..", "..", "examples", "cms"))
	if err != nil {
		t.Fatalf("load CMS: %v", err)
	}
	t.Cleanup(proj.Close)

	dbtest.ForEachDialect(t, func(t *testing.T, db *storage.DB) {
		f := newCMSCheckoutFixture(t, proj, db)
		const requests = 8
		start := make(chan struct{})
		responses := make(chan *httptest.ResponseRecorder, requests)
		var ready sync.WaitGroup
		ready.Add(requests)
		for i := 0; i < requests; i++ {
			server := f.servers[i%len(f.servers)]
			go func() {
				ready.Done()
				<-start
				responses <- f.post(t, server)
			}()
		}
		ready.Wait()
		close(start)

		locations := map[string]bool{}
		for range requests {
			w := <-responses
			if w.Code != http.StatusSeeOther {
				t.Fatalf("checkout status=%d body=%s", w.Code, w.Body.String())
			}
			locations[w.Header().Get("Location")] = true
		}
		if len(locations) != 1 {
			t.Fatalf("parallel checkouts redirected to different orders: %v", locations)
		}
		for location := range locations {
			if !strings.HasPrefix(location, "/hs/shop/order/") {
				t.Fatalf("checkout redirect=%q", location)
			}
		}
		if got := f.orderCount(t); got != 1 {
			t.Fatalf("parallel checkouts created %d orders, want 1", got)
		}

		row, err := db.GetByID(context.Background(), f.basket.Name, f.basketID, f.basket)
		if err != nil {
			t.Fatalf("load closed basket: %v", err)
		}
		if !checkoutBool(row["Оформлена"]) || checkoutRef(row["Заказ"]) == "" {
			t.Fatalf("basket not closed with order: %#v", row)
		}
		part := checkoutTablePart(t, f.basket, "Строки")
		rows, err := db.GetTablePartRows(context.Background(), f.basket.Name, part.Name, f.basketID, part)
		if err != nil {
			t.Fatalf("load closed basket rows: %v", err)
		}
		if len(rows) != 0 {
			t.Fatalf("closed basket kept %d rows", len(rows))
		}
	})
}

func TestCMSCheckout_BasketWriteFailureRollsBackOrder(t *testing.T) {
	proj, err := project.Load(filepath.Join("..", "..", "examples", "cms"))
	if err != nil {
		t.Fatalf("load CMS: %v", err)
	}
	t.Cleanup(proj.Close)

	dbtest.ForEachDialect(t, func(t *testing.T, db *storage.DB) {
		f := newCMSCheckoutFixture(t, proj, db)
		installCheckoutBasketFailure(t, db, metadata.TableName(f.basket.Name))

		w := f.post(t, f.servers[0])
		if w.Code != http.StatusInternalServerError || !strings.Contains(w.Body.String(), "forced checkout basket failure") {
			t.Fatalf("checkout failure status=%d body=%s", w.Code, w.Body.String())
		}
		if got := f.orderCount(t); got != 0 {
			t.Fatalf("failed checkout left %d orders, want 0", got)
		}
		row, err := db.GetByID(context.Background(), f.basket.Name, f.basketID, f.basket)
		if err != nil {
			t.Fatalf("load basket after rollback: %v", err)
		}
		if checkoutBool(row["Оформлена"]) || checkoutRef(row["Заказ"]) != "" {
			t.Fatalf("failed checkout changed basket: %#v", row)
		}
		part := checkoutTablePart(t, f.basket, "Строки")
		rows, err := db.GetTablePartRows(context.Background(), f.basket.Name, part.Name, f.basketID, part)
		if err != nil {
			t.Fatalf("load basket rows after rollback: %v", err)
		}
		if len(rows) != 1 {
			t.Fatalf("failed checkout left %d basket rows, want 1", len(rows))
		}
	})
}

func checkoutBool(value any) bool {
	switch v := value.(type) {
	case bool:
		return v
	case int64:
		return v != 0
	case int:
		return v != 0
	}
	return false
}

func checkoutRef(value any) string {
	if value == nil {
		return ""
	}
	text := strings.TrimSpace(fmt.Sprint(value))
	if text == "<nil>" {
		return ""
	}
	return text
}

func installCheckoutBasketFailure(t *testing.T, db *storage.DB, table string) {
	t.Helper()
	ctx := context.Background()
	var statements []string
	if db.IsPostgres() {
		statements = []string{
			`CREATE FUNCTION cms_checkout_fail() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
  IF NEW.оформлена THEN
    RAISE EXCEPTION 'forced checkout basket failure';
  END IF;
  RETURN NEW;
END $$`,
			fmt.Sprintf(`CREATE TRIGGER cms_checkout_fail BEFORE UPDATE ON %s
FOR EACH ROW EXECUTE FUNCTION cms_checkout_fail()`, table),
		}
	} else {
		statements = []string{fmt.Sprintf(`CREATE TRIGGER cms_checkout_fail BEFORE UPDATE ON %s
WHEN NEW.оформлена = 1
BEGIN
  SELECT RAISE(ABORT, 'forced checkout basket failure');
END`, table)}
	}
	for _, statement := range statements {
		if _, err := db.Exec(ctx, statement); err != nil {
			t.Fatalf("install basket failure trigger: %v", err)
		}
	}
}
