package ui

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/ivantit66/onebase/internal/auth"
	"github.com/ivantit66/onebase/internal/metadata"
	reportpkg "github.com/ivantit66/onebase/internal/report"
	"github.com/ivantit66/onebase/internal/runtime"
	"github.com/stretchr/testify/require"
	"github.com/xuri/excelize/v2"
	"gopkg.in/yaml.v3"
)

func TestReportCatalogNameContextHTTP(t *testing.T) {
	var rep reportpkg.Report
	require.NoError(t, yaml.Unmarshal([]byte(`
name: StockPrice
params:
  - name: PriceType
    type: reference:PriceTypeCatalog
    context:
      catalog_name:
        catalog: PriceTypeCatalog
        name: Цена мастера
query: |
  ВЫБРАТЬ С.Наименование ИЗ Справочник.StockOwner КАК М
  ВНУТРЕННЕЕ СОЕДИНЕНИЕ Справочник.StockEntry КАК С ПО С.Владелец = М.Ссылка
  ГДЕ С.ТипЦены = &PriceType
`), &rep))
	price := &metadata.Entity{Name: "PriceTypeCatalog", Kind: metadata.KindCatalog, Hierarchical: true, Fields: []metadata.Field{
		{Name: "Наименование", Type: metadata.FieldTypeString},
	}}
	owner := &metadata.Entity{Name: "StockOwner", Kind: metadata.KindCatalog}
	row := &metadata.Entity{Name: "StockEntry", Kind: metadata.KindCatalog, Fields: []metadata.Field{
		{Name: "Наименование", Type: metadata.FieldTypeString},
		{Name: "ТипЦены", Type: metadata.FieldTypeString, RefEntity: price.Name},
		{Name: "Владелец", Type: metadata.FieldTypeString, RefEntity: owner.Name},
	}}
	entities := []*metadata.Entity{price, owner, row}
	s, ctx := newSubmitTestServer(t, entities)
	s.ops = newOperationLimiter()
	s.reg.Load(runtime.LoadOptions{Entities: entities, Reports: []*reportpkg.Report{&rep}})
	master, alternate := uuid.New(), uuid.New()
	putPrice := func(id uuid.UUID, name string) {
		t.Helper()
		require.NoError(t, s.store.Upsert(ctx, price.Name, id, map[string]any{"Наименование": name}, price))
	}
	putPrice(master, "Цена мастера")
	putPrice(alternate, "Розничная цена")
	for _, flag := range []string{"deletion_mark", "is_folder"} {
		id := uuid.New()
		putPrice(id, "Цена мастера")
		_, err := s.store.Exec(ctx, "UPDATE "+metadata.TableName(price.Name)+" SET "+flag+" = 1 WHERE id = ?", id.String())
		require.NoError(t, err)
	}
	ownerID := uuid.New()
	require.NoError(t, s.store.Upsert(ctx, owner.Name, ownerID, nil, owner))
	require.NoError(t, s.store.Upsert(ctx, row.Name, uuid.New(), map[string]any{
		"Наименование": "STOCK_MASTER_PRICE", "ТипЦены": master.String(), "Владелец": ownerID.String(),
	}, row))
	require.NoError(t, s.store.Upsert(ctx, row.Name, uuid.New(), map[string]any{
		"Наименование": "STOCK_ALTERNATE_PRICE", "ТипЦены": alternate.String(), "Владелец": ownerID.String(),
	}, row))
	user := &auth.User{ID: uuid.NewString(), Login: "stock-otk", Roles: []*auth.Role{{
		Name: "ОТКСотрудник", Permissions: auth.Permission{
			Reports:  map[string][]string{rep.Name: {"run"}},
			Catalogs: map[string][]string{row.Name: {"read"}, owner.Name: {"read"}},
		},
	}}}
	router := chi.NewRouter()
	s.Mount(router)
	get := func(path string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		req = req.WithContext(auth.ContextWithUser(req.Context(), user))
		res := httptest.NewRecorder()
		router.ServeHTTP(res, req)
		return res
	}
	require.Equal(t, http.StatusForbidden, get("/ui/catalog/"+price.Name).Code)
	require.Equal(t, http.StatusForbidden, get("/ui/catalog/"+price.Name+"/"+master.String()).Code)
	base := "/ui/report/" + rep.Name
	spoof := url.Values{"__run": {"1"}, "PriceType": {alternate.String()}, "pricetype": {alternate.String()}}
	actorCtx := auth.ContextWithUser(ctx, user)
	bound, err := reportpkg.ResolveContextParams(actorCtx, &rep, nil, s.reg, s.store)
	require.NoError(t, err)
	require.Equal(t, master.String(), bound["PriceType"])
	compiled, err := s.compileQueryWithRowAccess(actorCtx, rep.Query, bound)
	require.NoError(t, err)
	require.Empty(t, s.deniedQuerySource(actorCtx, compiled.Sources), "sources=%v", compiled.Sources)
	html := get(base + "?" + spoof.Encode())
	require.Equal(t, http.StatusOK, html.Code)
	require.Contains(t, html.Body.String(), "STOCK_MASTER_PRICE")
	require.NotContains(t, html.Body.String(), "STOCK_ALTERNATE_PRICE")
	require.NotContains(t, html.Body.String(), master.String())
	require.NotContains(t, html.Body.String(), `name="PriceType"`)
	xlsx := get(base + "/excel?" + spoof.Encode())
	require.Equal(t, http.StatusOK, xlsx.Code)
	book, err := excelize.OpenReader(bytes.NewReader(xlsx.Body.Bytes()))
	require.NoError(t, err)
	rows, err := book.GetRows(book.GetSheetList()[0])
	require.NoError(t, err)
	require.NoError(t, book.Close())
	require.Contains(t, rows, []string{"STOCK_MASTER_PRICE"})
	require.NotContains(t, rows, []string{"STOCK_ALTERNATE_PRICE"})
	pdf := get(base + "/pdf?" + spoof.Encode())
	require.Equal(t, http.StatusOK, pdf.Code)
	require.True(t, strings.HasPrefix(pdf.Body.String(), "%PDF-"))

	putPrice(master, "Другая цена")
	missing := get(base + "?" + spoof.Encode())
	require.Equal(t, http.StatusForbidden, missing.Code)
	require.Contains(t, missing.Body.String(), "запись для параметра PriceType не найдена")
	putPrice(master, "Цена мастера")
	putPrice(uuid.New(), "Цена мастера")
	for _, path := range []string{base, base + "/excel"} {
		res := get(path + "?" + spoof.Encode())
		require.Equal(t, http.StatusForbidden, res.Code)
		require.Contains(t, res.Body.String(), "для параметра PriceType найдено несколько записей")
	}
}
