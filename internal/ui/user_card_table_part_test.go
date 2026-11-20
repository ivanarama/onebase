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

func newTableCardFixture(t *testing.T, db *storage.DB) cardFixture {
	t.Helper()
	return newTableCardFixtureTyped(t, db, metadata.FieldTypeString, "A", "B")
}

func newTableCardFixtureTyped(t *testing.T, db *storage.DB, typ metadata.FieldType, own, other any) cardFixture {
	t.Helper()
	f := newCardFixtureTyped(t, db, typ, own, other)
	f.card.TableParts = []metadata.TablePart{{Name: "Филиалы", Fields: []metadata.Field{f.card.Fields[2]}}}
	if err := db.Migrate(f.ctx, []*metadata.Entity{f.card, f.orders}); err != nil {
		t.Fatal(err)
	}
	p := auth.RowPolicy{Field: "Филиал", Op: "in", Value: auth.RowValue{UserCard: &auth.UserCardSource{
		Catalog: f.card.Name, AccountField: "Учётка", TablePart: "Филиалы", Column: "Филиал",
	}}}
	f.user.Roles[0].Permissions.RowAccess.Documents[f.orders.Name]["read"] = p
	f.saveBranches(t, own, other, own)
	return f
}

func (f cardFixture) saveBranches(t *testing.T, branches ...any) {
	t.Helper()
	rows := make([]map[string]any, len(branches))
	for i, branch := range branches {
		rows[i] = map[string]any{"Филиал": branch}
	}
	if err := f.s.store.UpsertTablePartRows(f.ctx, f.card.Name, "Филиалы", f.cardID, rows, f.card.TableParts[0]); err != nil {
		t.Fatal(err)
	}
}

func TestUserCardTablePartPublicReadMatrix(t *testing.T) {
	dbtest.ForEachDialect(t, func(t *testing.T, db *storage.DB) {
		f := newTableCardFixture(t, db)
		third := uuid.New()
		if err := db.Upsert(f.ctx, f.orders.Name, third, map[string]any{"Номер": "THIRD-BRANCH", "Филиал": "C"}, f.orders); err != nil {
			t.Fatal(err)
		}
		w := f.request(http.MethodGet, "/ui/document/ордер", nil)
		if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), "VISIBLE") || !strings.Contains(w.Body.String(), "HIDDEN") || strings.Contains(w.Body.String(), "THIRD-BRANCH") || strings.Contains(w.Body.String(), "PRIVATE-CARD") {
			t.Fatalf("table-part public list: status=%d body=%s", w.Code, w.Body.String())
		}
		if got := f.dslCount(t, "ВЫБРАТЬ Номер ИЗ Документ.Ордер"); got != float64(2) {
			t.Fatalf("table-part DSL count=%v, want 2", got)
		}
		for _, query := range []string{
			"ВЫБРАТЬ А.Номер ИЗ Документ.Ордер КАК А ЛЕВОЕ СОЕДИНЕНИЕ Документ.Ордер КАК Б ПО А.Ссылка = Б.Ссылка",
			"ВЫБРАТЬ Номер ИЗ Документ.Ордер ОБЪЕДИНИТЬ ВСЕ ВЫБРАТЬ Номер ИЗ Документ.Ордер",
		} {
			want := float64(2)
			if strings.Contains(query, "ОБЪЕДИНИТЬ") {
				want = 4
			}
			if got := f.dslCount(t, query); got != want {
				t.Fatalf("query %s: %v", query, got)
			}
		}
		for _, tc := range []struct {
			id     uuid.UUID
			status int
		}{{f.visible, 200}, {f.hidden, 200}, {third, 403}} {
			if w := f.request("GET", "/ui/document/ордер/"+tc.id.String(), nil); w.Code != tc.status {
				t.Fatalf("open %s: %d", tc.id, w.Code)
			}
		}
		if w := f.request("GET", "/ui/catalog/сотрудник", nil); w.Code != 403 {
			t.Fatalf("private card: %d", w.Code)
		}
		// Changing the set affects the next operation in the same session.
		f.saveBranches(t, "B")
		f.assertTableCount(t, 1)
		if w := f.request("GET", "/ui/document/ордер/"+f.visible.String(), nil); w.Code != 403 {
			t.Fatalf("stale source allowed A: %d", w.Code)
		}
	})
}

// The picker uses SQL filtering; direct opens and writes use the in-memory
// matcher. Check both public routes, rather than calling either compiler.
func (f cardFixture) assertTableCount(t *testing.T, want int) {
	t.Helper()
	w := f.request("GET", "/ui/_ref-options/ордер", nil)
	var choices choiceHTTPResponse
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &choices) != nil || choices.Total != want || len(choices.Items) != want {
		t.Fatalf("picker want=%d got=%d %s", want, w.Code, w.Body.String())
	}
	if got := f.dslCount(t, "ВЫБРАТЬ Номер ИЗ Документ.Ордер"); got != float64(want) {
		t.Fatalf("DSL want=%d got=%v", want, got)
	}
}

func TestUserCardTablePartUnavailableMatrix(t *testing.T) {
	dbtest.ForEachDialect(t, func(t *testing.T, db *storage.DB) {
		f := newTableCardFixture(t, db)
		base := f.user.Roles[0].Permissions.RowAccess.Documents[f.orders.Name]["read"]
		for _, state := range []string{"empty", "null", "invalid", "missing", "duplicate", "folder", "deleted", "1000", "1001"} {
			t.Run(state, func(t *testing.T) {
				f.saveCard(t, "A")
				f.saveBranches(t, "A", "B", "A")
				restore := func() {}
				switch state {
				case "empty":
					f.saveBranches(t)
				case "null":
					f.saveBranches(t, "A", nil)
				case "invalid":
					f.saveBranches(t, "A", "")
				case "missing":
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
					column := "is_folder"
					if state == "deleted" {
						column = "deletion_mark"
					}
					query := fmt.Sprintf("UPDATE %s SET %s = %s WHERE id = %s", metadata.TableName(f.card.Name), column, db.Dialect().Placeholder(1), db.Dialect().Placeholder(2))
					if _, err := db.Exec(f.ctx, query, true, f.cardID.String()); err != nil {
						t.Fatal(err)
					}
					restore = func() {
						if _, err := db.Exec(f.ctx, query, false, f.cardID.String()); err != nil {
							t.Fatal(err)
						}
					}
				case "1000", "1001":
					n := 1000
					if state == "1001" {
						n++
					}
					values := make([]any, n)
					for i := range values {
						values[i] = "A"
					}
					f.saveBranches(t, values...)
				}
				defer restore()
				negative := base
				negative.Op = "not_in"
				for name, policy := range map[string]auth.RowPolicy{
					"in": base, "not_in": negative, "not": {Not: &base}, "nested": {Not: &auth.RowPolicy{Not: &base}},
					"any": {Any: []auth.RowPolicy{base, {Field: "Филиал", Op: "eq", Value: auth.RowValue{Literal: "A"}}}},
				} {
					t.Run(name, func(t *testing.T) {
						f.user.Roles[0].Permissions.RowAccess.Documents[f.orders.Name]["read"] = policy
						want := 0
						if state == "1000" {
							want = 1
						}
						f.assertTableCount(t, want)
						if state != "1000" {
							for _, id := range []uuid.UUID{f.visible, f.hidden} {
								if w := f.request("GET", "/ui/document/ордер/"+id.String(), nil); w.Code != 403 {
									t.Fatalf("unavailable direct open: %d", w.Code)
								}
							}
						}
					})
				}
			})
		}
	})
}

func TestUserCardTablePartTypesMatrix(t *testing.T) {
	for _, tc := range []struct {
		name       string
		typ        metadata.FieldType
		own, other any
	}{
		{"number zero", metadata.FieldTypeNumber, decimal.Zero, decimal.RequireFromString("1.25")},
		{"number fraction", metadata.FieldTypeNumber, decimal.RequireFromString("9007199254740993.125"), decimal.RequireFromString("9007199254740993.126")},
		{"false", metadata.FieldTypeBool, false, true},
		{"reference", metadata.FieldType("reference:Филиал"), uuid.NewString(), uuid.NewString()},
		{"enum", metadata.FieldType("enum:Филиалы"), "A", "B"},
		{"date", metadata.FieldTypeDate, time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC), time.Date(2026, 1, 2, 0, 0, 0, 0, time.UTC)},
		{"spaces", metadata.FieldTypeString, " ", "B"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dbtest.ForEachDialect(t, func(t *testing.T, db *storage.DB) {
				f := newTableCardFixtureTyped(t, db, tc.typ, tc.own, tc.other)
				f.saveBranches(t, tc.own, tc.own)
				f.assertTableCount(t, 1)
				for _, pair := range []struct {
					id     uuid.UUID
					status int
				}{{f.visible, 200}, {f.hidden, 403}} {
					if w := f.request("GET", "/ui/document/ордер/"+pair.id.String(), nil); w.Code != pair.status {
						t.Fatalf("typed matcher: %d", w.Code)
					}
				}
				p := f.user.Roles[0].Permissions.RowAccess.Documents[f.orders.Name]["read"]
				p.Op = "not_in"
				f.user.Roles[0].Permissions.RowAccess.Documents[f.orders.Name]["read"] = p
				f.assertTableCount(t, 1)
				if w := f.request("GET", "/ui/document/ордер/"+f.visible.String(), nil); w.Code != 403 {
					t.Fatalf("typed negative matcher: %d", w.Code)
				}
			})
		})
	}
}

func TestUserCardTablePartPublicMutationsMatrix(t *testing.T) {
	dbtest.ForEachDialect(t, func(t *testing.T, db *storage.DB) {
		f := newTableCardFixture(t, db)
		f.orders.Posting = true
		f.saveBranches(t, "A")
		if w := f.request("POST", "/ui/document/ордер/"+f.visible.String(), url.Values{"Номер": {"CHANGED"}, "Филиал": {"B"}}); w.Code != 403 {
			t.Fatalf("move to foreign branch=%d", w.Code)
		}
		row, err := db.GetByID(f.ctx, f.orders.Name, f.visible, f.orders)
		if err != nil || row["Филиал"] != "A" || row["Номер"] != "VISIBLE" {
			t.Fatalf("denied write persisted: %v %v", row, err)
		}
		for _, op := range []string{"post", "unpost", "delete"} {
			if w := f.request("POST", "/ui/document/ордер/"+f.hidden.String()+"/"+op, nil); w.Code != 403 {
				t.Fatalf("foreign %s=%d", op, w.Code)
			}
		}
		if w := f.request("POST", "/ui/document/ордер/new", url.Values{"Номер": {"NEW-FOREIGN"}, "Филиал": {"B"}}); w.Code != 403 {
			t.Fatalf("create foreign=%d", w.Code)
		}
		if w := f.request("POST", "/ui/document/ордер/"+f.visible.String(), url.Values{"Номер": {"CHANGED"}, "Филиал": {"A"}}); w.Code != 303 {
			t.Fatalf("own write=%d %s", w.Code, w.Body.String())
		}
		for _, op := range []string{"post", "unpost"} {
			if w := f.request("POST", "/ui/document/ордер/"+f.visible.String()+"/"+op, nil); w.Code != 303 {
				t.Fatalf("own %s=%d", op, w.Code)
			}
		}
		if w := f.request("POST", "/ui/document/ордер/"+f.visible.String()+"/delete", url.Values{"hard": {"1"}}); w.Code != 303 {
			t.Fatalf("own delete=%d", w.Code)
		}
	})
}

func TestUserCardTablePartPublicRESTExportMatrix(t *testing.T) {
	dbtest.ForEachDialect(t, func(t *testing.T, db *storage.DB) {
		f := newTableCardFixture(t, db)
		f.saveBranches(t, "A")
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
			if w.Code != tc.status || strings.Contains(w.Body.String(), "PRIVATE-CARD") || strings.Contains(w.Body.String(), "HIDDEN") {
				t.Fatalf("REST %s=%d %s", tc.path, w.Code, w.Body.String())
			}
		}
		w := f.request("GET", "/ui/document/ордер/excel", nil)
		if w.Code != 200 {
			t.Fatalf("export %d", w.Code)
		}
		x, err := excelize.OpenReader(strings.NewReader(w.Body.String()))
		if err != nil {
			t.Fatal(err)
		}
		defer func() {
			if err := x.Close(); err != nil {
				t.Error(err)
			}
		}()
		rows, err := x.GetRows(x.GetSheetName(0))
		if err != nil || !strings.Contains(fmt.Sprint(rows), "VISIBLE") || strings.Contains(fmt.Sprint(rows), "HIDDEN") {
			t.Fatalf("workbook %v %v", rows, err)
		}
	})
}

func TestUserCardTablePartDSLTransactionMatrix(t *testing.T) {
	dbtest.ForEachDialect(t, func(t *testing.T, db *storage.DB) {
		f := newTableCardFixture(t, db)
		f.user.Roles[0].Permissions.Catalogs = map[string][]string{f.card.Name: {"read", "write"}}
		peer := tableCardConcurrentDB(t, db)
		for _, finish := range []string{"ОтменитьТранзакцию", "ЗафиксироватьТранзакцию"} {
			f.saveBranches(t, "A")
			src := fmt.Sprintf(`Функция Проверка() Экспорт
 З=Новый Запрос; З.Текст="ВЫБРАТЬ Номер ИЗ Документ.Ордер ГДЕ Номер = 'HIDDEN'";
 НачатьТранзакцию();
 К=Справочники.Сотрудник.НайтиПоНаименованию("PRIVATE-CARD").ПолучитьОбъект();
 К.Филиалы.Очистить(); С=К.Филиалы.Добавить(); С.Филиал="B"; К.Записать();
 Р=З.Выполнить().Количество();
 ПроверитьИзоляцию();
 %s(); Возврат Р;
КонецФункции`, finish)
			prog := mustParse(t, src)
			var result any
			vars, tx := f.s.buildDSLVarsTx(auth.ContextWithUser(f.ctx, f.user), runtime.NewMovementsCollector("test", uuid.Nil))
			defer interpreter.RollbackTxExecution(tx)
			vars["проверитьизоляцию"] = interpreter.BuiltinFunc(func(_ []any, _ string, _ int) (any, error) {
				// A separate public operation must not see uncommitted rights.
				desc := storage.UserCardDescriptor{Catalog: f.card, AccountField: f.card.Fields[1], Field: f.card.TableParts[0].Fields[0], TablePart: &f.card.TableParts[0]}
				value, err := peer.ReadUserCardSource(f.ctx, desc, f.user.ID)
				if err != nil {
					return nil, err
				}
				if !value.Available || len(value.Values) != 1 || value.Values[0] != "A" {
					return nil, fmt.Errorf("uncommitted source escaped: %+v", value)
				}
				return nil, nil
			})
			if err := f.s.interp.RunWithResult(prog.Procedures[0], nil, &result, vars); err != nil {
				t.Fatal(err)
			}
			if result != float64(1) {
				t.Fatalf("read your writes=%v", result)
			}
			want := float64(0)
			if finish == "ЗафиксироватьТранзакцию" {
				want = 1
			}
			if got := f.dslCount(t, "ВЫБРАТЬ Номер ИЗ Документ.Ордер ГДЕ Номер = 'HIDDEN'"); got != want {
				t.Fatalf("after %s=%v", finish, got)
			}
		}
	})
}

func TestUserCardTablePartPublicFailureMatrix(t *testing.T) {
	dbtest.ForEachDialect(t, func(t *testing.T, db *storage.DB) {
		f := newTableCardFixture(t, db)
		table := metadata.TablePartTableName(f.card.Name, "Филиалы")
		if _, err := db.Exec(f.ctx, "ALTER TABLE "+table+" RENAME COLUMN филиал TO unavailable_field"); err != nil {
			t.Fatal(err)
		}
		defer func() {
			if _, err := db.Exec(f.ctx, "ALTER TABLE "+table+" RENAME COLUMN unavailable_field TO филиал"); err != nil {
				t.Error(err)
			}
		}()
		w := f.request("GET", "/ui/document/ордер", nil)
		if w.Code != 403 || strings.Contains(w.Body.String(), "unavailable_field") || strings.Contains(w.Body.String(), "SELECT") {
			t.Fatalf("source error exposed success/details: %d %s", w.Code, w.Body.String())
		}
		prog := mustParse(t, `Функция Проверка() Экспорт
 НачатьТранзакцию(); О=Документы.Ордер.Создать();
 О.Номер="MUST-NOT-PERSIST"; О.Филиал="A"; О.Записать();
 ЗафиксироватьТранзакцию(); Возврат Истина;
КонецФункции`)
		vars, tx := f.s.buildDSLVarsTx(auth.ContextWithUser(f.ctx, f.user), runtime.NewMovementsCollector("test", uuid.Nil))
		err := f.s.interp.Run(prog.Procedures[0], nil, vars)
		interpreter.RollbackTxExecution(tx)
		if err == nil || strings.Contains(err.Error(), "SELECT") {
			t.Fatalf("source error not propagated safely: %v", err)
		}
		rows, err := db.List(f.ctx, f.orders.Name, f.orders, storage.ListParams{RowFilterEvaluated: true})
		if err != nil || len(rows) != 2 {
			t.Fatalf("failed source persisted writes: %v %v", rows, err)
		}
	})
	t.Run("cancelled HTTP", func(t *testing.T) {
		dbtest.ForEachDialect(t, func(t *testing.T, db *storage.DB) {
			f := newTableCardFixture(t, db)
			ctx, cancel := context.WithCancel(f.ctx)
			cancel()
			r := httptest.NewRequest("GET", "/ui/document/ордер", nil).WithContext(ctx)
			w := httptest.NewRecorder()
			f.router.ServeHTTP(w, r)
			if w.Code != 403 {
				t.Fatalf("cancelled lookup=%d", w.Code)
			}
		})
	})
}

func TestUserCardTablePartRoleORAndReaderContextsMatrix(t *testing.T) {
	dbtest.ForEachDialect(t, func(t *testing.T, db *storage.DB) {
		f := newTableCardFixture(t, db)
		f.saveBranches(t)
		original := f.user.Roles[0]
		other := &auth.Role{Name: "Other", Permissions: auth.Permission{Documents: map[string][]string{f.orders.Name: {"read"}}, RowAccess: auth.RowAccess{Documents: map[string]auth.RowPolicies{f.orders.Name: {"read": {Field: "Филиал", Op: "eq", Value: auth.RowValue{Literal: "B"}}}}}}}
		for _, roles := range [][]*auth.Role{{original, other}, {other, original}} {
			f.user.Roles = roles
			f.assertTableCount(t, 1)
		}
		other.Permissions.RowAccess = auth.RowAccess{}
		for _, roles := range [][]*auth.Role{{original, other}, {other, original}} {
			f.user.Roles = roles
			f.assertTableCount(t, 2)
		}
		f.user.Roles = []*auth.Role{original}
		f.saveBranches(t, "A", "A")
		desc := storage.UserCardDescriptor{Catalog: f.card, AccountField: f.card.Fields[1], Field: f.card.TableParts[0].Fields[0], TablePart: &f.card.TableParts[0]}
		check := func(ctx context.Context) error {
			v, err := db.ReadUserCardSource(ctx, desc, f.user.ID)
			if err != nil {
				return err
			}
			if !v.Available || len(v.Values) != 1 || v.Values[0] != "A" {
				return fmt.Errorf("typed dedupe=%+v", v)
			}
			copy := f
			copy.ctx = ctx
			if got := copy.dslCount(t, "ВЫБРАТЬ Номер ИЗ Документ.Ордер"); got != float64(1) {
				return fmt.Errorf("context lost: %v", got)
			}
			return nil
		}
		if err := check(f.ctx); err != nil {
			t.Fatal(err)
		}
		if err := db.WithReadSnapshot(f.ctx, check); err != nil {
			t.Fatal(err)
		}
		if err := db.WithTx(f.ctx, check); err != nil {
			t.Fatal(err)
		}
	})
}

func TestUserCardTablePartLiveSourcesMatrix(t *testing.T) {
	dbtest.ForEachDialect(t, func(t *testing.T, db *storage.DB) {
		f := newTableCardFixture(t, db)
		f.saveBranches(t, "A")
		runner := widget.New(f.s.reg, db)
		runner.User = f.user
		runner.CurrentUser = f.user.Login
		runner.Cache = widget.NewCache(time.Hour)
		w := &metadata.Widget{Name: "Orders", Type: metadata.WidgetTypeList, Query: "ВЫБРАТЬ Номер ИЗ Документ.Ордер"}
		a := runner.Run(f.ctx, w)
		if a.Error != "" || len(a.Rows) != 1 {
			t.Fatalf("widget=%+v", a)
		}
		f.saveBranches(t, "B")
		b := runner.Run(f.ctx, w)
		if b.Error != "" || len(b.Rows) != 1 || fmt.Sprint(a.Rows) == fmt.Sprint(b.Rows) {
			t.Fatalf("stale set cache: %+v -> %+v", a, b)
		}
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
		publisher.PublishChange(f.ctx, f.orders.Name, "записан", nil, map[string]any{"Филиал": "A"})
		if !emptyUI(t, events) {
			t.Fatal("foreign set SSE leaked")
		}
		publisher.PublishChange(f.ctx, f.orders.Name, "записан", nil, map[string]any{"Филиал": "B"})
		if _, ok := recvUI(t, events); !ok {
			t.Fatal("own set SSE missing")
		}
		f.saveBranches(t, "A")
		publisher.PublishChange(f.ctx, f.orders.Name, "записан", nil, map[string]any{"Филиал": "B"})
		if !emptyUI(t, events) {
			t.Fatal("stale set SSE leaked")
		}
		publisher.PublishChange(f.ctx, f.orders.Name, "записан", nil, map[string]any{"Филиал": "A"})
		if _, ok := recvUI(t, events); !ok {
			t.Fatal("changed set SSE missing")
		}
	})
}

func TestUserCardTablePartDSLOwnAccountChangeMatrix(t *testing.T) {
	dbtest.ForEachDialect(t, func(t *testing.T, db *storage.DB) {
		f := newTableCardFixture(t, db)
		peer := tableCardConcurrentDB(t, db)
		f.user.Roles[0].Permissions.Catalogs = map[string][]string{f.card.Name: {"read", "write"}}
		secondUser := uuid.NewString()
		if _, err := db.Exec(f.ctx, fmt.Sprintf("INSERT INTO _users(id,login,password_hash) VALUES(%s,%s,%s)", db.Dialect().Placeholder(1), db.Dialect().Placeholder(2), db.Dialect().Placeholder(3)), secondUser, "second", "unused"); err != nil {
			t.Fatal(err)
		}
		for _, finish := range []string{"ОтменитьТранзакцию", "ЗафиксироватьТранзакцию"} {
			f.saveCard(t, "A")
			f.saveBranches(t, "A")
			source := fmt.Sprintf(`Функция Проверка() Экспорт
 З=Новый Запрос;З.Текст="ВЫБРАТЬ Номер ИЗ Документ.Ордер";
 НачатьТранзакцию();
 К=Справочники.Сотрудник.НайтиПоНаименованию("PRIVATE-CARD").ПолучитьОбъект();
 К.Учётка="%s";К.Филиалы.Очистить();С=К.Филиалы.Добавить();С.Филиал="B";К.Записать();
 Р=З.Выполнить().Количество();ПроверитьСвязь();%s();Возврат Р;
КонецФункции`, secondUser, finish)
			prog := mustParse(t, source)
			var result any
			vars, tx := f.s.buildDSLVarsTx(auth.ContextWithUser(f.ctx, f.user), runtime.NewMovementsCollector("test", uuid.Nil))
			defer interpreter.RollbackTxExecution(tx)
			vars["проверитьсвязь"] = interpreter.BuiltinFunc(func(_ []any, _ string, _ int) (any, error) {
				desc := storage.UserCardDescriptor{Catalog: f.card, AccountField: f.card.Fields[1], Field: f.card.TableParts[0].Fields[0], TablePart: &f.card.TableParts[0]}
				value, err := peer.ReadUserCardSource(f.ctx, desc, f.user.ID)
				if err != nil {
					return nil, err
				}
				if !value.Available || len(value.Values) != 1 || value.Values[0] != "A" {
					return nil, fmt.Errorf("uncommitted account/set escaped: %+v", value)
				}
				return nil, nil
			})
			if err := f.s.interp.RunWithResult(prog.Procedures[0], nil, &result, vars); err != nil {
				t.Fatal(err)
			}
			if result != float64(0) {
				t.Fatalf("own changed link retained rights=%v", result)
			}
			want := 1
			if finish == "ЗафиксироватьТранзакцию" {
				want = 0
			}
			f.assertTableCount(t, want)
		}
	})
}

func TestUserCardTablePartTwoEmployeesMatrix(t *testing.T) {
	dbtest.ForEachDialect(t, func(t *testing.T, db *storage.DB) {
		f := newTableCardFixture(t, db)
		f.saveBranches(t, "A")
		first, second := f.user.ID, uuid.NewString()
		if _, err := db.Exec(f.ctx, fmt.Sprintf("INSERT INTO _users(id,login,password_hash) VALUES(%s,%s,%s)", db.Dialect().Placeholder(1), db.Dialect().Placeholder(2), db.Dialect().Placeholder(3)), second, "second", "unused"); err != nil {
			t.Fatal(err)
		}
		card := uuid.New()
		if err := db.Upsert(f.ctx, f.card.Name, card, map[string]any{"Наименование": "PRIVATE-SECOND", "Учётка": second, "Филиал": "B"}, f.card); err != nil {
			t.Fatal(err)
		}
		if err := db.UpsertTablePartRows(f.ctx, f.card.Name, "Филиалы", card, []map[string]any{{"Филиал": "B"}}, f.card.TableParts[0]); err != nil {
			t.Fatal(err)
		}
		// A recursive policy on the private card must not be followed by the
		// infrastructure lookup; neither user is granted catalog read.
		p := f.user.Roles[0].Permissions.RowAccess.Documents[f.orders.Name]["read"]
		f.user.Roles[0].Permissions.RowAccess.Catalogs = map[string]auth.RowPolicies{f.card.Name: {"read": p}}
		for _, id := range []string{first, second} {
			f.user.ID = id
			f.assertTableCount(t, 1)
			own, foreign := "VISIBLE", "HIDDEN"
			if id == second {
				own, foreign = foreign, own
			}
			w := f.request("GET", "/ui/document/ордер", nil)
			if w.Code != 200 || !strings.Contains(w.Body.String(), own) || strings.Contains(w.Body.String(), foreign) || strings.Contains(w.Body.String(), "PRIVATE-") {
				t.Fatalf("employee isolation=%d %s", w.Code, w.Body.String())
			}
			if w := f.request("GET", "/ui/catalog/сотрудник", nil); w.Code != 403 {
				t.Fatalf("private recursive card=%d", w.Code)
			}
		}
	})
}

func TestUserCardTablePartReadOnlyDSLMatrix(t *testing.T) {
	for _, source := range []string{"scalar", "set"} {
		t.Run(source, func(t *testing.T) {
			dbtest.ForEachDialect(t, func(t *testing.T, db *storage.DB) {
				factory, want := newCardFixture, float64(1)
				if source == "set" {
					factory, want = newTableCardFixture, 2
				}
				f := factory(t, db)
				f.ctx = storage.ReadOnlyContext(f.ctx)
				if got := f.dslCount(t, "ВЫБРАТЬ Номер ИЗ Документ.Ордер"); got != want {
					t.Fatalf("read-only DSL %s source=%v", source, got)
				}
				// A successful infrastructure read must not grant application
				// writes to the same read-only DSL execution context.
				prog := mustParse(t, `Функция Проверка() Экспорт
 О=Документы.Ордер.Создать();О.Номер="READ-ONLY-MUST-NOT-WRITE";О.Филиал="A";О.Записать();
 Возврат Истина;
КонецФункции`)
				vars, tx := f.s.buildDSLVarsTx(auth.ContextWithUser(f.ctx, f.user), runtime.NewMovementsCollector("test", uuid.Nil))
				err := f.s.interp.Run(prog.Procedures[0], nil, vars)
				interpreter.RollbackTxExecution(tx)
				if err == nil {
					t.Fatal("read-only source enabled an application write")
				}
				rows, err := db.List(context.Background(), f.orders.Name, f.orders, storage.ListParams{RowFilterEvaluated: true})
				if err != nil || len(rows) != 2 {
					t.Fatalf("read-only context persisted a write: %v %v", rows, err)
				}
			})
		})
	}
}

func TestUserCardTablePartLargeCatalogMatrix(t *testing.T) {
	dbtest.ForEachDialect(t, func(t *testing.T, db *storage.DB) {
		f := newTableCardFixture(t, db)
		f.card.Indexes = []metadata.IndexSpec{{Fields: []string{"Учётка"}}}
		if err := db.Migrate(f.ctx, []*metadata.Entity{f.card, f.orders}); err != nil {
			t.Fatal(err)
		}
		if err := db.WithTx(f.ctx, func(ctx context.Context) error {
			for i := 0; i < 2000; i++ {
				if err := db.Upsert(ctx, f.card.Name, uuid.New(), map[string]any{"Наименование": fmt.Sprintf("PRIVATE-OTHER-%d", i), "Филиал": "C"}, f.card); err != nil {
					return err
				}
			}
			for i := 0; i < 3000; i++ {
				branch, title := []string{"A", "B", "C"}[i%3], fmt.Sprintf("OWN-%d", i)
				if branch == "C" {
					title = fmt.Sprintf("FOREIGN-%d", i)
				}
				if err := db.Upsert(ctx, f.orders.Name, uuid.New(), map[string]any{"Номер": title, "Филиал": branch}, f.orders); err != nil {
					return err
				}
			}
			return nil
		}); err != nil {
			t.Fatal(err)
		}
		started := time.Now()
		w := f.request("GET", "/ui/document/ордер", nil)
		listDuration := time.Since(started)
		if w.Code != 200 || strings.Contains(w.Body.String(), "FOREIGN-") || strings.Contains(w.Body.String(), "PRIVATE-") {
			t.Fatalf("large public list: %d", w.Code)
		}
		w = f.request("GET", "/ui/_ref-options/ордер", nil)
		var choices choiceHTTPResponse
		if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &choices) != nil || choices.Total != 2002 || len(choices.Items) == 0 {
			t.Fatalf("large picker count: %d %+v", w.Code, choices)
		}
		if got := f.dslCount(t, "ВЫБРАТЬ Номер ИЗ Документ.Ордер"); got != float64(2002) {
			t.Fatalf("large DSL count=%v", got)
		}
		started = time.Now()
		w = f.request("GET", "/ui/document/ордер/excel?"+url.Values{"sort": {"Номер"}, "dir": {"asc"}}.Encode(), nil)
		exportDuration := time.Since(started)
		if w.Code != 200 {
			t.Fatalf("large export status=%d", w.Code)
		}
		x, err := excelize.OpenReader(strings.NewReader(w.Body.String()))
		if err != nil {
			t.Fatal(err)
		}
		defer func() {
			if err := x.Close(); err != nil {
				t.Error(err)
			}
		}()
		rows, err := x.GetRows(x.GetSheetName(0))
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(fmt.Sprint(rows), "OWN-0") || strings.Contains(fmt.Sprint(rows), "FOREIGN-") || strings.Contains(fmt.Sprint(rows), "PRIVATE-") {
			t.Fatal("large workbook leaked/omitted rows")
		}
		t.Logf("indexed source: 2001 cards, 3002 orders; public list=%s, XLSX export=%s", listDuration, exportDuration)
	})
}
