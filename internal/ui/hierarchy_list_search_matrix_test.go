package ui

// Поиск и отбор в списке иерархического справочника (режим папок) ищут по
// всему справочнику, а не среди прямых детей открытой папки. Раньше поиск из
// корня находил только верхние папки, а из папки — только её детей: мастер в
// группе «СМ» не находился ни из корня, ни из «БРН». Матрица — потому что
// условие parent_id и поиск собирает SQL; путь — публичный GET списка.

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/ivantit66/onebase/internal/auth"

	"github.com/ivantit66/onebase/internal/dbtest"
	"github.com/ivantit66/onebase/internal/dsl/interpreter"
	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/ivantit66/onebase/internal/runtime"
	"github.com/ivantit66/onebase/internal/storage"
)

func TestHierarchyListSearchFindsWholeCatalog(t *testing.T) {
	dbtest.ForEachDialect(t, func(t *testing.T, db *storage.DB) {
		ctx := context.Background()
		ent := &metadata.Entity{
			Name: "МастерИерархия", Kind: metadata.KindCatalog, Hierarchical: true,
			Fields: []metadata.Field{{Name: "Наименование", Type: metadata.FieldTypeString}},
		}
		if err := db.Migrate(ctx, []*metadata.Entity{ent}); err != nil {
			t.Fatal(err)
		}
		sm, brn, item := uuid.New(), uuid.New(), uuid.New()
		for _, row := range []struct {
			id     uuid.UUID
			values map[string]any
		}{
			{sm, map[string]any{"Наименование": "СМ", "is_folder": true}},
			{brn, map[string]any{"Наименование": "БРН", "is_folder": true}},
			{item, map[string]any{"Наименование": "Севрюкова_Тест", "parent_id": sm.String()}},
			{uuid.New(), map[string]any{"Наименование": "Петров", "parent_id": brn.String()}},
		} {
			if err := db.Upsert(ctx, ent.Name, row.id, row.values, ent); err != nil {
				t.Fatal(err)
			}
		}
		registry := runtime.NewRegistry()
		registry.Load(runtime.LoadOptions{Entities: []*metadata.Entity{ent}})
		s := &Server{store: db, reg: registry, interp: interpreter.New(), lockMgr: runtime.NewLockManager(),
			messages: NewMessageStore(), ops: newOperationLimiter()}
		get := func(query string) string {
			w := httptest.NewRecorder()
			s.list(w, reqWithChi("GET", "/ui/catalog/мастериерархия"+query, nil, map[string]string{"entity": "мастериерархия"}))
			if w.Code != 200 {
				t.Fatalf("%s: код %d", query, w.Code)
			}
			return w.Body.String()
		}

		for _, q := range []string{"?q=Севрюкова", "?q=Севрюкова&parent=" + brn.String()} {
			body := get(q)
			if !strings.Contains(body, "Севрюкова_Тест") {
				t.Fatalf("%s: элемент из группы «СМ» не найден", q)
			}
			if strings.Contains(body, "Петров") {
				t.Fatalf("%s: поиск вернул лишнее", q)
			}
			if !strings.Contains(body, "Результаты поиска по всему справочнику") {
				t.Fatalf("%s: нет пометки «поиск по всему справочнику»", q)
			}
			if !strings.Contains(body, "data-ob-list-group") || !strings.Contains(body, "?parent="+sm.String()) || !strings.Contains(body, "📁 СМ") {
				t.Fatalf("%s: у найденного не показана группа «СМ» со ссылкой на неё", q)
			}
		}
		// Отбор панели «Отбор» — так же по всему справочнику.
		if body := get("?f.Наименование=Севрюкова_Тест"); !strings.Contains(body, "Севрюкова_Тест") {
			t.Fatalf("отбор из корня не нашёл элемент в группе")
		}
		// «✕» и навигация: без поиска — прежняя работа по папкам.
		root := get("")
		if strings.Contains(root, "Севрюкова_Тест") || !strings.Contains(root, "СМ") || strings.Contains(root, "Результаты поиска по всему справочнику") {
			t.Fatalf("без поиска корень обязан показывать только верхний уровень")
		}
		if folder := get("?parent=" + sm.String()); !strings.Contains(folder, "Севрюкова_Тест") || strings.Contains(folder, "Петров") {
			t.Fatalf("без поиска папка показывает только своих детей")
		}
		// Количество совпадает со строками: CountList и List — одно условие.
		total, err := db.CountList(ctx, ent.Name, ent, storage.ListParams{Search: "Севрюкова"})
		if err != nil || total != 1 {
			t.Fatalf("CountList по всему справочнику = %d, %v", total, err)
		}
	})
}

// Подпись группы не должна обходить строковые права: ребёнок может быть
// доступен, даже если его родитель запрещён. Проверяем настоящие GET-маршруты
// списка и формы, а также сохранение подписи разрешённой группы.
func TestHierarchyListSearchHidesUnreadableGroups(t *testing.T) {
	dbtest.ForEachDialect(t, func(t *testing.T, db *storage.DB) {
		ctx := context.Background()
		ent := &metadata.Entity{
			Name: "ГруппыПоиска", Kind: metadata.KindCatalog, Hierarchical: true,
			Fields: []metadata.Field{
				{Name: "Наименование", Type: metadata.FieldTypeString},
				{Name: "Доступен", Type: metadata.FieldTypeBool},
			},
		}
		if err := db.Migrate(ctx, []*metadata.Entity{ent}); err != nil {
			t.Fatal(err)
		}
		hidden, visible, hiddenChild, visibleChild := uuid.New(), uuid.New(), uuid.New(), uuid.New()
		for _, row := range []struct {
			id              uuid.UUID
			name            string
			parent          string
			folder, allowed bool
		}{
			{hidden, "Секретная_группа", "", true, false},
			{visible, "Открытая_группа", "", true, true},
			{hiddenChild, "Найденный_ребёнок_скрытой", hidden.String(), false, true},
			{visibleChild, "Найденный_ребёнок_открытой", visible.String(), false, true},
		} {
			if err := db.Upsert(ctx, ent.Name, row.id, map[string]any{
				"Наименование": row.name, "parent_id": row.parent,
				"is_folder": row.folder, "Доступен": row.allowed,
			}, ent); err != nil {
				t.Fatal(err)
			}
		}
		reg := runtime.NewRegistry()
		reg.Load(runtime.LoadOptions{Entities: []*metadata.Entity{ent}})
		srv := &Server{store: db, reg: reg, interp: interpreter.New(), lockMgr: runtime.NewLockManager(),
			messages: NewMessageStore(), ops: newOperationLimiter()}
		user := &auth.User{Login: "reader", Roles: []*auth.Role{{Permissions: auth.Permission{
			Catalogs: map[string][]string{ent.Name: {"read"}},
			RowAccess: auth.RowAccess{Catalogs: map[string]auth.RowPolicies{
				ent.Name: {"read": {Field: "Доступен", Op: "eq", Value: auth.RowValue{Literal: true}}},
			}},
		}}}}
		router := chi.NewRouter()
		srv.Mount(router)
		listURL := "/ui/catalog/" + url.PathEscape(strings.ToLower(ent.Name))
		get := func(path string, who *auth.User) *httptest.ResponseRecorder {
			t.Helper()
			req := httptest.NewRequest(http.MethodGet, path, nil)
			req = req.WithContext(auth.ContextWithUser(req.Context(), who))
			rec := httptest.NewRecorder()
			router.ServeHTTP(rec, req)
			return rec
		}
		if rec := get(listURL+"/"+hidden.String(), user); rec.Code != http.StatusForbidden {
			t.Fatalf("закрытая группа: status=%d, ожидалось 403", rec.Code)
		}
		for _, query := range []string{
			"?q=" + url.QueryEscape("Найденный"),
			"?f." + url.QueryEscape("Доступен") + "=true",
		} {
			rec := get(listURL+query, user)
			if rec.Code != http.StatusOK {
				t.Fatalf("%s: status=%d", query, rec.Code)
			}
			body := rec.Body.String()
			for _, id := range []uuid.UUID{hiddenChild, visibleChild} {
				if !strings.Contains(body, "/"+id.String()) {
					t.Fatalf("%s: нет строки ребёнка %s", query, id)
				}
			}
			if strings.Contains(body, "Секретная_группа") || strings.Contains(body, "?parent="+hidden.String()) {
				t.Errorf("%s: раскрыты подпись или ссылка закрытой группы", query)
			}
			if !strings.Contains(body, "📁 Открытая_группа") || !strings.Contains(body, "?parent="+visible.String()) {
				t.Errorf("%s: нет подписи или ссылки разрешённой группы", query)
			}
		}
		// Администратор видит обе группы: ограничение не меняет его права.
		rec := get(listURL+"?q="+url.QueryEscape("Найденный"), &auth.User{Login: "admin", IsAdmin: true})
		if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "📁 Секретная_группа") ||
			!strings.Contains(rec.Body.String(), "?parent="+hidden.String()) {
			t.Fatal("администратор не видит подпись и ссылку группы")
		}
	})
}
