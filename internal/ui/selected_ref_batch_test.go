package ui

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/ivantit66/onebase/internal/auth"
	"github.com/ivantit66/onebase/internal/dbtest"
	"github.com/ivantit66/onebase/internal/dsl/interpreter"
	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/ivantit66/onebase/internal/processor"
	"github.com/ivantit66/onebase/internal/runtime"
	"github.com/ivantit66/onebase/internal/storage"
)

// Exercise the HTTP form-event path, including processor state decoding and
// option serialisation. Complete options must retain GetByID's JSON contract.
func selectedRefBatchServer(t *testing.T, db *storage.DB, target *metadata.Entity) (*Server, *processor.Processor) {
	t.Helper()
	form := processorExecutionForm(
		&metadata.FormElement{Kind: metadata.FormElementButton, Name: "Проверить",
			Handlers: map[metadata.FormEventType]string{metadata.FormEventOnClick: "Проверить"}},
		&metadata.FormElement{Kind: metadata.FormElementTablePart, Name: "СтрокиФормы", DataPath: "Объект.Строки"},
	)
	form.ProgramAST = mustParse(t, "Процедура Проверить()\nКонецПроцедуры")
	proc := &processor.Processor{Name: "ПакетныйПодбор", Forms: []*metadata.FormModule{form},
		TableParts: []metadata.TablePart{{Name: "Строки", Fields: []metadata.Field{
			{Name: "Ссылка", Type: metadata.FieldType("reference:" + target.Name), RefEntity: target.Name},
		}}}}
	reg := runtime.NewRegistry()
	reg.Load(runtime.LoadOptions{Entities: []*metadata.Entity{target}})
	reg.LoadProcessors([]*processor.Processor{proc})
	interp := interpreter.New()
	interp.LookupProc = reg.GetModuleProc
	srv := &Server{store: db, reg: reg, interp: interp, lockMgr: runtime.NewLockManager(),
		messages: NewMessageStore(), ops: newOperationLimiter()}
	srv.entitySvc = srv.newEntityService(nil)
	return srv, proc
}

func selectedRefBatchHTTP(t *testing.T, srv *Server, proc *processor.Processor, selected []string, user *auth.User) []map[string]any {
	t.Helper()
	rows := make([]map[string]any, 0, len(selected))
	for _, id := range selected {
		rows = append(rows, map[string]any{"Ссылка": id})
	}
	raw, err := json.Marshal(rows)
	if err != nil {
		t.Fatal(err)
	}
	body := processorClickBody("Проверить")
	body.Set("tp_json.Строки", string(raw))
	req := httptest.NewRequest(http.MethodPost, "/ui/processor/"+proc.Name+"/form-event", strings.NewReader(body.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded; charset=utf-8")
	req = req.WithContext(auth.ContextWithUser(req.Context(), user))
	router := chi.NewRouter()
	router.Post("/ui/processor/{name}/form-event", srv.handleProcessorFormEvent)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("form-event status=%d: %s", rec.Code, rec.Body.String())
	}
	resp := decodeFormEventResponse(t, rec.Body.Bytes())
	if !resp.OK || resp.Error != "" {
		t.Fatalf("form-event: %s", rec.Body.String())
	}
	return resp.TPRefOptions["Строки"]["Ссылка"]
}

func TestProcessorSelectedRefBatchMatrix(t *testing.T) {
	dbtest.ForEachDialect(t, func(t *testing.T, db *storage.DB) {
		for _, kind := range []metadata.Kind{metadata.KindCatalog, metadata.KindDocument} {
			t.Run(string(kind), func(t *testing.T) {
				target := &metadata.Entity{Name: "Выбор" + uuid.NewString()[:8], Kind: kind,
					Hierarchical: kind == metadata.KindCatalog,
					Fields: []metadata.Field{
						{Name: "Наименование", Type: metadata.FieldTypeString},
						{Name: "Телефон", Type: metadata.FieldTypeString},
						{Name: "Скрытое", Type: metadata.FieldTypeString},
						{Name: "Число", Type: metadata.FieldTypeNumber},
						{Name: "Флаг", Type: metadata.FieldTypeBool},
						{Name: "Аудитория", Type: metadata.FieldTypeString},
					}}
				if err := db.Migrate(t.Context(), []*metadata.Entity{target}); err != nil {
					t.Fatal(err)
				}
				srv, proc := selectedRefBatchServer(t, db, target)
				mask := auth.FieldPolicies{"Наименование": {Read: "mask_all"}, "Телефон": {Read: "mask_tail", Keep: 4}, "Скрытое": {Read: "hide"}}
				policy := auth.RowPolicies{"read": {Field: "Аудитория", Op: "eq", Value: auth.RowValue{User: "login"}}}
				perm := auth.Permission{Processors: map[string][]string{proc.Name: {"run"}}}
				if kind == metadata.KindCatalog {
					perm.Catalogs = map[string][]string{target.Name: {"read"}}
					perm.RowAccess.Catalogs = map[string]auth.RowPolicies{target.Name: policy}
					perm.FieldAccess.Catalogs = map[string]auth.FieldPolicies{target.Name: mask}
				} else {
					perm.Documents = map[string][]string{target.Name: {"read"}}
					perm.RowAccess.Documents = map[string]auth.RowPolicies{target.Name: policy}
					perm.FieldAccess.Documents = map[string]auth.FieldPolicies{target.Name: mask}
				}
				user := &auth.User{Login: "anna", Roles: []*auth.Role{{Permissions: perm}}}
				ids := make([]uuid.UUID, refPickerDefaultLimit+3)
				for i := range ids {
					ids[i] = uuid.MustParse(fmt.Sprintf("abcdef00-0000-4000-8000-%012x", i+1))
					scope := "anna"
					if i == refPickerDefaultLimit+2 {
						scope = "other"
					}
					values := map[string]any{"Наименование": fmt.Sprintf("Товар-%03d", i), "Телефон": "+79161234455",
						"Скрытое": "секрет", "Число": "12.50", "Флаг": true, "Аудитория": scope}
					if target.Hierarchical && i == refPickerDefaultLimit+1 {
						values["is_folder"] = true // selected folders remain valid options
						values["parent_id"] = ids[0].String()
					}
					if err := db.Upsert(t.Context(), target.Name, ids[i], values, target); err != nil {
						t.Fatal(err)
					}
				}
				initial := selectedRefBatchHTTP(t, srv, proc, nil, user)
				if len(initial) != refPickerDefaultLimit {
					t.Fatalf("initial options=%d", len(initial))
				}
				// First page + reverse selected order + canonical aliases + missing,
				// invalid and RLS-denied UUIDs. No attempted ID may be appended twice.
				selected := []string{ids[0].String(), strings.ToUpper(ids[51].String()), ids[50].String(),
					ids[51].String(), uuid.NewString(), "invalid", "", ids[52].String(), ids[52].String()}
				got := selectedRefBatchHTTP(t, srv, proc, selected, user)
				want := append([]map[string]any{}, initial...)
				for _, id := range []uuid.UUID{ids[51], ids[50]} {
					row, err := db.GetByID(t.Context(), target.Name, id, target)
					if err != nil {
						t.Fatal(err)
					}
					srv.maskRecord(auth.ContextWithUser(t.Context(), user), target, row)
					row["_label"] = firstStringField(row, target)
					want = append(want, row)
				}
				gotJSON, _ := json.Marshal(got)
				wantJSON, _ := json.Marshal(want)
				if string(gotJSON) != string(wantJSON) {
					t.Fatalf("options JSON differs: got %d rows, want %d", len(got), len(want))
				}
				for _, deny := range []string{"no-read", "invalid-policy"} {
					t.Run(deny, func(t *testing.T) {
						deniedPerm := perm
						if deny == "no-read" {
							deniedPerm.Catalogs = map[string][]string{}
							deniedPerm.Documents = map[string][]string{}
						} else {
							invalid := map[string]auth.RowPolicies{target.Name: {"read": {Field: "НетПоля", Op: "eq", Value: auth.RowValue{Literal: "x"}}}}
							deniedPerm.RowAccess = auth.RowAccess{Catalogs: invalid, Documents: invalid}
						}
						denied := &auth.User{Login: "anna", Roles: []*auth.Role{{Permissions: deniedPerm}}}
						if options := selectedRefBatchHTTP(t, srv, proc, selected, denied); len(options) != 0 {
							t.Fatalf("inaccessible options leaked: %#v", options)
						}
					})
				}
			})
		}
	})
}

func TestProcessorSelectedRefBatchQueryGrowthMatrix(t *testing.T) {
	dbtest.ForEachDialect(t, func(t *testing.T, db *storage.DB) {
		target := &metadata.Entity{Name: "BatchGrowth" + uuid.NewString()[:8], Kind: metadata.KindCatalog,
			Fields: []metadata.Field{{Name: "Name", Type: metadata.FieldTypeString}}}
		if err := db.Migrate(t.Context(), []*metadata.Entity{target}); err != nil {
			t.Fatal(err)
		}
		srv, proc := selectedRefBatchServer(t, db, target)
		var selected []string
		for i := 0; i < refPickerDefaultLimit+refLabelBatchSize+1; i++ {
			id := uuid.MustParse(fmt.Sprintf("abcdef00-0000-4000-8000-%012x", i+1))
			if err := db.Upsert(t.Context(), target.Name, id, map[string]any{"Name": fmt.Sprintf("Item-%04d", i)}, target); err != nil {
				t.Fatal(err)
			}
			if i >= refPickerDefaultLimit {
				selected = append(selected, id.String())
			}
		}
		var onePageQueries int64
		for _, n := range []int{1, refLabelBatchSize - 1, refLabelBatchSize, refLabelBatchSize + 1} {
			before := db.PoolStats()
			// Duplicates must not increase the number of selected-object reads.
			requestIDs := append(append([]string{}, selected[:n]...), selected[:n]...)
			got := selectedRefBatchHTTP(t, srv, proc, requestIDs, nil)
			if len(got) != refPickerDefaultLimit+n {
				t.Fatalf("selected=%d options=%d", n, len(got))
			}
			for i, row := range got[refPickerDefaultLimit:] {
				if row["id"] != selected[i] {
					t.Fatalf("selected order at %d: %v", i, row["id"])
				}
			}
			if before != nil {
				// In this isolated DB each autocommit SQL statement acquires one
				// connection. Fixed form-event overhead cancels in the difference.
				queries := db.PoolStats().AcquireCount - before.AcquireCount
				if n == 1 {
					onePageQueries = queries
				}
				want := onePageQueries
				if n > refLabelBatchSize {
					want++
				}
				if queries != want {
					t.Fatalf("selected=%d SQL acquisitions=%d, want %d (one per batch)", n, queries, want)
				}
				t.Logf("selected=%d SQL acquisitions=%d", n, queries)
			}
		}
	})
}

func TestProcessorSelectedRefBatchPolicyCorrelationMatrix(t *testing.T) {
	dbtest.ForEachDialect(t, func(t *testing.T, db *storage.DB) {
		target := &metadata.Entity{Name: "CorrelatedChoice" + uuid.NewString()[:8], Kind: metadata.KindCatalog}
		target.Fields = []metadata.Field{
			{Name: "Name", Type: metadata.FieldTypeString},
			{Name: "Audience", Type: metadata.FieldTypeString},
			{Name: "Owner", Type: metadata.FieldType("reference:" + target.Name), RefEntity: target.Name},
			{Name: "Secret", Type: metadata.FieldTypeString},
		}
		if err := db.Migrate(t.Context(), []*metadata.Entity{target}); err != nil {
			t.Fatal(err)
		}
		srv, proc := selectedRefBatchServer(t, db, target)
		user := &auth.User{Login: "anna", Roles: []*auth.Role{{Permissions: auth.Permission{
			Processors: map[string][]string{proc.Name: {"run"}},
			Catalogs:   map[string][]string{target.Name: {"read"}},
			RowAccess: auth.RowAccess{Catalogs: map[string]auth.RowPolicies{target.Name: {
				"read": {Field: "Owner.Audience", Op: "eq", Value: auth.RowValue{User: "login"}},
			}}},
		}}}}
		idAt := func(i int) uuid.UUID {
			return uuid.MustParse(fmt.Sprintf("abcdef00-0000-4000-8000-%012x", i))
		}
		allowedOwner, deniedOwner, allowed, denied := idAt(100), idAt(101), idAt(102), idAt(103)
		insert := func(id uuid.UUID, name, audience string, owner uuid.UUID) {
			t.Helper()
			if err := db.Upsert(t.Context(), target.Name, id, map[string]any{
				"Name": name, "Audience": audience, "Owner": owner.String(), "Secret": "private-" + name,
			}, target); err != nil {
				t.Fatal(err)
			}
		}
		// Both the outer object and its referenced owner have owner_id. An
		// unqualified EXISTS can bind to the inner owner's self-reference.
		insert(allowedOwner, "Z-owner-allowed", "anna", allowedOwner)
		insert(deniedOwner, "Z-owner-denied", "other", deniedOwner)
		for i := 0; i < refPickerDefaultLimit; i++ {
			insert(idAt(i+1), fmt.Sprintf("A-item-%03d", i), "other", allowedOwner)
		}
		insert(allowed, "Z-selected-allowed", "other", allowedOwner)
		insert(denied, "Z-selected-denied", "other", deniedOwner)
		initial := selectedRefBatchHTTP(t, srv, proc, nil, user)
		if len(initial) != refPickerDefaultLimit {
			t.Fatalf("initial options=%d", len(initial))
		}
		for _, row := range initial {
			if row["id"] == allowed.String() || row["id"] == denied.String() {
				t.Fatal("selected objects must lie outside the initial page")
			}
		}
		got := selectedRefBatchHTTP(t, srv, proc, []string{denied.String(), allowed.String(), denied.String()}, user)
		want, err := db.GetByID(t.Context(), target.Name, allowed, target)
		if err != nil {
			t.Fatal(err)
		}
		want["_label"] = firstStringField(want, target)
		wantJSON, _ := json.Marshal(append(initial, want))
		gotJSON, _ := json.Marshal(got)
		if string(gotJSON) != string(wantJSON) {
			t.Fatalf("reference policy must admit only the selected object with anna's owner: got %d options, want %d", len(got), len(initial)+1)
		}
	})
}
