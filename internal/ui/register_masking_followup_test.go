package ui

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/ivantit66/onebase/internal/auth"
	"github.com/ivantit66/onebase/internal/dsl/interpreter"
)

func renderRegisterMovementsForUser(f *registerMaskFixture, user *auth.User) (int, string) {
	w := httptest.NewRecorder()
	f.s.registerMovements(w, registerRequest(http.MethodGet, "/ui/register/"+f.reg.Name, f.reg.Name, user))
	return w.Code, w.Body.String()
}

func renderRegisterBalancesForUser(f *registerMaskFixture, user *auth.User) (int, string) {
	w := httptest.NewRecorder()
	f.s.registerBalances(w, registerRequest(http.MethodGet, "/ui/register/"+f.reg.Name+"/balances", f.reg.Name, user))
	return w.Code, w.Body.String()
}

func renderDocumentFormForUser(f *registerMaskFixture, user *auth.User) (int, string) {
	w := httptest.NewRecorder()
	f.s.formEdit(w, documentFormRequest(f, user))
	return w.Code, w.Body.String()
}

func TestRegisterReferenceLabelsRespectTargetReadAndRLS(t *testing.T) {
	f := newRegisterMaskFixture(t)

	t.Run("dimension target object read", func(t *testing.T) {
		user := f.reader("no-goods-read", true, nil, nil)
		delete(user.Roles[0].Permissions.Catalogs, f.goods.Name)
		code, page := renderRegisterMovementsForUser(f, user)
		if code != http.StatusOK {
			t.Fatalf("status = %d: %s", code, page)
		}
		if strings.Contains(page, "Стул") || strings.Contains(page, "Стол") {
			t.Fatal("register resolver disclosed labels without target catalog read")
		}
	})

	t.Run("dimension target row access", func(t *testing.T) {
		user := f.reader("goods-rls", true, nil, nil)
		user.Roles[0].Permissions.RowAccess.Catalogs = map[string]auth.RowPolicies{
			f.goods.Name: {"read": {
				Field: "Наименование", Op: "eq", Value: auth.RowValue{Literal: "Стул"},
			}},
		}
		code, page := renderRegisterMovementsForUser(f, user)
		if code != http.StatusOK {
			t.Fatalf("status = %d: %s", code, page)
		}
		if !strings.Contains(page, "Стул") {
			t.Fatal("target RLS removed allowed reference label")
		}
		if strings.Contains(page, "Стол") {
			t.Fatal("target RLS disclosed denied reference label")
		}
	})

	t.Run("dimension malformed target row access fails closed", func(t *testing.T) {
		user := f.reader("goods-invalid-rls", true, nil, nil)
		user.Roles[0].Permissions.RowAccess.Catalogs = map[string]auth.RowPolicies{
			f.goods.Name: {"read": {
				Field: "НетТакогоПоля", Op: "eq", Value: auth.RowValue{Literal: "x"},
			}},
		}
		code, page := renderRegisterMovementsForUser(f, user)
		if code != http.StatusOK {
			t.Fatalf("status = %d: %s", code, page)
		}
		if strings.Contains(page, "Стул") || strings.Contains(page, "Стол") {
			t.Fatal("malformed target RLS failed open during batched label resolution")
		}
	})

	t.Run("recorder target object read", func(t *testing.T) {
		user := f.reader("no-document-read", true, nil, nil)
		delete(user.Roles[0].Permissions.Documents, f.doc.Name)
		code, page := renderRegisterMovementsForUser(f, user)
		if code != http.StatusOK {
			t.Fatalf("status = %d: %s", code, page)
		}
		if strings.Contains(page, "DOC-925") {
			t.Fatal("recorder resolver disclosed document label without target read")
		}
	})

	t.Run("recorder target row access", func(t *testing.T) {
		user := f.reader("document-target-rls", true, nil, nil)
		user.Roles[0].Permissions.RowAccess.Documents = map[string]auth.RowPolicies{
			f.doc.Name: {"read": {
				Field: "Номер", Op: "eq", Value: auth.RowValue{Literal: "DENIED"},
			}},
		}
		code, page := renderRegisterMovementsForUser(f, user)
		if code != http.StatusOK {
			t.Fatalf("status = %d: %s", code, page)
		}
		if strings.Contains(page, "DOC-925") {
			t.Fatal("recorder resolver disclosed document label denied by target RLS")
		}

		user = f.reader("document-target-rls-allowed", true, nil, nil)
		user.Roles[0].Permissions.RowAccess.Documents = map[string]auth.RowPolicies{
			f.doc.Name: {"read": {
				Field: "Номер", Op: "eq", Value: auth.RowValue{Literal: "DOC-925"},
			}},
		}
		code, page = renderRegisterMovementsForUser(f, user)
		if code != http.StatusOK || !strings.Contains(page, "DOC-925") {
			t.Fatalf("target RLS removed allowed recorder label; status=%d", code)
		}
	})
}

func TestRegisterReferenceLabelsTrustedContextPreservesInternalSemantics(t *testing.T) {
	f := newRegisterMaskFixture(t)
	user := f.reader("trusted-labels", true, nil, nil)
	delete(user.Roles[0].Permissions.Catalogs, f.goods.Name)
	delete(user.Roles[0].Permissions.Documents, f.doc.Name)
	ctx := trustedDSLContext(auth.ContextWithUser(f.ctx, user))
	rows := []map[string]any{{
		"Товар":         f.productIDs[0].String(),
		"recorder":      f.recorderID.String(),
		"recorder_type": f.doc.Name,
		"вид_движения":  "Приход",
		"line_number":   1,
	}}

	f.s.resolveRegisterRows(ctx, rows, f.reg)
	if rows[0]["Товар"] != "Стул" {
		t.Fatalf("trusted dimension label = %v, want Стул", rows[0]["Товар"])
	}
	if label := fmt.Sprint(rows[0]["recorder_label"]); !strings.Contains(label, "DOC-925") {
		t.Fatalf("trusted recorder label = %q, want DOC-925", label)
	}
}

func TestDocumentFormSkipsProtectedRecorderSelection(t *testing.T) {
	f := newRegisterMaskFixture(t)
	for _, strategy := range []string{"mask_all", "hide"} {
		for _, field := range []string{"recorder", "recorder_type"} {
			t.Run(strategy+"/"+field, func(t *testing.T) {
				user := f.reader("doc-protected-"+strategy+"-"+field, true, auth.FieldPolicies{
					field: {Read: strategy},
				}, nil)
				code, page := renderDocumentFormForUser(f, user)
				if code != http.StatusOK {
					t.Fatalf("status = %d: %s", code, page)
				}
				for _, marker := range []string{"VISIBLE-MOVEMENT", "HIDDEN-MOVEMENT", "COLLIDING-RECORDER-TYPE"} {
					if strings.Contains(page, marker) {
						t.Fatalf("document form selected movements through protected %s (%s)", field, strategy)
					}
				}
			})
		}
	}
}

func TestRegisterHideRemovesRenderedColumns(t *testing.T) {
	f := newRegisterMaskFixture(t)

	t.Run("movement metadata and synthetic columns", func(t *testing.T) {
		user := f.reader("movement-hide", true, auth.FieldPolicies{
			"Товар":         {Read: "hide"},
			"Себестоимость": {Read: "hide"},
			"Метка":         {Read: "hide"},
			"вид_движения":  {Read: "hide"},
			"recorder":      {Read: "hide"},
		}, nil)
		code, page := renderRegisterMovementsForUser(f, user)
		if code != http.StatusOK {
			t.Fatalf("status = %d: %s", code, page)
		}
		for _, header := range []string{"Товар", "Себестоимость", "Метка", "Вид движения", "Регистратор"} {
			if strings.Contains(page, "<th>"+header+"</th>") {
				t.Fatalf("hidden field still rendered a column: %s", header)
			}
		}
		if strings.Contains(page, "VISIBLE-MOVEMENT") || strings.Contains(page, "Стул") {
			t.Fatal("hidden register field still rendered a cell value")
		}
	})

	t.Run("recorder type hides compound column", func(t *testing.T) {
		user := f.reader("recorder-type-hide", true, auth.FieldPolicies{
			"recorder_type": {Read: "hide"},
		}, nil)
		code, page := renderRegisterMovementsForUser(f, user)
		if code != http.StatusOK {
			t.Fatalf("status = %d: %s", code, page)
		}
		if strings.Contains(page, "<th>Регистратор</th>") || strings.Contains(page, "DOC-925") {
			t.Fatal("hidden recorder_type still rendered the compound recorder column")
		}
	})

	t.Run("balance resource", func(t *testing.T) {
		user := f.reader("balance-hide", true, auth.FieldPolicies{
			"Себестоимость": {Read: "hide"},
		}, nil)
		code, page := renderRegisterBalancesForUser(f, user)
		if code != http.StatusOK {
			t.Fatalf("status = %d: %s", code, page)
		}
		if strings.Contains(page, "<th>Себестоимость</th>") {
			t.Fatal("hidden balance resource still rendered a header")
		}
		if !strings.Contains(page, "<th>Количество</th>") {
			t.Fatal("unprotected balance resource disappeared with hidden neighbour")
		}
	})

	t.Run("document synthetic columns", func(t *testing.T) {
		user := f.reader("document-hide", true, auth.FieldPolicies{
			"line_number":   {Read: "hide"},
			"вид_движения":  {Read: "hide"},
			"period":        {Read: "hide"},
			"Себестоимость": {Read: "hide"},
			"Метка":         {Read: "hide"},
		}, nil)
		code, page := renderDocumentFormForUser(f, user)
		if code != http.StatusOK {
			t.Fatalf("status = %d: %s", code, page)
		}
		for _, header := range []string{"№", "Вид", "period", "Себестоимость", "Метка"} {
			if strings.Contains(page, "<th>"+header+"</th>") {
				t.Fatalf("document movements rendered hidden column %s", header)
			}
		}
		if strings.Contains(page, "VISIBLE-MOVEMENT") || strings.Contains(page, f.secrets[0]) {
			t.Fatal("document movements rendered a hidden cell value")
		}
	})
}

func TestProtectedMovementKindDeniesBalancesUIAndDSL(t *testing.T) {
	f := newRegisterMaskFixture(t)
	user := f.reader("movement-kind-protected", true, auth.FieldPolicies{
		"вид_движения": {Read: "mask_all"},
	}, nil)

	code, page := renderRegisterMovementsForUser(f, user)
	if code != http.StatusOK || !strings.Contains(page, "••••••") {
		t.Fatalf("movement list did not mask kind; status=%d", code)
	}
	if strings.Contains(page, "Остатки →") {
		t.Fatal("movement page links to balances that protected kind must deny")
	}
	if code, _ = renderRegisterBalancesForUser(f, user); code != http.StatusForbidden {
		t.Fatalf("balances status = %d, want 403 for protected movement kind", code)
	}

	ctx := auth.ContextWithUser(f.ctx, user)
	proxy := newAccumRegsRoot(f.s, interpreter.NewTxState(ctx)).Get(f.reg.Name).(*accumRegProxy)
	value, recovered := callAccumReg(proxy, "select", nil)
	if recovered != nil {
		t.Fatalf("select panic: %v", recovered)
	}
	rows := value.(*interpreter.Array)
	if got := rows.Index(0).(*interpreter.MapThis).Get("вид_движения"); got != "••••••" {
		t.Fatalf("movement kind = %v, want mask", got)
	}
	if _, recovered = callAccumReg(proxy, "balances", nil); recovered == nil {
		t.Fatal("DSL balances accepted protected movement kind")
	}
}

func TestDSLSelectByRecorderRejectsUUIDOnlyAndKeepsTypesSeparate(t *testing.T) {
	f := newRegisterMaskFixture(t)
	user := f.reader("typed-recorder", true, nil, nil)
	ctx := auth.ContextWithUser(f.ctx, user)
	proxy := newAccumRegsRoot(f.s, interpreter.NewTxState(ctx)).Get(f.reg.Name).(*accumRegProxy)

	if _, recovered := callAccumReg(proxy, "selectbyrecorder", []any{f.recorderID.String()}); recovered == nil {
		t.Fatal("UUID-only selectbyrecorder must be rejected as ambiguous")
	} else if !strings.Contains(strings.ToLower(fmt.Sprint(recovered)), "типизирован") {
		t.Fatalf("UUID-only error is not actionable: %v", recovered)
	}

	value, recovered := callAccumReg(proxy, "selectbyrecorder", []any{&interpreter.Ref{
		UUID: f.recorderID.String(), Type: strings.ToLower(f.doc.Name),
	}})
	if recovered != nil {
		t.Fatalf("typed selectbyrecorder panic: %v", recovered)
	}
	rows := value.(*interpreter.Array)
	if len(rows.Iterate()) != 2 {
		t.Fatalf("typed recorder returned %d rows, want 2 from its type only", len(rows.Iterate()))
	}
	for _, item := range rows.Iterate() {
		if got := item.(*interpreter.MapThis).Get("Метка"); got == "COLLIDING-RECORDER-TYPE" {
			t.Fatal("typed recorder mixed a same-UUID movement from another type")
		}
	}
}
