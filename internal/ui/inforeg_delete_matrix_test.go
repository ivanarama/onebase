package ui

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/ivantit66/onebase/internal/auth"
	"github.com/ivantit66/onebase/internal/dbtest"
	"github.com/ivantit66/onebase/internal/exchange"
	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/ivantit66/onebase/internal/runtime"
	"github.com/ivantit66/onebase/internal/storage"
	"golang.org/x/net/html"
)

func deleteMatrixServer(db *storage.DB, entities []*metadata.Entity, regs []*metadata.InfoRegister, plans []*metadata.ExchangePlan) *Server {
	registry := runtime.NewRegistry()
	registry.Load(runtime.LoadOptions{Entities: entities, InfoRegs: regs})
	registry.LoadExchangePlans(plans)
	return &Server{store: db, reg: registry}
}

func performInfoRegDelete(s *Server, ir *metadata.InfoRegister, form url.Values, user *auth.User) *httptest.ResponseRecorder {
	req := reqWithChi(http.MethodPost, "/ui/inforeg/"+strings.ToLower(ir.Name)+"/delete", form,
		map[string]string{"name": ir.Name})
	if user != nil {
		req = req.WithContext(auth.ContextWithUser(req.Context(), user))
	}
	rec := httptest.NewRecorder()
	s.infoRegDelete(rec, req)
	return rec
}

func renderedInfoRegDeleteForms(t *testing.T, s *Server, ir *metadata.InfoRegister, user *auth.User) ([]url.Values, string) {
	t.Helper()
	req := reqWithChi(http.MethodGet, "/ui/inforeg/"+strings.ToLower(ir.Name), nil,
		map[string]string{"name": ir.Name})
	if user != nil {
		req = req.WithContext(auth.ContextWithUser(req.Context(), user))
	}
	rec := httptest.NewRecorder()
	s.infoRegList(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("info-register list: status=%d body=%s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	doc, err := html.Parse(strings.NewReader(body))
	if err != nil {
		t.Fatalf("parse info-register list HTML: %v", err)
	}
	wantedAction := "/ui/inforeg/" + strings.ToLower(ir.Name) + "/delete"
	var forms []url.Values
	var walk func(*html.Node)
	walk = func(node *html.Node) {
		if node.Type == html.ElementNode && node.Data == "form" {
			action, _ := deleteMatrixHTMLAttr(node, "action")
			if action == wantedAction {
				values := url.Values{}
				var inputs func(*html.Node)
				inputs = func(current *html.Node) {
					if current.Type == html.ElementNode && current.Data == "input" {
						if name, ok := deleteMatrixHTMLAttr(current, "name"); ok {
							value, _ := deleteMatrixHTMLAttr(current, "value")
							values.Add(name, value)
						}
					}
					for child := current.FirstChild; child != nil; child = child.NextSibling {
						inputs(child)
					}
				}
				inputs(node)
				forms = append(forms, values)
			}
		}
		for child := node.FirstChild; child != nil; child = child.NextSibling {
			walk(child)
		}
	}
	walk(doc)
	return forms, body
}

func deleteMatrixHTMLAttr(node *html.Node, name string) (string, bool) {
	for _, attr := range node.Attr {
		if attr.Key == name {
			return attr.Val, true
		}
	}
	return "", false
}

func cloneDeleteForm(source url.Values) url.Values {
	clone := make(url.Values, len(source))
	for name, values := range source {
		clone[name] = append([]string(nil), values...)
	}
	return clone
}

func assertInfoRegRowCount(t *testing.T, db *storage.DB, ir *metadata.InfoRegister, want int) []map[string]any {
	t.Helper()
	rows, err := db.InfoRegList(context.Background(), ir, storage.RegFilter{})
	if err != nil {
		t.Fatalf("list %s: %v", ir.Name, err)
	}
	if len(rows) != want {
		t.Fatalf("%s row count=%d, want %d; rows=%#v", ir.Name, len(rows), want, rows)
	}
	return rows
}

func TestParseInfoRegDeleteKeyPostgresExponentRemainsText(t *testing.T) {
	req := &http.Request{PostForm: url.Values{"Seq": {"1e2147483647"}}}
	dims, err := parseInfoRegDeleteKey(req,
		[]metadata.Field{{Name: "Seq", Type: metadata.FieldTypeNumber}}, true)
	if err != nil {
		t.Fatal(err)
	}
	if value, ok := dims["Seq"].(string); !ok || value != "1e2147483647" {
		t.Fatalf("postgres exponent key=%T(%v), want bounded raw text", dims["Seq"], dims["Seq"])
	}
}

func TestUIInfoRegDeleteTypedCompositeKeyMatrix(t *testing.T) {
	dbtest.ForEachDialect(t, func(t *testing.T, db *storage.DB) {
		ctx := context.Background()
		goods := &metadata.Entity{
			Name:   "UIDeleteGoods",
			Kind:   metadata.KindCatalog,
			Fields: []metadata.Field{{Name: "Name", Type: metadata.FieldTypeString}},
		}
		if err := db.Migrate(ctx, []*metadata.Entity{goods}); err != nil {
			t.Fatal(err)
		}
		ownerID := uuid.New()
		if err := db.Upsert(ctx, goods.Name, ownerID, map[string]any{"Name": "Owner"}, goods); err != nil {
			t.Fatal(err)
		}
		ir := &metadata.InfoRegister{
			Name: "UIDeleteTypedKey",
			Dimensions: []metadata.Field{
				{Name: "Slice", Type: metadata.FieldTypeString},
				{Name: "Flag", Type: metadata.FieldTypeBool},
				{Name: "Moment", Type: metadata.FieldTypeDate},
				{Name: "Seq", Type: metadata.FieldTypeNumber},
				{Name: "Scaled", Type: metadata.FieldTypeNumber, Length: 10, Scale: 2},
				{Name: "Exponent", Type: metadata.FieldTypeNumber, Length: 10, Scale: 2},
				{Name: "Zero", Type: metadata.FieldTypeNumber, Length: 10, Scale: 2},
				{Name: "Comma", Type: metadata.FieldTypeNumber},
				{Name: "Owner", Type: metadata.FieldType("reference:" + goods.Name), RefEntity: goods.Name},
			},
			Resources: []metadata.Field{{Name: "Value", Type: metadata.FieldTypeBool}},
		}
		if err := db.MigrateInfoRegisters(ctx, []*metadata.InfoRegister{ir}); err != nil {
			t.Fatal(err)
		}
		if err := db.EnsureExchangeSchema(ctx); err != nil {
			t.Fatal(err)
		}
		plan := &metadata.ExchangePlan{
			Name:    "TypedKeyDeleteExchange",
			Content: []string{"РегистрСведений." + ir.Name},
			Nodes:   []metadata.ExchangeNode{{Code: "center"}, {Code: "branch"}},
		}
		plan.Normalize()
		if err := db.SaveExchangeThisNode(ctx, plan.Name, "branch"); err != nil {
			t.Fatal(err)
		}
		s := deleteMatrixServer(db, []*metadata.Entity{goods}, []*metadata.InfoRegister{ir}, []*metadata.ExchangePlan{plan})
		moment := time.Date(2026, 8, 15, 9, 30, 45, 123000000, time.UTC)
		momentKey := moment.Format(time.RFC3339Nano)
		for _, flag := range []bool{false, true} {
			var dateValue any = momentKey
			if !flag {
				// Ordinary UI/DSL writes use time.Time; SQLite stores that value in
				// its adapter layout while PostgreSQL retains microseconds.
				dateValue = moment
			}
			commaValue := "1.00"
			if db.IsSQLite() {
				commaValue = "1,00"
			}
			dims := map[string]any{
				"Slice": "typed", "Flag": flag, "Moment": dateValue,
				// The storage boundary now canonicalizes NUMBER by its declared scale.
				"Seq": "1.00",
				// Both backends must round and render these values identically.
				"Scaled": " 1.005 ", "Exponent": "1e0", "Zero": "0.00",
				"Comma": commaValue, "Owner": ownerID,
			}
			if err := s.infoRegWrite(ctx, ir, dims, map[string]any{"Value": flag}, nil, nil); err != nil {
				t.Fatal(err)
			}
		}
		packageBytes, err := exchange.BuildPackage(ctx, db, s.reg, plan, "center")
		if err != nil {
			t.Fatalf("typed-key exchange readback: %v", err)
		}
		var initialPackage exchange.Package
		if err := json.Unmarshal(packageBytes, &initialPackage); err != nil || len(initialPackage.Objects) != 2 {
			t.Fatalf("typed-key package objects=%d err=%v payload=%s", len(initialPackage.Objects), err, packageBytes)
		}
		forms, _ := renderedInfoRegDeleteForms(t, s, ir, nil)
		if len(forms) != 2 {
			t.Fatalf("delete forms=%d, want 2: %#v", len(forms), forms)
		}
		var trueForm, falseForm url.Values
		for _, form := range forms {
			switch strings.ToLower(form.Get("Flag")) {
			case "true", "1":
				trueForm = form
			case "false", "0":
				falseForm = form
			}
		}
		if trueForm == nil || falseForm == nil {
			t.Fatalf("boolean keys were not serialized losslessly: %#v", forms)
		}
		if got := trueForm.Get("Seq"); got != "1" {
			t.Fatalf("number machine key=%q, want canonical SQLite/PG value 1", got)
		}
		if got := trueForm.Get("Moment"); got != momentKey {
			t.Fatalf("date machine key=%q, want exact RFC3339 value %q", got, momentKey)
		}
		wantFalseMoment := moment.UTC().Format("2006-01-02 15:04:05-07:00")
		wantScaledForm, wantExponentForm := "1.01", "1.00"
		if db.IsPostgres() {
			wantFalseMoment = momentKey
		}
		if got := falseForm.Get("Moment"); got != wantFalseMoment {
			t.Fatalf("time.Time date machine key=%q, want %q", got, wantFalseMoment)
		}
		if got := trueForm.Get("Scaled"); got != wantScaledForm {
			t.Fatalf("scaled number machine key=%q, want %q", got, wantScaledForm)
		}
		if got := trueForm.Get("Exponent"); got != wantExponentForm {
			t.Fatalf("exponent number machine key=%q, want %q", got, wantExponentForm)
		}
		if got := trueForm.Get("Comma"); got != "1" {
			t.Fatalf("comma number machine key=%q, want canonical value 1", got)
		}

		for _, field := range ir.Dimensions {
			t.Run("missing_"+field.Name, func(t *testing.T) {
				form := cloneDeleteForm(trueForm)
				form.Del(field.Name)
				rec := performInfoRegDelete(s, ir, form, nil)
				if rec.Code != http.StatusBadRequest {
					t.Fatalf("status=%d, want 400; body=%s", rec.Code, rec.Body.String())
				}
				assertInfoRegRowCount(t, db, ir, 2)
			})
			t.Run("duplicate_"+field.Name, func(t *testing.T) {
				form := cloneDeleteForm(trueForm)
				form[field.Name] = append(form[field.Name], "attacker")
				rec := performInfoRegDelete(s, ir, form, nil)
				if rec.Code != http.StatusBadRequest {
					t.Fatalf("status=%d, want 400; body=%s", rec.Code, rec.Body.String())
				}
				assertInfoRegRowCount(t, db, ir, 2)
			})
		}
		for _, tc := range []struct {
			name, field, value string
		}{
			{"empty bool", "Flag", ""},
			{"bad bool", "Flag", "on"},
			{"empty date", "Moment", ""},
			{"bad date", "Moment", "not-a-date"},
			{"empty number", "Seq", ""},
			{"bad number", "Seq", "not-a-number"},
			{"empty reference", "Owner", ""},
			{"bad reference", "Owner", "not-a-uuid"},
		} {
			t.Run(tc.name, func(t *testing.T) {
				form := cloneDeleteForm(trueForm)
				form.Set(tc.field, tc.value)
				rec := performInfoRegDelete(s, ir, form, nil)
				if rec.Code != http.StatusBadRequest {
					t.Fatalf("status=%d, want 400; body=%s", rec.Code, rec.Body.String())
				}
				assertInfoRegRowCount(t, db, ir, 2)
			})
		}

		if rec := performInfoRegDelete(s, ir, trueForm, nil); rec.Code != http.StatusFound {
			t.Fatalf("delete true row: status=%d body=%s", rec.Code, rec.Body.String())
		}
		pending, err := db.PendingExchangeChanges(ctx, plan.Name, "center")
		if err != nil {
			t.Fatal(err)
		}
		if len(pending) != 2 {
			t.Fatalf("numeric delete created a second exchange identity: %#v", pending)
		}
		expectedSeq, expectedScaled, expectedExponent, expectedZero, expectedComma := "1", "1.01", "1.00", "0.00", "1"
		if db.IsPostgres() {
			expectedSeq, expectedScaled, expectedExponent, expectedZero, expectedComma = "1", "1.01", "1", "0", "1"
		}
		foundDeletedTrue := false
		for _, change := range pending {
			var key map[string]any
			if err := json.Unmarshal([]byte(change.ObjectID), &key); err != nil {
				t.Fatalf("decode typed exchange key %q: %v", change.ObjectID, err)
			}
			if key["Seq"] != expectedSeq || key["Scaled"] != expectedScaled || key["Exponent"] != expectedExponent || key["Zero"] != expectedZero || key["Comma"] != expectedComma {
				t.Fatalf("numeric key changed backend identity in outbox: %#v", key)
			}
			flag, _ := key["Flag"].(bool)
			expectedMoment := momentKey
			if db.IsSQLite() && !flag {
				expectedMoment = moment.UTC().Format("2006-01-02 15:04:05-07:00")
			}
			if key["Moment"] != expectedMoment {
				t.Fatalf("date key changed backend identity in outbox: %#v", key)
			}
			if flag {
				foundDeletedTrue = change.Deletion
			}
		}
		if !foundDeletedTrue {
			t.Fatalf("deleted true row did not replace its original outbox entry: %#v", pending)
		}
		rows := assertInfoRegRowCount(t, db, ir, 1)
		if infoRegKeyValue(ir.Dimensions[1], rows[0]) != "false" {
			t.Fatalf("wrong boolean sibling survived: %#v", rows)
		}
		if rec := performInfoRegDelete(s, ir, falseForm, nil); rec.Code != http.StatusFound {
			t.Fatalf("delete false row: status=%d body=%s", rec.Code, rec.Body.String())
		}
		assertInfoRegRowCount(t, db, ir, 0)
		pending, err = db.PendingExchangeChanges(ctx, plan.Name, "center")
		if err != nil || len(pending) != 2 || !pending[0].Deletion || !pending[1].Deletion {
			t.Fatalf("typed tombstones did not replace both original changes: changes=%#v err=%v", pending, err)
		}

		emptyIR := &metadata.InfoRegister{
			Name:       "UIDeleteEmptyStringKey",
			Dimensions: []metadata.Field{{Name: "Key", Type: metadata.FieldTypeString}},
			Resources:  []metadata.Field{{Name: "Value", Type: metadata.FieldTypeString}},
		}
		if err := db.MigrateInfoRegisters(ctx, []*metadata.InfoRegister{emptyIR}); err != nil {
			t.Fatal(err)
		}
		if err := db.InfoRegSet(ctx, emptyIR, map[string]any{"Key": ""}, map[string]any{"Value": "empty"}, nil); err != nil {
			t.Fatal(err)
		}
		emptyServer := deleteMatrixServer(db, nil, []*metadata.InfoRegister{emptyIR}, nil)
		emptyForms, _ := renderedInfoRegDeleteForms(t, emptyServer, emptyIR, nil)
		if len(emptyForms) != 1 || len(emptyForms[0]["Key"]) != 1 || emptyForms[0]["Key"][0] != "" {
			t.Fatalf("present empty string key was lost in HTML form: %#v", emptyForms)
		}
		if rec := performInfoRegDelete(emptyServer, emptyIR, url.Values{}, nil); rec.Code != http.StatusBadRequest {
			t.Fatalf("missing string key: status=%d, want 400; body=%s", rec.Code, rec.Body.String())
		}
		assertInfoRegRowCount(t, db, emptyIR, 1)
		if rec := performInfoRegDelete(emptyServer, emptyIR, emptyForms[0], nil); rec.Code != http.StatusFound {
			t.Fatalf("present empty string key: status=%d, want 302; body=%s", rec.Code, rec.Body.String())
		}
		assertInfoRegRowCount(t, db, emptyIR, 0)
		if err := db.InfoRegSet(ctx, emptyIR, map[string]any{"Key": "   "}, map[string]any{"Value": "spaces"}, nil); err != nil {
			t.Fatal(err)
		}
		if rec := performInfoRegDelete(emptyServer, emptyIR, url.Values{"Key": {"   "}}, nil); rec.Code != http.StatusFound {
			t.Fatalf("present whitespace string key: status=%d, want 302; body=%s", rec.Code, rec.Body.String())
		}
		assertInfoRegRowCount(t, db, emptyIR, 0)
	})
}

func TestUIInfoRegDeleteRowPolicyNoOracleAndOutboxMatrix(t *testing.T) {
	dbtest.ForEachDialect(t, func(t *testing.T, db *storage.DB) {
		ctx := context.Background()
		ir := &metadata.InfoRegister{
			Name: "UIDeleteRLS",
			Dimensions: []metadata.Field{
				{Name: "Slice", Type: metadata.FieldTypeString},
				{Name: "Key", Type: metadata.FieldTypeString},
			},
			Resources: []metadata.Field{{Name: "Owner", Type: metadata.FieldTypeString}},
		}
		if err := db.MigrateInfoRegisters(ctx, []*metadata.InfoRegister{ir}); err != nil {
			t.Fatal(err)
		}
		for _, row := range []struct{ slice, owner string }{{"exists", "other"}, {"allowed", ""}} {
			if err := db.InfoRegSet(ctx, ir,
				map[string]any{"Slice": row.slice, "Key": "K"}, map[string]any{"Owner": row.owner}, nil); err != nil {
				t.Fatal(err)
			}
		}
		if err := db.EnsureExchangeSchema(ctx); err != nil {
			t.Fatal(err)
		}
		plan := &metadata.ExchangePlan{
			Name:    "HTTPDeleteExchange",
			Content: []string{"РегистрСведений." + ir.Name},
			Nodes:   []metadata.ExchangeNode{{Code: "center"}, {Code: "branch"}},
		}
		plan.Normalize()
		if err := db.SaveExchangeThisNode(ctx, plan.Name, "branch"); err != nil {
			t.Fatal(err)
		}
		s := deleteMatrixServer(db, nil, []*metadata.InfoRegister{ir}, []*metadata.ExchangePlan{plan})
		policy := auth.RowPolicy{Field: "Owner", Op: "empty"}
		user := &auth.User{ID: "restricted", Roles: []*auth.Role{{Permissions: auth.Permission{
			InfoRegs: map[string][]string{ir.Name: {"read", "delete"}},
			RowAccess: auth.RowAccess{InfoRegs: map[string]auth.RowPolicies{
				ir.Name: {"delete": policy},
			}},
		}}}}

		var deniedBodies []string
		for _, slice := range []string{"exists", "absent"} {
			rec := performInfoRegDelete(s, ir, url.Values{"Slice": {slice}, "Key": {"K"}}, user)
			if rec.Code != http.StatusForbidden {
				t.Fatalf("restricted %s: status=%d, want 403; body=%s", slice, rec.Code, rec.Body.String())
			}
			deniedBodies = append(deniedBodies, rec.Body.String())
		}
		if deniedBodies[0] != deniedBodies[1] {
			t.Fatalf("hidden and absent keys are distinguishable:\nhidden=%q\nabsent=%q", deniedBodies[0], deniedBodies[1])
		}
		if pending, err := db.PendingExchangeChanges(ctx, plan.Name, "center"); err != nil || len(pending) != 0 {
			t.Fatalf("denied deletes created outbox changes: changes=%#v err=%v", pending, err)
		}

		allowedForm := url.Values{"Slice": {"allowed"}, "Key": {"K"}}
		if rec := performInfoRegDelete(s, ir, allowedForm, user); rec.Code != http.StatusFound {
			t.Fatalf("allowed delete: status=%d body=%s", rec.Code, rec.Body.String())
		}
		rows := assertInfoRegRowCount(t, db, ir, 1)
		if rows[0]["Slice"] != "exists" {
			t.Fatalf("hidden sibling was deleted: %#v", rows)
		}
		pending, err := db.PendingExchangeChanges(ctx, plan.Name, "center")
		if err != nil {
			t.Fatal(err)
		}
		if len(pending) != 1 || !pending[0].Deletion || pending[0].Kind != storage.ExchangeKindInfoReg {
			t.Fatalf("actual delete did not create exactly one tombstone: %#v", pending)
		}
		var objectKey map[string]any
		if err := json.Unmarshal([]byte(pending[0].ObjectID), &objectKey); err != nil {
			t.Fatalf("decode tombstone key %q: %v", pending[0].ObjectID, err)
		}
		if objectKey["Slice"] != "allowed" || objectKey["Key"] != "K" {
			t.Fatalf("tombstone uses requested/sibling key instead of deleted row: %#v", objectKey)
		}

		if err := db.InfoRegSet(ctx, ir,
			map[string]any{"Slice": "rollback", "Key": "K"}, map[string]any{"Owner": ""}, nil); err != nil {
			t.Fatal(err)
		}
		if err := db.SaveExchangeThisNode(ctx, plan.Name, "ghost"); err != nil {
			t.Fatal(err)
		}
		rec := performInfoRegDelete(s, ir, url.Values{"Slice": {"rollback"}, "Key": {"K"}}, user)
		if rec.Code != http.StatusInternalServerError {
			t.Fatalf("outbox failure: status=%d, want 500; body=%s", rec.Code, rec.Body.String())
		}
		rows = assertInfoRegRowCount(t, db, ir, 2)
		foundRollback := false
		for _, row := range rows {
			foundRollback = foundRollback || row["Slice"] == "rollback"
		}
		if !foundRollback {
			t.Fatalf("delete was not rolled back after tombstone failure: %#v", rows)
		}
		pendingAfter, err := db.PendingExchangeChanges(ctx, plan.Name, "center")
		if err != nil || len(pendingAfter) != 1 {
			t.Fatalf("failed tombstone changed outbox: changes=%#v err=%v", pendingAfter, err)
		}
	})
}

func TestUIInfoRegDeletePeriodMaskMatrix(t *testing.T) {
	dbtest.ForEachDialect(t, func(t *testing.T, db *storage.DB) {
		ctx := context.Background()
		ir := &metadata.InfoRegister{
			Name:       "UIDeletePeriodMask",
			Periodic:   true,
			Dimensions: []metadata.Field{{Name: "Key", Type: metadata.FieldTypeString}},
			Resources:  []metadata.Field{{Name: "Value", Type: metadata.FieldTypeString}},
		}
		if err := db.MigrateInfoRegisters(ctx, []*metadata.InfoRegister{ir}); err != nil {
			t.Fatal(err)
		}
		period := time.Date(2026, 8, 15, 12, 0, 0, 0, time.UTC)
		if err := db.InfoRegSet(ctx, ir, map[string]any{"Key": "K"}, map[string]any{"Value": "keep"}, &period); err != nil {
			t.Fatal(err)
		}
		if exact, err := db.InfoRegGetExact(ctx, ir, map[string]any{"Key": "K"}, &period); err != nil || exact == nil || exact["Value"] != "keep" {
			t.Fatalf("periodic exact read regressed: row=%#v err=%v", exact, err)
		}
		s := deleteMatrixServer(db, nil, []*metadata.InfoRegister{ir}, nil)
		user := &auth.User{ID: "period-masked", Roles: []*auth.Role{{Permissions: auth.Permission{
			InfoRegs: map[string][]string{ir.Name: {"read", "delete"}},
			FieldAccess: auth.FieldAccess{InfoRegs: map[string]auth.FieldPolicies{
				ir.Name: {"period": {Read: "mask_all"}},
			}},
		}}}}
		for _, rawPeriod := range []string{period.Format(time.RFC3339Nano), "not-a-period"} {
			rec := performInfoRegDelete(s, ir, url.Values{"period": {rawPeriod}, "Key": {"K"}}, user)
			if rec.Code != http.StatusForbidden {
				t.Fatalf("masked period %q: status=%d, want 403; body=%s", rawPeriod, rec.Code, rec.Body.String())
			}
		}
		assertInfoRegRowCount(t, db, ir, 1)
		_, body := renderedInfoRegDeleteForms(t, s, ir, user)
		if strings.Contains(body, period.Format(time.RFC3339Nano)) || strings.Contains(body, "onebase_info_reg_key_values") {
			t.Fatalf("masked primary key leaked through list HTML: %s", body)
		}
	})
}

func TestUIInfoRegDeleteStorageErrorMatrix(t *testing.T) {
	dbtest.ForEachDialect(t, func(t *testing.T, db *storage.DB) {
		ctx := context.Background()
		ir := &metadata.InfoRegister{
			Name:       "UIDeleteStorageError",
			Dimensions: []metadata.Field{{Name: "Key", Type: metadata.FieldTypeString}},
			Resources:  []metadata.Field{{Name: "Value", Type: metadata.FieldTypeString}},
		}
		if err := db.MigrateInfoRegisters(ctx, []*metadata.InfoRegister{ir}); err != nil {
			t.Fatal(err)
		}
		s := deleteMatrixServer(db, nil, []*metadata.InfoRegister{ir}, nil)
		if _, err := db.Exec(ctx, "DROP TABLE "+metadata.InfoRegTableName(ir.Name)); err != nil {
			t.Fatal(err)
		}
		rec := performInfoRegDelete(s, ir, url.Values{"Key": {"K"}}, nil)
		if rec.Code != http.StatusInternalServerError {
			t.Fatalf("storage failure: status=%d, want 500; body=%s", rec.Code, rec.Body.String())
		}
	})
}

func TestUIInfoRegDeletePostgresConcurrentRacesMatrix(t *testing.T) {
	dbtest.ForEachDialect(t, func(t *testing.T, db *storage.DB) {
		if !db.IsPostgres() {
			t.Skip("PostgreSQL row-lock coordination")
		}
		ctx := context.Background()
		rlsIR := &metadata.InfoRegister{
			Name: "UIDeleteConcurrentRLS",
			Dimensions: []metadata.Field{
				{Name: "Slice", Type: metadata.FieldTypeString},
				{Name: "Key", Type: metadata.FieldTypeString},
			},
			Resources: []metadata.Field{{Name: "Owner", Type: metadata.FieldTypeString}},
		}
		goneIR := &metadata.InfoRegister{
			Name:       "UIDeleteConcurrentGone",
			Dimensions: []metadata.Field{{Name: "Key", Type: metadata.FieldTypeString}},
			Resources:  []metadata.Field{{Name: "Value", Type: metadata.FieldTypeString}},
		}
		if err := db.MigrateInfoRegisters(ctx, []*metadata.InfoRegister{rlsIR, goneIR}); err != nil {
			t.Fatal(err)
		}
		if err := db.EnsureExchangeSchema(ctx); err != nil {
			t.Fatal(err)
		}
		plan := &metadata.ExchangePlan{
			Name: "HTTPDeleteConcurrentExchange",
			Content: []string{
				"РегистрСведений." + rlsIR.Name,
				"РегистрСведений." + goneIR.Name,
			},
			Nodes: []metadata.ExchangeNode{{Code: "center"}, {Code: "branch"}},
		}
		plan.Normalize()
		if err := db.SaveExchangeThisNode(ctx, plan.Name, "branch"); err != nil {
			t.Fatal(err)
		}
		s := deleteMatrixServer(db, nil, []*metadata.InfoRegister{rlsIR, goneIR}, []*metadata.ExchangePlan{plan})
		rlsDims := map[string]any{"Slice": "S", "Key": "K"}
		if err := db.InfoRegSet(ctx, rlsIR, rlsDims, map[string]any{"Owner": "mine"}, nil); err != nil {
			t.Fatal(err)
		}
		user := &auth.User{ID: "mine", Roles: []*auth.Role{{Permissions: auth.Permission{
			InfoRegs: map[string][]string{rlsIR.Name: {"delete"}},
			RowAccess: auth.RowAccess{InfoRegs: map[string]auth.RowPolicies{
				rlsIR.Name: {"delete": {Field: "Owner", Op: "eq", Value: auth.RowValue{Literal: "mine"}}},
			}},
		}}}}

		updateTx, updateCtx, err := db.BeginTx(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if err := db.InfoRegSet(updateCtx, rlsIR, rlsDims, map[string]any{"Owner": "other"}, nil); err != nil {
			_ = updateTx.Rollback(ctx)
			t.Fatal(err)
		}
		rlsResult := make(chan *httptest.ResponseRecorder, 1)
		go func() {
			rlsResult <- performInfoRegDelete(s, rlsIR, url.Values{"Slice": {"S"}, "Key": {"K"}}, user)
		}()
		waiting, waitErr := waitForPostgresInfoRegDeleteLock(ctx, db, rlsIR)
		if waitErr != nil || !waiting {
			_ = updateTx.Rollback(ctx)
			select {
			case <-rlsResult:
			case <-time.After(3 * time.Second):
			}
			if waitErr != nil {
				t.Fatal(waitErr)
			}
			t.Fatal("RLS delete never reached the row-lock wait")
		}
		if err := updateTx.Commit(ctx); err != nil {
			t.Fatal(err)
		}
		var rlsRec *httptest.ResponseRecorder
		select {
		case rlsRec = <-rlsResult:
		case <-time.After(5 * time.Second):
			t.Fatal("RLS delete did not finish after concurrent update commit")
		}
		if rlsRec.Code != http.StatusForbidden {
			t.Fatalf("concurrent RLS update: status=%d, want 403; body=%s", rlsRec.Code, rlsRec.Body.String())
		}
		rows := assertInfoRegRowCount(t, db, rlsIR, 1)
		if rows[0]["Owner"] != "other" {
			t.Fatalf("concurrently hidden row was deleted or reverted: %#v", rows)
		}
		if pending, err := db.PendingExchangeChanges(ctx, plan.Name, "center"); err != nil || len(pending) != 0 {
			t.Fatalf("RLS race created tombstone: changes=%#v err=%v", pending, err)
		}

		goneDims := map[string]any{"Key": "K"}
		if err := db.InfoRegSet(ctx, goneIR, goneDims, map[string]any{"Value": "soon gone"}, nil); err != nil {
			t.Fatal(err)
		}
		deleteTx, deleteCtx, err := db.BeginTx(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if err := db.InfoRegDelete(deleteCtx, goneIR, goneDims, nil); err != nil {
			_ = deleteTx.Rollback(ctx)
			t.Fatal(err)
		}
		goneResult := make(chan *httptest.ResponseRecorder, 1)
		go func() {
			goneResult <- performInfoRegDelete(s, goneIR, url.Values{"Key": {"K"}}, nil)
		}()
		waiting, waitErr = waitForPostgresInfoRegDeleteLock(ctx, db, goneIR)
		if waitErr != nil || !waiting {
			_ = deleteTx.Rollback(ctx)
			select {
			case <-goneResult:
			case <-time.After(3 * time.Second):
			}
			if waitErr != nil {
				t.Fatal(waitErr)
			}
			t.Fatal("concurrent delete never reached the row-lock wait")
		}
		if err := deleteTx.Commit(ctx); err != nil {
			t.Fatal(err)
		}
		var goneRec *httptest.ResponseRecorder
		select {
		case goneRec = <-goneResult:
		case <-time.After(5 * time.Second):
			t.Fatal("delete did not finish after concurrent delete commit")
		}
		if goneRec.Code != http.StatusNotFound {
			t.Fatalf("concurrent delete: status=%d, want 404; body=%s", goneRec.Code, goneRec.Body.String())
		}
		if pending, err := db.PendingExchangeChanges(ctx, plan.Name, "center"); err != nil || len(pending) != 0 {
			t.Fatalf("zero-row delete race created tombstone: changes=%#v err=%v", pending, err)
		}
	})
}

func waitForPostgresInfoRegDeleteLock(ctx context.Context, db *storage.DB, ir *metadata.InfoRegister) (bool, error) {
	pattern := "%DELETE FROM " + metadata.InfoRegTableName(ir.Name) + "%"
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		var waiting bool
		err := db.QueryRow(ctx, `SELECT EXISTS (
			SELECT 1 FROM pg_stat_activity
			WHERE pid <> pg_backend_pid()
			  AND datname = current_database()
			  AND query LIKE $1
			  AND wait_event_type = 'Lock'
		)`, pattern).Scan(&waiting)
		if err != nil {
			return false, err
		}
		if waiting {
			return true, nil
		}
		time.Sleep(50 * time.Millisecond)
	}
	return false, nil
}
