package ui

import (
	"fmt"
	"net/http"
	"sort"
	"strings"

	"github.com/ivantit66/onebase/internal/access"
	"github.com/ivantit66/onebase/internal/auth"
	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/ivantit66/onebase/internal/storage"
)

type rlsDiagnosticRow struct {
	Role       string
	Section    string
	Kind       string
	Object     string
	Op         string
	Field      string
	Permission bool
	Status     string
	Error      string
	AutoFill   string
}

type rlsDiagnosticTarget struct {
	kind    string
	section string
	name    string
	label   string
	meta    *metadata.Entity
}

type rlsDSLPathRow struct {
	Path   string
	Mode   string
	Detail string
}

func (s *Server) adminRLSDiagnostics(w http.ResponseWriter, r *http.Request) {
	if !s.isAdmin(r) {
		s.renderForbidden(w, r)
		return
	}
	if s.authRepo == nil {
		http.Error(w, "auth not configured", http.StatusInternalServerError)
		return
	}
	roles, err := s.authRepo.ListRoles(r.Context())
	if err != nil {
		http.Error(w, s.errText(r, err), http.StatusInternalServerError)
		return
	}
	rows := s.buildRLSDiagnosticRows(roles)
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	renderAdminTemplate(w, "admin-rls", map[string]any{
		"Rows":     rows,
		"DSLPaths": rlsDSLPathRows(),
	})
}

func (s *Server) buildRLSDiagnosticRows(roles []*auth.Role) []rlsDiagnosticRow {
	targets := s.rlsDiagnosticTargets()
	var rows []rlsDiagnosticRow
	for _, role := range roles {
		if role == nil || role.Permissions.RowAccess.IsZero() {
			continue
		}
		addSection := func(kind, section string, policies map[string]auth.RowPolicies) {
			for object, ops := range policies {
				target, ok := targets[rlsDiagnosticTargetKey(kind, object)]
				for op, raw := range ops {
					row := rlsDiagnosticRow{
						Role:    role.Name,
						Section: section,
						Kind:    kind,
						Object:  object,
						Op:      strings.ToLower(strings.TrimSpace(op)),
						Field:   rowPolicyFieldLabel(raw),
					}
					if !ok {
						row.Status = "unknown object"
						row.Error = "объект не найден"
						rows = append(rows, row)
						continue
					}
					row.Object = target.name
					row.Kind = target.label
					row.Permission = auth.PermissionHas(role.Permissions, kind, object, op)
					policy := raw
					if raw.SameAs != "" {
						policy, _ = ops.Resolve(op)
					}
					if err := access.ValidatePolicyWithLookup(policy, target.meta, s.reg); err != nil {
						row.Status = "invalid"
						row.Error = err.Error()
					} else if !row.Permission {
						row.Status = "ignored"
						row.Error = "нет object-level права"
					} else {
						row.Status = "active"
						row.AutoFill = s.rlsAutoFillFields(role, target, op)
					}
					if row.AutoFill == "" {
						row.AutoFill = "нет"
					}
					rows = append(rows, row)
				}
			}
		}
		addSection("catalog", "catalogs", role.Permissions.RowAccess.Catalogs)
		addSection("document", "documents", role.Permissions.RowAccess.Documents)
		addSection("register", "registers", role.Permissions.RowAccess.Registers)
		addSection("inforeg", "inforegs", role.Permissions.RowAccess.InfoRegs)
	}
	sort.Slice(rows, func(i, j int) bool {
		a := rows[i]
		b := rows[j]
		return strings.Join([]string{a.Role, a.Section, a.Object, a.Op}, "\x00") <
			strings.Join([]string{b.Role, b.Section, b.Object, b.Op}, "\x00")
	})
	return rows
}

func (s *Server) rlsDiagnosticTargets() map[string]rlsDiagnosticTarget {
	out := map[string]rlsDiagnosticTarget{}
	add := func(kind, section, name, label string, meta *metadata.Entity) {
		out[rlsDiagnosticTargetKey(kind, name)] = rlsDiagnosticTarget{
			kind: kind, section: section, name: name, label: label, meta: meta,
		}
	}
	for _, ent := range s.reg.Entities() {
		if ent == nil {
			continue
		}
		if ent.Kind == metadata.KindCatalog {
			add("catalog", "catalogs", ent.Name, "справочник", ent)
		} else {
			add("document", "documents", ent.Name, "документ", ent)
		}
	}
	for _, reg := range s.reg.Registers() {
		if reg != nil {
			add("register", "registers", reg.Name, "регистр", storage.RegisterPredicateEntity(reg))
		}
	}
	for _, ar := range s.reg.AccountRegisters() {
		if ar != nil {
			add("register", "registers", ar.Name, "регистр бухгалтерии", storage.AccountRegisterPredicateEntity(ar))
		}
	}
	for _, ir := range s.reg.InfoRegisters() {
		if ir != nil {
			add("inforeg", "inforegs", ir.Name, "регистр сведений", storage.InfoRegisterPredicateEntity(ir))
		}
	}
	return out
}

func rlsDiagnosticTargetKey(kind, name string) string {
	return strings.ToLower(strings.TrimSpace(kind)) + "\x00" + strings.ToLower(strings.TrimSpace(name))
}

func (s *Server) rlsAutoFillFields(role *auth.Role, target rlsDiagnosticTarget, op string) string {
	if role == nil || target.meta == nil {
		return ""
	}
	u := &auth.User{
		ID:       "_diag_user_id",
		Login:    "_diag_user_login",
		FullName: "_diag_user_full_name",
		Lang:     "ru",
		Roles:    []*auth.Role{role},
	}
	dec, err := access.DecideWithLookup(u, target.kind, target.name, op, target.meta, s.reg)
	if err != nil || !dec.Allowed || dec.Unrestricted {
		return ""
	}
	fields := map[string]any{}
	filled := access.AutoFillPredicateFields(dec.Predicate, fields, target.meta)
	return strings.Join(filled, ", ")
}

func rowPolicyFieldLabel(p auth.RowPolicy) string {
	switch {
	case p.SameAs != "":
		return "same_as " + p.SameAs
	case p.Field != "":
		return p.Field
	case len(p.All) > 0:
		return fmt.Sprintf("all[%d]", len(p.All))
	case len(p.Any) > 0:
		return fmt.Sprintf("any[%d]", len(p.Any))
	case p.Not != nil:
		return "not"
	default:
		return ""
	}
}

func rlsDSLPathRows() []rlsDSLPathRow {
	return []rlsDSLPathRow{
		{Path: "Справочники / Документы", Mode: "user", Detail: "read/write/delete/post проходят object-level и row-level checks"},
		{Path: "Новый Запрос", Mode: "user", Detail: "read sources получают SQL row filters; forbidden sources отклоняются"},
		{Path: "РегистрыНакопления", Mode: "user", Detail: "Остатки/Движения фильтруются row policy"},
		{Path: "ЗначениеРеквизитаОбъекта", Mode: "user", Detail: "ссылка сначала проверяется на read; скрытые ссылки не раскрывают display name"},
		{Path: "OnWrite / OnPost", Mode: "trusted", Detail: "server-code исполняется с текущим пользователем, но без row filters"},
		{Path: "Серверные события форм", Mode: "trusted", Detail: "ПередЗаписью/ПриЗаписи/ПослеЗаписи/ПриЧтенииНаСервере работают как trusted server-code"},
	}
}
