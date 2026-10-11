package access_test

import (
	"testing"

	"github.com/ivantit66/onebase/internal/access"
	"github.com/ivantit66/onebase/internal/auth"
	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/ivantit66/onebase/internal/query"
)

func clientEntity() *metadata.Entity {
	return &metadata.Entity{
		Name: "Клиент",
		Kind: metadata.KindCatalog,
		Fields: []metadata.Field{
			{Name: "Телефон", Type: metadata.FieldTypeString},
			{Name: "Адрес", Type: metadata.FieldTypeString},
			{Name: "Паспорт", Type: metadata.FieldTypeString},
			{Name: "Возраст", Type: metadata.FieldTypeNumber},
		},
	}
}

func catRole(ops []string, fields auth.FieldPolicies) *auth.Role {
	fa := auth.FieldAccess{}
	if fields != nil {
		fa.Catalogs = map[string]auth.FieldPolicies{"Клиент": fields}
	}
	return &auth.Role{Permissions: auth.Permission{
		Catalogs:    map[string][]string{"Клиент": ops},
		FieldAccess: fa,
	}}
}

func TestMaskValue(t *testing.T) {
	cases := []struct {
		name     string
		strategy string
		keep     int
		in       any
		want     any
	}{
		{"full", access.FieldFull, 0, "+79161234455", "+79161234455"},
		{"empty strategy is full", "", 0, "x", "x"},
		{"hide", access.FieldHide, 0, "secret", nil},
		{"mask_tail keeps last N", access.FieldMaskTail, 4, "+79161234455", "••••••••4455"},
		{"mask_tail keep>=len masks all", access.FieldMaskTail, 10, "abc", "•••"},
		{"mask_tail on number", access.FieldMaskTail, 2, 12345, "•••45"},
		{"mask_all fixed mask", access.FieldMaskAll, 0, "sensitive", "••••••"},
		{"mask_city keeps first segment", access.FieldMaskCity, 0, "г. Москва, ул. Ленина, д.1", "г. Москва, ••••••"},
		{"mask_city single segment", access.FieldMaskCity, 0, "Москва", "Москва"},
		{"unknown strategy fails closed", "bogus", 4, "secret", "••••••"},
		{"empty value stays empty", access.FieldMaskAll, 0, "", ""},
		{"nil value stays empty for mask", access.FieldMaskTail, 4, nil, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := access.MaskValue(tc.strategy, tc.keep, tc.in)
			if got != tc.want {
				t.Fatalf("MaskValue(%q,%d,%v) = %v, want %v", tc.strategy, tc.keep, tc.in, got, tc.want)
			}
		})
	}
}

func TestFieldDecisions_NilUserAndAdmin(t *testing.T) {
	meta := clientEntity()
	if d := access.FieldDecisions(nil, "catalog", "Клиент", meta); d != nil {
		t.Fatalf("nil user must not mask, got %v", d)
	}
	admin := &auth.User{IsAdmin: true, Roles: []*auth.Role{
		catRole([]string{"read"}, auth.FieldPolicies{"Телефон": {Read: "mask_tail", Keep: 4}}),
	}}
	if d := access.FieldDecisions(admin, "catalog", "Клиент", meta); d != nil {
		t.Fatalf("admin must read full by default, got %v", d)
	}
	access.SetMaskAdmin(true)
	defer access.SetMaskAdmin(false)
	d := access.FieldDecisions(admin, "catalog", "Клиент", meta)
	if got, ok := d["Телефон"]; !ok || got.Strategy != access.FieldMaskTail {
		t.Fatalf("mask_admin must subject admin to masking, got %v", d)
	}
}

func TestDeniedMaskedColumn_ProjectionAndMaskAdmin(t *testing.T) {
	meta := clientEntity()
	u := &auth.User{Roles: []*auth.Role{
		catRole([]string{"read"}, auth.FieldPolicies{"Телефон": {Read: "mask_all"}}),
	}}
	sources := []query.SourceRef{{Kind: "catalog", Name: "Клиент"}}
	lookup := func(_, _ string) *metadata.Entity { return meta }

	if got := access.DeniedMaskedColumn(u, sources, []string{"Телефон"}, lookup); got != "Телефон" {
		t.Fatalf("protected projection got %q", got)
	}
	if got := access.DeniedMaskedColumn(u, sources, []string{"Наименование"}, lookup); got != "" {
		t.Fatalf("safe projection denied as %q", got)
	}
	if got := access.DeniedMaskedColumn(u, sources, []string{"*"}, lookup); got != "*" {
		t.Fatalf("wildcard must fail closed, got %q", got)
	}

	admin := &auth.User{IsAdmin: true, Roles: u.Roles}
	if got := access.DeniedMaskedColumn(admin, sources, []string{"Телефон"}, lookup); got != "" {
		t.Fatalf("unmasked admin denied as %q", got)
	}
	access.SetMaskAdmin(true)
	defer access.SetMaskAdmin(false)
	if got := access.DeniedMaskedColumn(admin, sources, []string{"Телефон"}, lookup); got != "Телефон" {
		t.Fatalf("mask_admin projection got %q", got)
	}
}

func TestFieldDecisions_SingleRole(t *testing.T) {
	meta := clientEntity()
	u := &auth.User{Roles: []*auth.Role{
		catRole([]string{"read"}, auth.FieldPolicies{
			"Телефон": {Read: "mask_tail", Keep: 4},
			"Паспорт": {Read: "hide"},
		}),
	}}
	d := access.FieldDecisions(u, "catalog", "Клиент", meta)
	if got := d["Телефон"]; got.Strategy != access.FieldMaskTail || got.Keep != 4 {
		t.Fatalf("Телефон = %+v, want mask_tail/4", got)
	}
	if !d["Паспорт"].Hidden() {
		t.Fatalf("Паспорт must be hidden, got %+v", d["Паспорт"])
	}
	if _, ok := d["Адрес"]; ok {
		t.Fatalf("unlisted field Адрес must stay full, got %+v", d["Адрес"])
	}
}

func TestFieldDecisions_LeastRestrictiveWins(t *testing.T) {
	meta := clientEntity()
	// One reading role masks Телефон, another reading role reads it in full →
	// full wins (OR semantics, mirrors row_access).
	u := &auth.User{Roles: []*auth.Role{
		catRole([]string{"read"}, auth.FieldPolicies{"Телефон": {Read: "mask_all"}}),
		catRole([]string{"read"}, nil),
	}}
	if d := access.FieldDecisions(u, "catalog", "Клиент", meta); len(d) != 0 {
		t.Fatalf("full-reading role must expose Телефон, got %v", d)
	}
	// Both roles restrict Телефон: least restrictive (mask_tail) beats mask_all.
	u2 := &auth.User{Roles: []*auth.Role{
		catRole([]string{"read"}, auth.FieldPolicies{"Телефон": {Read: "mask_all"}}),
		catRole([]string{"read"}, auth.FieldPolicies{"Телефон": {Read: "mask_tail", Keep: 4}}),
	}}
	d := access.FieldDecisions(u2, "catalog", "Клиент", meta)
	if got := d["Телефон"]; got.Strategy != access.FieldMaskTail || got.Keep != 4 {
		t.Fatalf("least restrictive Телефон = %+v, want mask_tail/4", got)
	}
}

func TestFieldDecisions_NonReadingRoleDoesNotVote(t *testing.T) {
	meta := clientEntity()
	u := &auth.User{Roles: []*auth.Role{
		catRole([]string{"read"}, auth.FieldPolicies{"Телефон": {Read: "hide"}}),
		// write-only role masking Адрес: it cannot read, so it neither exposes
		// nor restricts anything.
		{Permissions: auth.Permission{
			Catalogs:    map[string][]string{"Клиент": {"write"}},
			FieldAccess: auth.FieldAccess{Catalogs: map[string]auth.FieldPolicies{"Клиент": {"Адрес": {Read: "hide"}}}},
		}},
	}}
	d := access.FieldDecisions(u, "catalog", "Клиент", meta)
	if !d["Телефон"].Hidden() {
		t.Fatalf("Телефон must be hidden, got %+v", d["Телефон"])
	}
	if _, ok := d["Адрес"]; ok {
		t.Fatalf("Адрес restricted only by a non-reading role must stay full, got %+v", d["Адрес"])
	}
}

func TestFieldDecisions_CaseInsensitiveFieldName(t *testing.T) {
	meta := clientEntity()
	u := &auth.User{Roles: []*auth.Role{
		catRole([]string{"read"}, auth.FieldPolicies{"телефон": {Read: "mask_all"}}),
	}}
	d := access.FieldDecisions(u, "catalog", "Клиент", meta)
	if _, ok := d["Телефон"]; !ok {
		t.Fatalf("policy for lowercase телефон must canonicalise to Телефон, got %v", d)
	}
}

func TestMaskRecord(t *testing.T) {
	dec := map[string]access.FieldDecision{
		"Телефон": {Strategy: access.FieldMaskTail, Keep: 4},
		"Паспорт": {Strategy: access.FieldHide},
	}
	row := map[string]any{
		"телефон": "+79161234455", // case-insensitive key
		"Паспорт": "1234567890",
		"Адрес":   "г. Москва",
	}
	access.MaskRecord(dec, row)
	if row["телефон"] != "••••••••4455" {
		t.Fatalf("phone not masked: %v", row["телефон"])
	}
	if _, ok := row["Паспорт"]; ok {
		t.Fatalf("hidden field must be removed, got %v", row["Паспорт"])
	}
	if row["Адрес"] != "г. Москва" {
		t.Fatalf("unlisted field must be untouched, got %v", row["Адрес"])
	}
}

func TestValidateFieldPolicy(t *testing.T) {
	meta := clientEntity()
	if err := access.ValidateFieldPolicy("Телефон", auth.FieldPolicy{Read: "mask_tail", Keep: 4}, meta); err != nil {
		t.Fatalf("valid mask_tail rejected: %v", err)
	}
	if err := access.ValidateFieldPolicy("Паспорт", auth.FieldPolicy{Read: "hide"}, meta); err != nil {
		t.Fatalf("valid hide rejected: %v", err)
	}
	if err := access.ValidateFieldPolicy("НетТакого", auth.FieldPolicy{Read: "hide"}, meta); err == nil {
		t.Fatal("unknown field must be rejected")
	}
	if err := access.ValidateFieldPolicy("Телефон", auth.FieldPolicy{Read: "bogus"}, meta); err == nil {
		t.Fatal("unknown strategy must be rejected")
	}
	if err := access.ValidateFieldPolicy("Возраст", auth.FieldPolicy{Read: "mask_city"}, meta); err == nil {
		t.Fatal("mask_city on a number field must be rejected")
	}
}

func TestMaskedEmptyFillable(t *testing.T) {
	cases := []struct {
		strategy string
		stored   any
		want     bool
	}{
		{access.FieldMaskAll, nil, true},
		{access.FieldMaskAll, "", true},
		{access.FieldMaskTail, "", true},
		{access.FieldMaskCity, nil, true},
		{access.FieldMaskAll, "(916)111-22-33", false},
		{access.FieldMaskTail, "+79161234455", false},
		{access.FieldMaskAll, " ", false}, // пробел показывается маской — не «пусто» для роли
		{access.FieldMaskAll, 0, false},   // число показывается маской
		{access.FieldHide, nil, false},    // пустоту скрытого поля роль не видит
		{access.FieldFull, nil, false},
	}
	for _, tc := range cases {
		if got := access.MaskedEmptyFillable(access.FieldDecision{Strategy: tc.strategy, Keep: 4}, tc.stored, "(903)222-33-44"); got != tc.want {
			t.Errorf("MaskedEmptyFillable(%s, %#v) = %v, ожидалось %v", tc.strategy, tc.stored, got, tc.want)
		}
	}
}

// Маска, вернувшаяся из формы, — не данные: пустое ею не заполняется.
func TestMaskedEmptyFillable_RejectsMaskValue(t *testing.T) {
	for _, sent := range []any{"••••••", "•••••••••22-33"} {
		if access.MaskedEmptyFillable(access.FieldDecision{Strategy: access.FieldMaskAll}, nil, sent) {
			t.Errorf("маска %q не должна заполнять пустое поле", sent)
		}
	}
}
