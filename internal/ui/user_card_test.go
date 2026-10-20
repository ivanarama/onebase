package ui

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/ivantit66/onebase/internal/api"
	"github.com/ivantit66/onebase/internal/auth"
	"github.com/ivantit66/onebase/internal/dbtest"
	"github.com/ivantit66/onebase/internal/dsl/interpreter"
	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/ivantit66/onebase/internal/report"
	"github.com/ivantit66/onebase/internal/runtime"
	"github.com/ivantit66/onebase/internal/storage"
	"github.com/ivantit66/onebase/internal/widget"
	"github.com/shopspring/decimal"
	"github.com/xuri/excelize/v2"
)

type cardFixture struct {
	s                       *Server
	router                  http.Handler
	ctx                     context.Context
	user                    *auth.User
	card, orders            *metadata.Entity
	cardID, visible, hidden uuid.UUID
}

func newCardFixture(t *testing.T, db *storage.DB) cardFixture {
	return newCardFixtureTyped(t, db, metadata.FieldTypeString, "A", "B")
}
func newCardFixtureTyped(t *testing.T, db *storage.DB, typ metadata.FieldType, own, other any) cardFixture {
	t.Helper()
	f := cardFixture{ctx: context.Background(), cardID: uuid.New(), visible: uuid.New(), hidden: uuid.New()}
	f.card = &metadata.Entity{Name: "Сотрудник", Kind: metadata.KindCatalog, Hierarchical: true, Fields: []metadata.Field{
		{Name: "Наименование", Type: metadata.FieldTypeString},
		{Name: "Учётка", Type: metadata.FieldType("reference:_users"), RefEntity: "_users"},
		{Name: "Филиал", Type: typ}}}
	f.orders = &metadata.Entity{Name: "Ордер", Kind: metadata.KindDocument, Fields: []metadata.Field{
		{Name: "Номер", Type: metadata.FieldTypeString}, {Name: "Филиал", Type: typ}}}
	entities := []*metadata.Entity{f.card, f.orders}
	var enums []*metadata.Enum
	if metadata.IsEnum(typ) {
		name := metadata.EnumTypeName(typ)
		f.card.Fields[2].EnumName = name
		f.orders.Fields[1].EnumName = name
		enums = []*metadata.Enum{{Name: name, Values: []string{"A", "B"}}}
	}
	if metadata.IsReference(typ) {
		target := metadata.RefName(typ)
		f.card.Fields[2].RefEntity = target
		f.orders.Fields[1].RefEntity = target
		branches := &metadata.Entity{Name: target, Kind: metadata.KindCatalog, Fields: []metadata.Field{{Name: "Наименование", Type: metadata.FieldTypeString}}}
		entities = append([]*metadata.Entity{branches}, entities...)
	}
	if err := auth.NewRepo(db).EnsureSchema(f.ctx); err != nil {
		t.Fatal(err)
	}
	if err := db.Migrate(f.ctx, entities); err != nil {
		t.Fatal(err)
	}
	// Real FK user; infrastructure reader compares the card's FK without a JOIN.
	userID := uuid.NewString()
	if _, err := db.Exec(f.ctx, fmt.Sprintf("INSERT INTO _users (id, login, password_hash) VALUES (%s,%s,%s)", db.Dialect().Placeholder(1), db.Dialect().Placeholder(2), db.Dialect().Placeholder(3)), userID, "employee", "unused"); err != nil {
		t.Fatal(err)
	}
	policy := auth.RowPolicy{Field: "Филиал", Op: "eq", Value: auth.RowValue{UserCard: &auth.UserCardSource{Catalog: f.card.Name, AccountField: "Учётка", Field: "Филиал"}}}
	f.user = &auth.User{ID: userID, Login: "employee", Roles: []*auth.Role{{Name: "Сотрудник", Permissions: auth.Permission{
		Documents: map[string][]string{f.orders.Name: {"read", "write", "delete", "post", "unpost"}},
		RowAccess: auth.RowAccess{Documents: map[string]auth.RowPolicies{f.orders.Name: {"read": policy, "write": {SameAs: "read"}, "delete": {SameAs: "read"}, "post": {SameAs: "read"}, "unpost": {SameAs: "read"}}}}}}}}
	reg := runtime.NewRegistry()
	reg.Load(runtime.LoadOptions{Entities: entities, Enums: enums})
	interp := interpreter.New()
	interp.LookupProc = reg.GetModuleProc
	f.s = New(reg, db, interp, nil, Config{}, nil)
	t.Cleanup(f.s.Close)
	r := chi.NewRouter()
	r.Use(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			next.ServeHTTP(w, r.WithContext(auth.ContextWithUser(r.Context(), f.user)))
		})
	})
	f.s.Mount(r)
	f.router = r
	if metadata.IsReference(typ) {
		branches := reg.GetEntity(metadata.RefName(typ))
		for _, value := range []any{own, other} {
			id, err := uuid.Parse(fmt.Sprint(value))
			if err != nil {
				t.Fatal(err)
			}
			if err := db.Upsert(f.ctx, branches.Name, id, map[string]any{"Наименование": id.String()}, branches); err != nil {
				t.Fatal(err)
			}
		}
	}
	f.saveCard(t, own)
	for _, row := range []struct {
		id uuid.UUID
		n  string
		b  any
	}{{f.visible, "VISIBLE", own}, {f.hidden, "HIDDEN", other}} {
		if err := db.Upsert(f.ctx, f.orders.Name, row.id, map[string]any{"Номер": row.n, "Филиал": row.b}, f.orders); err != nil {
			t.Fatal(err)
		}
	}
	return f
}
func (f cardFixture) saveCard(t *testing.T, branch any) {
	t.Helper()
	if err := f.s.store.Upsert(f.ctx, f.card.Name, f.cardID, map[string]any{"Наименование": "PRIVATE-CARD", "Учётка": f.user.ID, "Филиал": branch}, f.card); err != nil {
		t.Fatal(err)
	}
}
func (f cardFixture) request(method, path string, values url.Values) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, path, strings.NewReader(values.Encode()))
	if method == "POST" {
		r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	}
	w := httptest.NewRecorder()
	f.router.ServeHTTP(w, r)
	return w
}
func (f cardFixture) dslCount(t *testing.T, text string) any {
	return runDSLRowAccessFunc(t, f.s, auth.ContextWithUser(f.ctx, f.user), fmt.Sprintf(`Функция Проверка() Экспорт
 З = Новый Запрос;
 З.Текст = "%s";
 Возврат З.Выполнить().Количество();
КонецФункции`, text))
}
func TestUserCardPublicReadMatrix(t *testing.T) {
	dbtest.ForEachDialect(t, func(t *testing.T, db *storage.DB) {
		f := newCardFixture(t, db)
		for _, path := range []string{"/ui/document/ордер", "/ui/_ref-options/ордер"} {
			w := f.request("GET", path, nil)
			if w.Code != 200 || !strings.Contains(w.Body.String(), "VISIBLE") || strings.Contains(w.Body.String(), "HIDDEN") || strings.Contains(w.Body.String(), "PRIVATE-CARD") {
				t.Fatalf("%s: %d %s", path, w.Code, w.Body.String())
			}
		}
		picker := f.request("GET", "/ui/_ref-options/ордер", nil)
		var options choiceHTTPResponse
		if err := json.Unmarshal(picker.Body.Bytes(), &options); err != nil {
			t.Fatal(err)
		}
		if options.Total != 1 || len(options.Items) != 1 {
			t.Fatalf("picker counter leaked hidden rows: %+v", options)
		}
		for _, tc := range []struct {
			id     uuid.UUID
			status int
		}{{f.visible, 200}, {f.hidden, 403}} {
			w := f.request("GET", "/ui/document/ордер/"+tc.id.String(), nil)
			if w.Code != tc.status {
				t.Fatalf("open %s: %d", tc.id, w.Code)
			}
		}
		for _, text := range []string{
			"ВЫБРАТЬ Номер ИЗ Документ.Ордер",
			"ВЫБРАТЬ А.Номер ИЗ Документ.Ордер КАК А ЛЕВОЕ СОЕДИНЕНИЕ Документ.Ордер КАК Б ПО А.Ссылка = Б.Ссылка",
			"ВЫБРАТЬ Номер ИЗ Документ.Ордер ОБЪЕДИНИТЬ ВСЕ ВЫБРАТЬ Номер ИЗ Документ.Ордер",
		} {
			want := float64(1)
			if strings.Contains(text, "ОБЪЕДИНИТЬ") {
				want = 2
			}
			if got := f.dslCount(t, text); got != want {
				t.Fatalf("%s: %v", text, got)
			}
		}
		// Reuses the same session; no value cache.
		f.saveCard(t, "B")
		w := f.request("GET", "/ui/document/ордер", nil)
		if !strings.Contains(w.Body.String(), "HIDDEN") || strings.Contains(w.Body.String(), "VISIBLE") {
			t.Fatalf("updated card: %s", w.Body.String())
		}
		if got := f.dslCount(t, "ВЫБРАТЬ Номер ИЗ Документ.Ордер"); got != float64(1) {
			t.Fatal(got)
		}
		// Own card is neither readable nor writable through public APIs.
		if w := f.request("GET", "/ui/catalog/сотрудник", nil); w.Code != 403 {
			t.Fatalf("card leaked %d", w.Code)
		}
	})
}
func TestUserCardUnavailablePublicMatrix(t *testing.T) {
	dbtest.ForEachDialect(t, func(t *testing.T, db *storage.DB) {
		f := newCardFixture(t, db)
		original := f.user.Roles[0].Permissions.RowAccess.Documents[f.orders.Name]["read"]
		missingCases := []string{"none", "duplicate", "folder", "deleted", "empty"}
		for _, state := range missingCases {
			t.Run(state, func(t *testing.T) {
				f.saveCard(t, "A")
				restore := func() {}
				switch state {
				case "none":
					if err := db.Delete(f.ctx, f.card.Name, f.cardID); err != nil {
						t.Fatal(err)
					}
				case "duplicate":
					id := uuid.New()
					if err := db.Upsert(f.ctx, f.card.Name, id, map[string]any{"Наименование": "duplicate", "Учётка": f.user.ID, "Филиал": "B"}, f.card); err != nil {
						t.Fatal(err)
					}
					restore = func() {
						if err := db.Delete(f.ctx, f.card.Name, id); err != nil {
							t.Fatal(err)
						}
					}
				case "folder", "deleted":
					col := "is_folder"
					if state == "deleted" {
						col = "deletion_mark"
					}
					sql := fmt.Sprintf("UPDATE %s SET %s = %s WHERE id = %s", metadata.TableName(f.card.Name), col, db.Dialect().Placeholder(1), db.Dialect().Placeholder(2))
					if _, err := db.Exec(f.ctx, sql, true, f.cardID.String()); err != nil {
						t.Fatal(err)
					}
					restore = func() {
						if _, err := db.Exec(f.ctx, sql, false, f.cardID.String()); err != nil {
							t.Fatal(err)
						}
					}
				case "empty":
					f.saveCard(t, "")
				}
				defer restore()
				ne := original
				ne.Op = "ne"
				for _, p := range []auth.RowPolicy{original, ne, {Not: &original}, {Not: &auth.RowPolicy{Not: &original}}, {Any: []auth.RowPolicy{original, {Field: "Филиал", Op: "eq", Value: auth.RowValue{Literal: "A"}}}}} {
					f.user.Roles[0].Permissions.RowAccess.Documents[f.orders.Name]["read"] = p
					if got := f.dslCount(t, "ВЫБРАТЬ Номер ИЗ Документ.Ордер"); got != float64(0) {
						t.Fatalf("unavailable %s allowed %v", state, got)
					}
					w := f.request("GET", "/ui/document/ордер/"+f.visible.String(), nil)
					if w.Code != 403 {
						t.Fatalf("unavailable %s open=%d", state, w.Code)
					}
				}
				f.user.Roles[0].Permissions.RowAccess.Documents[f.orders.Name]["read"] = original
			})
		}
	})
}

func TestUserCardPublicRoleORAndRecursionMatrix(t *testing.T) {
	dbtest.ForEachDialect(t, func(t *testing.T, db *storage.DB) {
		f := newCardFixture(t, db)
		cardPolicy := auth.RowPolicy{Field: "Филиал", Op: "eq", Value: auth.RowValue{UserCard: &auth.UserCardSource{Catalog: f.card.Name, AccountField: "Учётка", Field: "Филиал"}}}
		// Catalog itself has the same policy, but user still has no read on cards.
		f.user.Roles[0].Permissions.RowAccess.Catalogs = map[string]auth.RowPolicies{f.card.Name: {"read": cardPolicy}}
		if got := f.dslCount(t, "ВЫБРАТЬ Номер ИЗ Документ.Ордер"); got != float64(1) {
			t.Fatal(got)
		}
		if err := db.Delete(f.ctx, f.card.Name, f.cardID); err != nil {
			t.Fatal(err)
		}
		restricted := f.user.Roles[0]
		for _, unrestricted := range []bool{false, true} {
			other := &auth.Role{Name: "Other", Permissions: auth.Permission{Documents: map[string][]string{f.orders.Name: {"read"}}}}
			want := float64(2)
			if !unrestricted {
				other.Permissions.RowAccess.Documents = map[string]auth.RowPolicies{f.orders.Name: {"read": {Field: "Филиал", Op: "eq", Value: auth.RowValue{Literal: "B"}}}}
				want = 1
			}
			for _, roles := range [][]*auth.Role{{restricted, other}, {other, restricted}} {
				f.user.Roles = roles
				if got := f.dslCount(t, "ВЫБРАТЬ Номер ИЗ Документ.Ордер"); got != want {
					t.Fatalf("OR got %v want %v", got, want)
				}
			}
		}
	})
}

func TestUserCardPublicWriteMatrix(t *testing.T) {
	dbtest.ForEachDialect(t, func(t *testing.T, db *storage.DB) {
		f := newCardFixture(t, db)
		// Public form rejects both an invisible target and relocation of a visible row.
		for _, id := range []uuid.UUID{f.visible, f.hidden} {
			w := f.request("POST", "/ui/document/ордер/"+id.String(), url.Values{"Номер": {"CHANGED"}, "Филиал": {"B"}})
			if w.Code != 403 {
				t.Fatalf("move %s: %d %s", id, w.Code, w.Body.String())
			}
		}
		if row, err := db.GetByID(f.ctx, f.orders.Name, f.visible, f.orders); err != nil || row["Филиал"] != "A" {
			t.Fatalf("move persisted: %v %v", row, err)
		}
		// Public DSL creation fills a simple resolved equality, then writes normally.
		got := runDSLRowAccessFunc(t, f.s, auth.ContextWithUser(f.ctx, f.user), `Функция Проверка() Экспорт
 О = Документы.Ордер.Создать();
 О.Номер = "CREATED";
 О.Записать();
 Возврат О.Филиал;
КонецФункции`)
		if got != "A" {
			t.Fatalf("autofill=%v", got)
		}
		for _, path := range []string{"/ui/document/ордер/" + f.hidden.String() + "/delete", "/ui/document/ордер/" + f.hidden.String() + "/post", "/ui/document/ордер/" + f.hidden.String() + "/unpost"} {
			w := f.request("POST", path, nil)
			if w.Code != 403 {
				t.Fatalf("hidden write %s: %d %s", path, w.Code, w.Body.String())
			}
		}
	})
}

func TestUserCardPublicWidgetCacheMatrix(t *testing.T) {
	dbtest.ForEachDialect(t, func(t *testing.T, db *storage.DB) {
		f := newCardFixture(t, db)
		runner := widget.New(f.s.reg, db)
		runner.User = f.user
		runner.CurrentUser = f.user.Login
		runner.Cache = widget.NewCache(time.Hour)
		w := &metadata.Widget{Name: "Orders", Type: metadata.WidgetTypeList, Query: "ВЫБРАТЬ Номер ИЗ Документ.Ордер"}
		a := runner.Run(f.ctx, w)
		if a.Error != "" || len(a.Rows) != 1 {
			t.Fatalf("widget=%+v", a)
		}
		f.saveCard(t, "B")
		b := runner.Run(f.ctx, w)
		if b.Error != "" || len(b.Rows) != 1 || fmt.Sprint(a.Rows) == fmt.Sprint(b.Rows) {
			t.Fatalf("cached stale widget: %+v -> %+v", a, b)
		}
	})
}

func TestUserCardPublicDSLTransactionMatrix(t *testing.T) {
	dbtest.ForEachDialect(t, func(t *testing.T, db *storage.DB) {
		f := newCardFixture(t, db)
		// This fixture grants explicit card writes only for the transaction probe.
		f.user.Roles[0].Permissions.Catalogs = map[string][]string{f.card.Name: {"read", "write"}}
		for _, finish := range []string{"ОтменитьТранзакцию", "ЗафиксироватьТранзакцию"} {
			f.saveCard(t, "A")
			src := fmt.Sprintf(`Функция Проверка() Экспорт
 З = Новый Запрос;
 З.Текст = "ВЫБРАТЬ Номер ИЗ Документ.Ордер ГДЕ Номер = 'HIDDEN'";
 НачатьТранзакцию();
 К = Справочники.Сотрудник.НайтиПоНаименованию("PRIVATE-CARD").ПолучитьОбъект();
 К.Филиал = "B";
 К.Записать();
 Р = З.Выполнить().Количество();
 %s();
 Возврат Р;
КонецФункции`, finish)
			got := runDSLRowAccessFunc(t, f.s, auth.ContextWithUser(f.ctx, f.user), src)
			if got != float64(1) {
				t.Fatalf("read own changes %s=%v", finish, got)
			}
			want := float64(0)
			if finish == "ЗафиксироватьТранзакцию" {
				want = 1
			}
			if got := f.dslCount(t, "ВЫБРАТЬ Номер ИЗ Документ.Ордер ГДЕ Номер = 'HIDDEN'"); got != want {
				t.Fatalf("after %s got %v want %v", finish, got, want)
			}
		}
	})
}

func TestUserCardPublicRESTAndReportMatrix(t *testing.T) {
	dbtest.ForEachDialect(t, func(t *testing.T, db *storage.DB) {
		f := newCardFixture(t, db)
		f.s.reg.Load(runtime.LoadOptions{Entities: []*metadata.Entity{f.card, f.orders}, Reports: []*report.Report{{Name: "Ордера", Query: "ВЫБРАТЬ Номер ИЗ Документ.Ордер"}}})
		f.user.Roles[0].Permissions.Reports = map[string][]string{"Ордера": {"run"}}
		repo := auth.NewRepo(db)
		if err := repo.SyncRoles(f.ctx, f.user.Roles); err != nil {
			t.Fatal(err)
		}
		if err := repo.AssignRole(f.ctx, f.user.ID, f.user.Roles[0].ID); err != nil {
			t.Fatal(err)
		}
		token, err := repo.CreateSession(f.ctx, f.user.ID, auth.SessionMeta{Kind: auth.SessionKindEnterprise})
		if err != nil {
			t.Fatal(err)
		}
		server := api.New(f.s.reg, db, f.s.interp, repo, "", 0, api.Config{}, f.s, nil)
		for _, tc := range []struct {
			path   string
			status int
		}{{"/api/v2/document/Ордер", 200}, {"/api/v2/document/Ордер/" + f.visible.String(), 200}, {"/api/v2/document/Ордер/" + f.hidden.String(), 403}, {"/api/v2/catalog/Сотрудник", 403}, {"/api/v2/report/Ордера", 200}} {
			r := httptest.NewRequest("GET", tc.path, nil)
			r.AddCookie(&http.Cookie{Name: "onebase_session", Value: token})
			w := httptest.NewRecorder()
			server.Handler().ServeHTTP(w, r)
			if w.Code != tc.status {
				t.Fatalf("%s: %d %s", tc.path, w.Code, w.Body.String())
			}
			if strings.Contains(w.Body.String(), "HIDDEN") || strings.Contains(w.Body.String(), "PRIVATE-CARD") {
				t.Fatalf("leaked %s: %s", tc.path, w.Body.String())
			}
		}
		// Export passes through the production handler, then inspect actual workbook.
		w := f.request("GET", "/ui/document/ордер/excel", nil)
		if w.Code != 200 {
			t.Fatalf("export: %d %s", w.Code, w.Body.String())
		}
		files, err := excelize.OpenReader(strings.NewReader(w.Body.String()))
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() {
			if err := files.Close(); err != nil {
				t.Error(err)
			}
		})
		rows, err := files.GetRows(files.GetSheetName(0))
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(fmt.Sprint(rows), "VISIBLE") || strings.Contains(fmt.Sprint(rows), "HIDDEN") {
			t.Fatalf("export=%v", rows)
		}
	})
}

func TestUserCardPublicSourceFailureMatrix(t *testing.T) {
	dbtest.ForEachDialect(t, func(t *testing.T, db *storage.DB) {
		f := newCardFixture(t, db)
		// A real storage failure at the source SELECT, rather than a mocked compiler.
		table := metadata.TableName(f.card.Name)
		if _, err := db.Exec(f.ctx, "ALTER TABLE "+table+" RENAME COLUMN филиал TO unavailable_field"); err != nil {
			t.Fatal(err)
		}
		restore := func() {
			if _, err := db.Exec(f.ctx, "ALTER TABLE "+table+" RENAME COLUMN unavailable_field TO филиал"); err != nil {
				t.Fatal(err)
			}
		}
		defer restore()
		w := f.request("GET", "/ui/document/ордер", nil)
		if w.Code != 403 || strings.Contains(w.Body.String(), "unavailable_field") || strings.Contains(w.Body.String(), "SELECT") {
			t.Fatalf("source failure exposed details/success: %d %s", w.Code, w.Body.String())
		}
		src := `Функция Проверка() Экспорт
 НачатьТранзакцию();
 О = Документы.Ордер.Создать();
 О.Номер = "MUST-NOT-PERSIST";
 О.Филиал = "A";
 О.Записать();
 ЗафиксироватьТранзакцию();
 Возврат Истина;
КонецФункции`
		prog := mustParse(t, src)
		vars, tx := f.s.buildDSLVarsTx(auth.ContextWithUser(f.ctx, f.user), runtime.NewMovementsCollector("test", uuid.Nil))
		err := f.s.interp.Run(prog.Procedures[0], nil, vars)
		interpreter.RollbackTxExecution(tx)
		if err == nil || strings.Contains(err.Error(), "SELECT") {
			t.Fatalf("DSL source failure=%v", err)
		}
		rows, err := db.List(f.ctx, f.orders.Name, f.orders, storage.ListParams{RowFilterEvaluated: true})
		if err != nil {
			t.Fatal(err)
		}
		if len(rows) != 2 {
			t.Fatalf("source error persisted rows %v", rows)
		}
		// Same failure in the public close-intent path preserves the dirty form.
		f.orders.Forms = []*metadata.FormModule{{Name: "Форма", EntityName: f.orders.Name, Kind: "object", LayoutKind: metadata.FormLayoutManaged}}
		body := closeIntentBody(uuid.NewString(), "save", "UNSAVED")
		body.Set("Номер", "UNSAVED")
		body.Set("Филиал", "A")
		body.Set("_id", f.visible.String())
		body.Set("_version", "1")
		body.Set("_close_mode", "save")
		ensureEntityCloseIntentSchema(f.orders, body)
		req := httptest.NewRequest("POST", "/ui/document/ордер/form-close-intent", strings.NewReader(body.Encode()))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		setEntityCloseIntentHeaders(req, body)
		close := httptest.NewRecorder()
		f.router.ServeHTTP(close, req)
		var response formEventResponse
		if err := json.Unmarshal(close.Body.Bytes(), &response); err != nil {
			t.Fatalf("close response %d %s", close.Code, close.Body.String())
		}
		if response.Error == "" || (response.Close != nil && (response.Close.Allowed || response.Close.Saved)) || response.SavedID != "" {
			t.Fatalf("source failure close success: %+v", response)
		}
	})
}

func TestUserCardPublicSSEMatrix(t *testing.T) {
	dbtest.ForEachDialect(t, func(t *testing.T, db *storage.DB) {
		f := newCardFixture(t, db)
		repo := auth.NewRepo(db)
		if err := repo.SyncRoles(f.ctx, f.user.Roles); err != nil {
			t.Fatal(err)
		}
		if err := repo.AssignRole(f.ctx, f.user.ID, f.user.Roles[0].ID); err != nil {
			t.Fatal(err)
		}
		f.s.authRepo = repo
		f.orders.NotifyChanges = true
		_, events, cancel := f.s.hub.Subscribe(f.user.ID, f.user.Login, nil)
		defer cancel()
		publisher := f.s.newChangePublisher()
		publisher.PublishChange(f.ctx, f.orders.Name, "записан", nil, map[string]any{"Филиал": "B"})
		if !emptyUI(t, events) {
			t.Fatal("foreign event leaked")
		}
		publisher.PublishChange(f.ctx, f.orders.Name, "записан", nil, map[string]any{"Филиал": "A"})
		if _, ok := recvUI(t, events); !ok {
			t.Fatal("own event missing")
		}
		f.saveCard(t, "B")
		publisher.PublishChange(f.ctx, f.orders.Name, "записан", nil, map[string]any{"Филиал": "A"})
		if !emptyUI(t, events) {
			t.Fatal("stale event leaked")
		}
		publisher.PublishChange(f.ctx, f.orders.Name, "записан", nil, map[string]any{"Филиал": "B"})
		if _, ok := recvUI(t, events); !ok {
			t.Fatal("new own event missing")
		}
	})
}

func TestUserCardPublicScalarTypesMatrix(t *testing.T) {
	for _, tc := range []struct {
		name       string
		typ        metadata.FieldType
		own, other any
	}{
		{"zero number", metadata.FieldTypeNumber, decimal.Zero, decimal.NewFromInt(1)},
		{"exact number", metadata.FieldTypeNumber, decimal.RequireFromString("1234567890.123456"), decimal.RequireFromString("1234567890.123457")},
		{"false boolean", metadata.FieldTypeBool, false, true},
		{"date", metadata.FieldTypeDate, time.Date(2026, 10, 12, 1, 2, 3, 0, time.UTC), time.Date(2026, 10, 13, 1, 2, 3, 0, time.UTC)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dbtest.ForEachDialect(t, func(t *testing.T, db *storage.DB) {
				f := newCardFixtureTyped(t, db, tc.typ, tc.own, tc.other)
				if got := f.dslCount(t, "ВЫБРАТЬ Номер ИЗ Документ.Ордер"); got != float64(1) {
					t.Fatalf("scalar %s got %v", tc.name, got)
				}
				for _, item := range []struct {
					id     uuid.UUID
					status int
				}{{f.visible, 200}, {f.hidden, 403}} {
					w := f.request("GET", "/ui/document/ордер/"+item.id.String(), nil)
					if w.Code != item.status {
						t.Fatalf("scalar %s direct=%d", tc.name, w.Code)
					}
				}
			})
		})
	}
}

func TestUserCardPublicDefaultsMatrix(t *testing.T) {
	dbtest.ForEachDialect(t, func(t *testing.T, db *storage.DB) {
		f := newCardFixture(t, db)
		goods := &metadata.Entity{Name: "Товар", Kind: metadata.KindCatalog, Fields: []metadata.Field{{Name: "Наименование", Type: metadata.FieldTypeString}, {Name: "Филиал", Type: metadata.FieldTypeString}}}
		f.orders.Fields = append(f.orders.Fields, metadata.Field{Name: "Товар", Type: metadata.FieldType("reference:Товар"), RefEntity: "Товар", Default: "единственный"})
		f.s.reg.Load(runtime.LoadOptions{Entities: []*metadata.Entity{f.card, f.orders, goods}})
		if err := db.Migrate(f.ctx, []*metadata.Entity{f.card, f.orders, goods}); err != nil {
			t.Fatal(err)
		}
		own, hidden := uuid.New(), uuid.New()
		for _, row := range []struct {
			id   uuid.UUID
			n, b string
		}{{own, "OWN-GOODS", "A"}, {hidden, "HIDDEN-GOODS", "B"}} {
			if err := db.Upsert(f.ctx, goods.Name, row.id, map[string]any{"Наименование": row.n, "Филиал": row.b}, goods); err != nil {
				t.Fatal(err)
			}
		}
		f.user.Roles[0].Permissions.Catalogs = map[string][]string{goods.Name: {"read"}}
		f.user.Roles[0].Permissions.RowAccess.Catalogs = map[string]auth.RowPolicies{goods.Name: {"read": f.user.Roles[0].Permissions.RowAccess.Documents[f.orders.Name]["read"]}}
		w := f.request("GET", "/ui/document/ордер/new", nil)
		if w.Code != 200 || !strings.Contains(w.Body.String(), own.String()) || strings.Contains(w.Body.String(), hidden.String()) {
			t.Fatalf("default leaks hidden goods: %d %s", w.Code, w.Body.String())
		}
		if err := db.Delete(f.ctx, f.card.Name, f.cardID); err != nil {
			t.Fatal(err)
		}
		w = f.request("GET", "/ui/document/ордер/new", nil)
		if strings.Contains(w.Body.String(), own.String()) || strings.Contains(w.Body.String(), hidden.String()) {
			t.Fatalf("unavailable default: %s", w.Body.String())
		}
	})
}

func TestUserCardPublicReferenceAndEmptyMatrix(t *testing.T) {
	dbtest.ForEachDialect(t, func(t *testing.T, db *storage.DB) {
		own, other := uuid.NewString(), uuid.NewString()
		f := newCardFixtureTyped(t, db, metadata.FieldType("reference:Филиал"), own, other)
		if got := f.dslCount(t, "ВЫБРАТЬ Номер ИЗ Документ.Ордер"); got != float64(1) {
			t.Fatalf("reference=%v", got)
		}
		f.saveCard(t, nil)
		policy := f.user.Roles[0].Permissions.RowAccess.Documents[f.orders.Name]["read"]
		policy.Op = "ne"
		f.user.Roles[0].Permissions.RowAccess.Documents[f.orders.Name]["read"] = policy
		if got := f.dslCount(t, "ВЫБРАТЬ Номер ИЗ Документ.Ордер"); got != float64(0) {
			t.Fatalf("empty reference ne=%v", got)
		}
	})
}

func TestUserCardPublicAllowedMutationsMatrix(t *testing.T) {
	dbtest.ForEachDialect(t, func(t *testing.T, db *storage.DB) {
		f := newCardFixture(t, db)
		f.orders.Posting = true
		// Own branch update succeeds; hidden branch is tested separately above.
		w := f.request("POST", "/ui/document/ордер/"+f.visible.String(), url.Values{"Номер": {"UPDATED"}, "Филиал": {"A"}})
		if w.Code != 303 {
			t.Fatalf("own update: %d %s", w.Code, w.Body.String())
		}
		for _, operation := range []string{"post", "unpost"} {
			w := f.request("POST", "/ui/document/ордер/"+f.visible.String()+"/"+operation, nil)
			if w.Code != 303 {
				t.Fatalf("own %s: %d %s", operation, w.Code, w.Body.String())
			}
			row, err := db.GetByID(f.ctx, f.orders.Name, f.visible, f.orders)
			if err != nil {
				t.Fatal(err)
			}
			if row["posted"] != (operation == "post") {
				t.Fatalf("posting state=%v", row)
			}
		}
		w = f.request("POST", "/ui/document/ордер/"+f.visible.String()+"/delete", url.Values{"hard": {"1"}})
		if w.Code != 303 {
			t.Fatalf("own delete: %d %s", w.Code, w.Body.String())
		}
	})
}

func TestUserCardPublicTwoEmployeesMatrix(t *testing.T) {
	dbtest.ForEachDialect(t, func(t *testing.T, db *storage.DB) {
		f := newCardFixture(t, db)
		second := &auth.User{ID: uuid.NewString(), Login: "second", Roles: f.user.Roles}
		if _, err := db.Exec(f.ctx, fmt.Sprintf("INSERT INTO _users(id,login,password_hash) VALUES(%s,%s,%s)", db.Dialect().Placeholder(1), db.Dialect().Placeholder(2), db.Dialect().Placeholder(3)), second.ID, second.Login, "unused"); err != nil {
			t.Fatal(err)
		}
		if err := db.Upsert(f.ctx, f.card.Name, uuid.New(), map[string]any{"Наименование": "PRIVATE-SECOND", "Учётка": second.ID, "Филиал": "B"}, f.card); err != nil {
			t.Fatal(err)
		}
		for _, user := range []*auth.User{f.user, second} {
			r := chi.NewRouter()
			r.Use(func(next http.Handler) http.Handler {
				return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					next.ServeHTTP(w, r.WithContext(auth.ContextWithUser(r.Context(), user)))
				})
			})
			f.s.Mount(r)
			w := httptest.NewRecorder()
			r.ServeHTTP(w, httptest.NewRequest("GET", "/ui/document/ордер", nil))
			own, hidden := "VISIBLE", "HIDDEN"
			if user == second {
				own, hidden = hidden, own
			}
			if w.Code != 200 || !strings.Contains(w.Body.String(), own) || strings.Contains(w.Body.String(), hidden) {
				t.Fatalf("employee %s isolation: %d %s", user.Login, w.Code, w.Body.String())
			}
		}
	})
}

// Persisted JSON permissions go through the real auth reader before HTTP RBAC.
func TestUserCardPublicHTTPRejectsNullSourceMatrix(t *testing.T) {
	for name, policy := range map[string]string{
		"ne":           `{"field":"Филиал","op":"ne","value":{"user_card":null}}`,
		"not":          `{"not":{"field":"Филиал","op":"eq","value":{"user_card":null}}}`,
		"any":          `{"any":[{"field":"Филиал","op":"eq","value":{"user_card":null}},{"field":"Филиал","op":"eq","value":{"literal":"A"}}]}`,
		"literal null": `{"field":"Филиал","op":"ne","value":{"literal":null}}`,
	} {
		t.Run(name, func(t *testing.T) {
			dbtest.ForEachDialect(t, func(t *testing.T, db *storage.DB) {
				f := newCardFixture(t, db)
				permissions := `{"processors":{},"documents":{"Ордер":["read"]},"row_access":{"documents":{"Ордер":{"read":` + policy + `}}}}`
				roleID := uuid.NewString()
				q := fmt.Sprintf("INSERT INTO _roles (id, name, permissions) VALUES (%s,%s,%s)", db.Dialect().Placeholder(1), db.Dialect().Placeholder(2), db.Dialect().Placeholder(3))
				if _, err := db.Exec(f.ctx, q, roleID, "Stored", permissions); err != nil {
					t.Fatal(err)
				}
				repo := auth.NewRepo(db)
				if err := repo.AssignRole(f.ctx, f.user.ID, roleID); err != nil {
					t.Fatal(err)
				}
				roles, err := repo.GetRolesForUser(f.ctx, f.user.ID)
				if err != nil {
					t.Fatal(err)
				}
				if len(roles) != 1 {
					t.Fatalf("roles: %v", roles)
				}
				f.user.Roles = roles
				w := f.request(http.MethodGet, "/ui/document/ордер", nil)
				if name == "literal null" {
					if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), "VISIBLE") || !strings.Contains(w.Body.String(), "HIDDEN") {
						t.Fatalf("literal null lost: %d %s", w.Code, w.Body.String())
					}
				} else if w.Code != http.StatusForbidden || strings.Contains(w.Body.String(), "VISIBLE") || strings.Contains(w.Body.String(), "HIDDEN") {
					t.Fatalf("missing source disclosed rows: %d %s", w.Code, w.Body.String())
				}
			})
		})
	}
}

func TestUserCardPublicEnumSourceMatrix(t *testing.T) {
	dbtest.ForEachDialect(t, func(t *testing.T, db *storage.DB) {
		f := newCardFixtureTyped(t, db, metadata.FieldType("enum:Филиал"), "A", "B")
		eq := f.user.Roles[0].Permissions.RowAccess.Documents[f.orders.Name]["read"]
		ne := eq
		ne.Op = "ne"
		policies := []struct {
			name                        string
			policy                      auth.RowPolicy
			count                       float64
			visibleStatus, hiddenStatus int
		}{
			{"eq", eq, 1, 200, 403},
			{"ne", ne, 1, 403, 200},
			{"not", auth.RowPolicy{Not: &eq}, 1, 403, 200},
			{"any", auth.RowPolicy{Any: []auth.RowPolicy{eq, {Field: "Филиал", Op: "eq", Value: auth.RowValue{Literal: "B"}}}}, 2, 200, 200},
		}
		for _, source := range []struct {
			name       string
			value      any
			valid, raw bool
		}{
			{"canonical", "A", true, false},
			{"null", nil, false, false},
			{"empty", "", false, false},
			{"space", " ", false, false},
			{"whitespace", "\t\n", false, false},
			{"nil marker", "<nil>", false, false},
			{"padded nil marker", " <nil> ", false, false},
			{"trailing space", "A ", false, false},
			{"leading space", " A", false, false},
			{"unicode space", "A\u00a0", false, false},
			{"unknown", "INVALID", false, true},
			{"case mismatch", "a", false, true},
		} {
			t.Run(source.name, func(t *testing.T) {
				if source.raw {
					// Imported/corrupt data can bypass the ordinary write backstop.
					_, err := db.Exec(f.ctx, fmt.Sprintf("UPDATE %s SET филиал = %s WHERE id = %s", metadata.TableName(f.card.Name), db.Dialect().Placeholder(1), db.Dialect().Placeholder(2)), source.value, f.cardID.String())
					if err != nil {
						t.Fatal(err)
					}
				} else {
					// Empty placeholders and padded identifiers are accepted by Upsert.
					f.saveCard(t, source.value)
				}
				for _, p := range policies {
					t.Run(p.name, func(t *testing.T) {
						f.user.Roles[0].Permissions.RowAccess.Documents[f.orders.Name]["read"] = p.policy
						want := float64(0)
						if source.valid {
							want = p.count
						}
						if got := f.dslCount(t, "ВЫБРАТЬ Номер ИЗ Документ.Ордер"); got != want {
							t.Errorf("DSL returned %v rows, want %v", got, want)
						}
						for _, row := range []struct {
							id     uuid.UUID
							status int
						}{{f.visible, p.visibleStatus}, {f.hidden, p.hiddenStatus}} {
							status := http.StatusForbidden
							if source.valid {
								status = row.status
							}
							w := f.request("GET", "/ui/document/ордер/"+row.id.String(), nil)
							if w.Code != status {
								t.Errorf("HTTP card %s = %d, want %d", row.id, w.Code, status)
							}
						}
						if !source.valid {
							w := f.request("GET", "/ui/document/ордер", nil)
							if (w.Code != http.StatusOK && w.Code != http.StatusForbidden) || strings.Contains(w.Body.String(), "VISIBLE") || strings.Contains(w.Body.String(), "HIDDEN") {
								t.Errorf("HTTP list leaked rows or returned unexpected status: %d", w.Code)
							}
						}
					})
				}
			})
		}
	})
}

func TestUserCardPublicStringWhitespaceMatrix(t *testing.T) {
	for _, value := range []string{" ", "<nil>", "A "} {
		t.Run(value, func(t *testing.T) {
			dbtest.ForEachDialect(t, func(t *testing.T, db *storage.DB) {
				f := newCardFixtureTyped(t, db, metadata.FieldTypeString, value, "B")
				if got := f.dslCount(t, "ВЫБРАТЬ Номер ИЗ Документ.Ордер"); got != float64(1) {
					t.Fatalf("string equality count = %v", got)
				}
				if w := f.request("GET", "/ui/document/ордер/"+f.visible.String(), nil); w.Code != http.StatusOK {
					t.Fatalf("string equality HTTP = %d", w.Code)
				}
			})
		})
	}
}
