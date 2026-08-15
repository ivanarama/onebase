package ui

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/ivantit66/onebase/internal/auth"
	"github.com/ivantit66/onebase/internal/dsl/interpreter"
	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/ivantit66/onebase/internal/runtime"
)

type registerMaskFixture struct {
	s          *Server
	ctx        context.Context
	reg        *metadata.Register
	goods      *metadata.Entity
	doc        *metadata.Entity
	recorderID uuid.UUID
	productIDs []uuid.UUID
	secrets    []string
}

func newRegisterMaskFixture(t *testing.T) *registerMaskFixture {
	t.Helper()
	goods := &metadata.Entity{
		Name:   "Товары",
		Kind:   metadata.KindCatalog,
		Fields: []metadata.Field{{Name: "Наименование", Type: metadata.FieldTypeString}},
	}
	doc := &metadata.Entity{
		Name:    "Реализация",
		Kind:    metadata.KindDocument,
		Posting: true,
		Fields: []metadata.Field{
			{Name: "Номер", Type: metadata.FieldTypeString},
			{Name: "Дата", Type: metadata.FieldTypeDate},
		},
	}
	reg := &metadata.Register{
		Name: "Продажи",
		Dimensions: []metadata.Field{
			{Name: "Товар", Type: "reference:Товары", RefEntity: goods.Name},
			{Name: "Область", Type: metadata.FieldTypeString},
		},
		Resources: []metadata.Field{
			{Name: "Количество", Type: metadata.FieldTypeNumber},
			{Name: "Себестоимость", Type: metadata.FieldTypeNumber},
		},
		Attributes: []metadata.Field{{Name: "Метка", Type: metadata.FieldTypeString}},
	}

	s, ctx := newSubmitTestServer(t, []*metadata.Entity{goods, doc})
	if err := s.store.MigrateRegisters(ctx, []*metadata.Register{reg}); err != nil {
		t.Fatal(err)
	}
	s.reg.Load(runtime.LoadOptions{
		Entities:  []*metadata.Entity{goods, doc},
		Registers: []*metadata.Register{reg},
	})

	productIDs := []uuid.UUID{uuid.New(), uuid.New()}
	for i, name := range []string{"Стул", "Стол"} {
		if err := s.store.Upsert(ctx, goods.Name, productIDs[i], map[string]any{"Наименование": name}, goods); err != nil {
			t.Fatal(err)
		}
	}
	recorderID := uuid.New()
	period := time.Date(2026, time.August, 15, 12, 0, 0, 0, time.UTC)
	if err := s.store.Upsert(ctx, doc.Name, recorderID, map[string]any{
		"Номер": "DOC-925",
		"Дата":  period,
	}, doc); err != nil {
		t.Fatal(err)
	}
	if err := s.store.SetPosted(ctx, doc.Name, recorderID, true); err != nil {
		t.Fatal(err)
	}
	if err := s.store.WriteMovements(ctx, reg.Name, doc.Name, recorderID,
		[]map[string]any{
			{
				"Товар": productIDs[0].String(), "Область": "visible",
				"Количество": 3.0, "Себестоимость": 754321.25,
				"Метка": "VISIBLE-MOVEMENT", "ВидДвижения": "Приход",
			},
			{
				"Товар": productIDs[1].String(), "Область": "hidden",
				"Количество": 2.0, "Себестоимость": 987654.5,
				"Метка": "HIDDEN-MOVEMENT", "ВидДвижения": "Приход",
			},
		}, reg, &period); err != nil {
		t.Fatalf("движения: %v", err)
	}
	// The registrar key is (type, UUID), not UUID alone. Keep a colliding UUID
	// from another document type to lock that boundary in both form and DSL tests.
	if err := s.store.WriteMovements(ctx, reg.Name, "ДругаяРеализация", recorderID,
		[]map[string]any{{
			"Товар": productIDs[0].String(), "Область": "visible",
			"Количество": 1.0, "Себестоимость": 112233.75,
			"Метка": "COLLIDING-RECORDER-TYPE", "ВидДвижения": "Приход",
		}}, reg, &period); err != nil {
		t.Fatalf("движение с совпадающим UUID регистратора: %v", err)
	}

	return &registerMaskFixture{
		s:          s,
		ctx:        ctx,
		reg:        reg,
		goods:      goods,
		doc:        doc,
		recorderID: recorderID,
		productIDs: productIDs,
		secrets:    []string{"754321.25", "987654.5", "112233.75"},
	}
}

func (f *registerMaskFixture) reader(name string, registerRead bool, fields auth.FieldPolicies, rowPolicy *auth.RowPolicy) *auth.User {
	permission := auth.Permission{
		Catalogs:  map[string][]string{f.goods.Name: {"read"}},
		Documents: map[string][]string{f.doc.Name: {"read"}},
	}
	if registerRead {
		permission.Registers = map[string][]string{f.reg.Name: {"read"}}
	}
	if len(fields) > 0 {
		permission.FieldAccess.Registers = map[string]auth.FieldPolicies{f.reg.Name: fields}
	}
	if rowPolicy != nil {
		permission.RowAccess.Registers = map[string]auth.RowPolicies{
			f.reg.Name: {"read": *rowPolicy},
		}
	}
	return &auth.User{ID: name, Login: name, Roles: []*auth.Role{{
		Name:        name,
		Permissions: permission,
	}}}
}

func (f *registerMaskFixture) maskedReader(name string) *auth.User {
	return f.reader(name, true, auth.FieldPolicies{
		"Себестоимость": {Read: "mask_all"},
	}, nil)
}

func registerRequest(method, target, name string, user *auth.User) *http.Request {
	r := reqWithChi(method, target, nil, map[string]string{"name": name})
	return r.WithContext(auth.ContextWithUser(r.Context(), user))
}

func documentFormRequest(f *registerMaskFixture, user *auth.User) *http.Request {
	r := reqWithChi(http.MethodGet,
		"/ui/document/"+f.doc.Name+"/"+f.recorderID.String(), nil,
		map[string]string{"entity": f.doc.Name, "id": f.recorderID.String()})
	return r.WithContext(auth.ContextWithUser(r.Context(), user))
}

func callAccumReg(proxy *accumRegProxy, method string, args []any) (result any, recovered any) {
	defer func() { recovered = recover() }()
	result = proxy.CallMethod(method, args)
	return result, nil
}

func requireMaskedCostRows(t *testing.T, value any, secrets []string) *interpreter.Array {
	t.Helper()
	rows, ok := value.(*interpreter.Array)
	if !ok {
		t.Fatalf("результат %T, ожидался *interpreter.Array", value)
	}
	if len(rows.Iterate()) == 0 {
		t.Fatal("прокси не вернул подготовленные движения")
	}
	for i, item := range rows.Iterate() {
		row, ok := item.(*interpreter.MapThis)
		if !ok {
			t.Fatalf("строка %d имеет тип %T", i, item)
		}
		got := row.Get("Себестоимость")
		if got != "••••••" {
			t.Errorf("строка %d: Себестоимость = %v, ожидалась маска", i, got)
		}
		for _, secret := range secrets {
			if strings.Contains(strings.TrimSpace(fmt.Sprint(got)), secret) {
				t.Fatalf("строка %d раскрыла защищённую стоимость %s", i, secret)
			}
		}
	}
	return rows
}

func TestUIRegisterListsApplyFieldMask(t *testing.T) {
	f := newRegisterMaskFixture(t)
	user := f.maskedReader("list-reader")

	for _, test := range []struct {
		name string
		path string
		call func(http.ResponseWriter, *http.Request)
	}{
		{"movements", "/ui/register/" + f.reg.Name, f.s.registerMovements},
		{"balances", "/ui/register/" + f.reg.Name + "/balances", f.s.registerBalances},
	} {
		t.Run(test.name, func(t *testing.T) {
			w := httptest.NewRecorder()
			test.call(w, registerRequest(http.MethodGet, test.path, f.reg.Name, user))
			if w.Code != http.StatusOK {
				t.Fatalf("код %d: %s", w.Code, w.Body.String())
			}
			page := w.Body.String()
			for _, secret := range f.secrets {
				if strings.Contains(page, secret) {
					t.Fatalf("защищённое значение %s уехало в HTML", secret)
				}
			}
			if !strings.Contains(page, "••••••") {
				t.Fatalf("маски в выдаче нет:\n%.500s", page)
			}
			if test.name == "movements" {
				if !strings.Contains(page, "Остатки →") {
					t.Fatal("ссылка на доступные остатки пропала")
				}
			}
		})
	}
}

func TestDSLAccumRegisterProxyAppliesFieldMask(t *testing.T) {
	f := newRegisterMaskFixture(t)
	ctx := auth.ContextWithUser(f.ctx, f.maskedReader("dsl-mask-reader"))
	proxy := newAccumRegsRoot(f.s, interpreter.NewTxState(ctx)).Get(f.reg.Name).(*accumRegProxy)

	for _, test := range []struct {
		method string
		args   []any
	}{
		{method: "select"},
		{method: "balances"},
		{method: "selectbyrecorder", args: []any{&interpreter.Ref{
			UUID: f.recorderID.String(), Type: f.doc.Name,
		}}},
	} {
		t.Run(test.method, func(t *testing.T) {
			value, recovered := callAccumReg(proxy, test.method, test.args)
			if recovered != nil {
				t.Fatalf("CallMethod(%s) panic: %v", test.method, recovered)
			}
			requireMaskedCostRows(t, value, f.secrets)
		})
	}
}

func TestUIRegisterProtectedDimensionCannotBeFilteredOrGrouped(t *testing.T) {
	f := newRegisterMaskFixture(t)
	user := f.reader("dimension-reader", true, auth.FieldPolicies{
		"Товар": {Read: "mask_all"},
	}, nil)

	w := httptest.NewRecorder()
	f.s.registerMovements(w, registerRequest(http.MethodGet, "/ui/register/"+f.reg.Name, f.reg.Name, user))
	if w.Code != http.StatusOK {
		t.Fatalf("движения: код %d: %s", w.Code, w.Body.String())
	}
	page := w.Body.String()
	if strings.Contains(page, `name="flt_Товар"`) {
		t.Fatal("форма предлагает фильтр по защищённому измерению")
	}
	if strings.Contains(page, "Остатки →") {
		t.Fatal("форма предлагает заведомо запрещённые остатки")
	}
	for i, id := range f.productIDs {
		if strings.Contains(page, id.String()) || strings.Contains(page, []string{"Стул", "Стол"}[i]) {
			t.Fatal("защищённая ссылка была разрешена в UUID или представление")
		}
	}

	w = httptest.NewRecorder()
	path := "/ui/register/" + f.reg.Name + "?flt_Товар=" + f.productIDs[0].String()
	f.s.registerMovements(w, registerRequest(http.MethodGet, path, f.reg.Name, user))
	if w.Code != http.StatusForbidden {
		t.Fatalf("фильтр по защищённому измерению: код %d, ожидался 403", w.Code)
	}

	w = httptest.NewRecorder()
	f.s.registerBalances(w, registerRequest(http.MethodGet, "/ui/register/"+f.reg.Name+"/balances", f.reg.Name, user))
	if w.Code != http.StatusForbidden {
		t.Fatalf("GROUP BY защищённого измерения: код %d, ожидался 403", w.Code)
	}
}

func TestUIRegisterProtectedPeriodCannotBeFiltered(t *testing.T) {
	f := newRegisterMaskFixture(t)
	user := f.reader("period-reader", true, auth.FieldPolicies{
		"period": {Read: "mask_all"},
	}, nil)

	for _, test := range []struct {
		name string
		path string
		call func(http.ResponseWriter, *http.Request)
	}{
		{"movements", "/ui/register/" + f.reg.Name, f.s.registerMovements},
		{"balances", "/ui/register/" + f.reg.Name + "/balances", f.s.registerBalances},
	} {
		t.Run(test.name+"-controls", func(t *testing.T) {
			w := httptest.NewRecorder()
			test.call(w, registerRequest(http.MethodGet, test.path, f.reg.Name, user))
			if w.Code != http.StatusOK {
				t.Fatalf("код %d: %s", w.Code, w.Body.String())
			}
			if strings.Contains(w.Body.String(), `name="from"`) || strings.Contains(w.Body.String(), `name="to"`) {
				t.Fatal("форма предлагает фильтр по защищённому периоду")
			}
		})
	}

	for _, test := range []struct {
		path string
		call func(http.ResponseWriter, *http.Request)
	}{
		{"/ui/register/" + f.reg.Name + "?from=not-a-date", f.s.registerMovements},
		{"/ui/register/" + f.reg.Name + "/balances?to=2026-08-15", f.s.registerBalances},
	} {
		w := httptest.NewRecorder()
		test.call(w, registerRequest(http.MethodGet, test.path, f.reg.Name, user))
		if w.Code != http.StatusForbidden {
			t.Fatalf("защищённый период %s: код %d, ожидался 403", test.path, w.Code)
		}
	}
}

func TestDSLAccumRegisterRejectsProtectedGroupingAndRecorderFilter(t *testing.T) {
	f := newRegisterMaskFixture(t)

	t.Run("dimension", func(t *testing.T) {
		user := f.reader("dsl-dimension-reader", true, auth.FieldPolicies{
			"Товар": {Read: "mask_all"},
		}, nil)
		ctx := auth.ContextWithUser(f.ctx, user)
		proxy := newAccumRegsRoot(f.s, interpreter.NewTxState(ctx)).Get(f.reg.Name).(*accumRegProxy)

		value, recovered := callAccumReg(proxy, "select", nil)
		if recovered != nil {
			t.Fatalf("select panic: %v", recovered)
		}
		rows := value.(*interpreter.Array)
		for _, item := range rows.Iterate() {
			if got := item.(*interpreter.MapThis).Get("Товар"); got != "••••••" {
				t.Fatalf("защищённое измерение = %v, ожидалась маска", got)
			}
		}
		if _, recovered = callAccumReg(proxy, "balances", nil); recovered == nil {
			t.Fatal("balances не отказал при GROUP BY защищённого измерения")
		}
	})

	for _, protectedField := range []string{"recorder", "recorder_type"} {
		t.Run(protectedField, func(t *testing.T) {
			user := f.reader("dsl-"+protectedField+"-reader", true, auth.FieldPolicies{
				protectedField: {Read: "mask_all"},
			}, nil)
			ctx := auth.ContextWithUser(f.ctx, user)
			proxy := newAccumRegsRoot(f.s, interpreter.NewTxState(ctx)).Get(f.reg.Name).(*accumRegProxy)

			value, recovered := callAccumReg(proxy, "select", nil)
			if recovered != nil {
				t.Fatalf("select panic: %v", recovered)
			}
			rows := value.(*interpreter.Array)
			if got := rows.Index(0).(*interpreter.MapThis).Get(protectedField); got != "••••••" {
				t.Fatalf("защищённый %s = %v, ожидалась маска", protectedField, got)
			}
			_, recovered = callAccumReg(proxy, "selectbyrecorder", []any{&interpreter.Ref{UUID: f.recorderID.String(), Type: f.doc.Name}})
			if recovered == nil {
				t.Fatal("selectbyrecorder не отказал для защищённого регистратора")
			}
		})
	}
}

func TestDocumentFormGatesRegisterMovements(t *testing.T) {
	f := newRegisterMaskFixture(t)

	t.Run("read and mask", func(t *testing.T) {
		w := httptest.NewRecorder()
		f.s.formEdit(w, documentFormRequest(f, f.maskedReader("document-mask-reader")))
		if w.Code != http.StatusOK {
			t.Fatalf("код %d: %s", w.Code, w.Body.String())
		}
		page := w.Body.String()
		if !strings.Contains(page, f.reg.Name) || !strings.Contains(page, "••••••") {
			t.Fatalf("движения или маска не отрисованы:\n%.600s", page)
		}
		for _, secret := range f.secrets {
			if strings.Contains(page, secret) {
				t.Fatalf("форма документа раскрыла стоимость %s", secret)
			}
		}
		if strings.Contains(page, "COLLIDING-RECORDER-TYPE") {
			t.Fatal("форма смешала движения разных типов регистратора с одинаковым UUID")
		}
	})

	t.Run("reference mask before resolve", func(t *testing.T) {
		user := f.reader("document-reference-mask", true, auth.FieldPolicies{
			"Товар": {Read: "mask_all"},
		}, nil)
		w := httptest.NewRecorder()
		f.s.formEdit(w, documentFormRequest(f, user))
		if w.Code != http.StatusOK {
			t.Fatalf("код %d: %s", w.Code, w.Body.String())
		}
		page := w.Body.String()
		if !strings.Contains(page, "••••••") {
			t.Fatal("форма документа не применила маску ссылочного измерения")
		}
		for i, id := range f.productIDs {
			if strings.Contains(page, id.String()) || strings.Contains(page, []string{"Стул", "Стол"}[i]) {
				t.Fatal("форма разрешила защищённую ссылку регистра в представление")
			}
		}
	})

	t.Run("without register read", func(t *testing.T) {
		user := f.reader("document-only-reader", false, nil, nil)
		w := httptest.NewRecorder()
		f.s.formEdit(w, documentFormRequest(f, user))
		if w.Code != http.StatusOK {
			t.Fatalf("код %d: %s", w.Code, w.Body.String())
		}
		page := w.Body.String()
		if strings.Contains(page, "VISIBLE-MOVEMENT") || strings.Contains(page, "HIDDEN-MOVEMENT") ||
			strings.Contains(page, "COLLIDING-RECORDER-TYPE") {
			t.Fatal("форма показала движения регистра без права read")
		}
	})

	t.Run("row access", func(t *testing.T) {
		policy := auth.RowPolicy{Field: "Область", Op: "eq", Value: auth.RowValue{Literal: "visible"}}
		user := f.reader("document-rls-reader", true, auth.FieldPolicies{
			"Себестоимость": {Read: "mask_all"},
		}, &policy)
		w := httptest.NewRecorder()
		f.s.formEdit(w, documentFormRequest(f, user))
		if w.Code != http.StatusOK {
			t.Fatalf("код %d: %s", w.Code, w.Body.String())
		}
		page := w.Body.String()
		if !strings.Contains(page, "VISIBLE-MOVEMENT") {
			t.Fatal("RLS убрал разрешённое движение")
		}
		if strings.Contains(page, "HIDDEN-MOVEMENT") {
			t.Fatal("RLS пропустил запрещённое движение")
		}
		if strings.Contains(page, "COLLIDING-RECORDER-TYPE") {
			t.Fatal("форма смешала типы регистратора до применения RLS")
		}
		if !strings.Contains(page, "••••••") {
			t.Fatal("после RLS не применена маска поля")
		}
	})

	t.Run("invalid row policy fails closed", func(t *testing.T) {
		policy := auth.RowPolicy{Field: "НетТакогоПоля", Op: "eq", Value: auth.RowValue{Literal: "x"}}
		user := f.reader("document-invalid-rls", true, nil, &policy)
		w := httptest.NewRecorder()
		f.s.formEdit(w, documentFormRequest(f, user))
		if w.Code != http.StatusOK {
			t.Fatalf("код %d: %s", w.Code, w.Body.String())
		}
		page := w.Body.String()
		if strings.Contains(page, "VISIBLE-MOVEMENT") || strings.Contains(page, "HIDDEN-MOVEMENT") ||
			strings.Contains(page, "COLLIDING-RECORDER-TYPE") {
			t.Fatal("ошибка RLS открыла движения вместо fail-closed")
		}
	})
}

func TestDSLSelectByRecorderComposesRowAccessInSQL(t *testing.T) {
	f := newRegisterMaskFixture(t)
	policy := auth.RowPolicy{Field: "Область", Op: "eq", Value: auth.RowValue{Literal: "visible"}}
	user := f.reader("dsl-recorder-rls", true, auth.FieldPolicies{
		"Себестоимость": {Read: "mask_all"},
	}, &policy)
	ctx := auth.ContextWithUser(f.ctx, user)
	proxy := newAccumRegsRoot(f.s, interpreter.NewTxState(ctx)).Get(f.reg.Name).(*accumRegProxy)

	value, recovered := callAccumReg(proxy, "selectbyrecorder", []any{&interpreter.Ref{
		UUID: f.recorderID.String(), Type: f.doc.Name,
	}})
	if recovered != nil {
		t.Fatalf("selectbyrecorder panic: %v", recovered)
	}
	rows := requireMaskedCostRows(t, value, f.secrets)
	if len(rows.Iterate()) != 1 {
		t.Fatalf("selectbyrecorder вернул %d строк, ожидалась одна разрешённая RLS", len(rows.Iterate()))
	}
	row := rows.Index(0).(*interpreter.MapThis)
	if got := row.Get("Метка"); got != "VISIBLE-MOVEMENT" {
		t.Fatalf("вернулась не разрешённая RLS строка: Метка=%v", got)
	}
}

func TestDSLSelectByRecorderRequiresRegisterReadBeforeParsing(t *testing.T) {
	f := newRegisterMaskFixture(t)
	user := f.reader("dsl-no-register-read", false, nil, nil)
	ctx := auth.ContextWithUser(f.ctx, user)
	proxy := newAccumRegsRoot(f.s, interpreter.NewTxState(ctx)).Get(f.reg.Name).(*accumRegProxy)

	for _, arg := range []any{
		&interpreter.Ref{UUID: f.recorderID.String(), Type: f.doc.Name},
		"not-a-uuid",
	} {
		if _, recovered := callAccumReg(proxy, "selectbyrecorder", []any{arg}); recovered == nil {
			t.Fatalf("selectbyrecorder(%v) не проверил право read", arg)
		}
	}
}
