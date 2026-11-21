package dslvars

import (
	"strings"

	"github.com/ivantit66/onebase/internal/auth"
	"github.com/ivantit66/onebase/internal/dsl/interpreter"
	"github.com/ivantit66/onebase/internal/metadata"
)

// accessRightBuiltin returns only the current actor's object permission. It
// neither enumerates roles nor confers access: actual reads/writes still check
// their own ACL, field policies and RLS. No database access is needed here.
func (c Common) accessRightBuiltin(ctxSource interpreter.CtxSource) interpreter.BuiltinFunc {
	return func(args []any, _ string, _ int) (any, error) {
		if len(args) != 3 || c.Reg == nil {
			return false, nil
		}
		kindWord, kindOK := args[0].(string)
		name, nameOK := args[1].(string)
		opWord, opOK := args[2].(string)
		if !kindOK || !nameOK || !opOK {
			return false, nil
		}
		kind, op := auth.KindFromWord(kindWord), auth.NormalizeOp(opWord)
		switch op {
		case "read", "write", "delete", "post", "unpost", "run", "disclose", "create":
		default:
			return false, nil
		}
		ctx := ctxSource.Ctx()
		if ctx == nil {
			return false, nil
		}
		u := auth.UserFromContext(ctx)
		if u == nil || strings.TrimSpace(u.ID) == "" {
			return false, nil
		}
		// Resolve names using the same metadata registry before User.Has. In
		// particular, admin must not turn misspelled/nonexistent objects true.
		name = strings.TrimSpace(name)
		canonical := ""
		switch kind {
		case "catalog", "document":
			if e := c.Reg.GetEntity(name); e != nil &&
				((kind == "catalog" && e.Kind == metadata.KindCatalog) || (kind == "document" && e.Kind == metadata.KindDocument)) {
				canonical = e.Name
			}
		case "register":
			if r := c.Reg.GetRegister(name); r != nil {
				canonical = r.Name
			}
		case "inforeg":
			if r := c.Reg.GetInfoRegister(name); r != nil {
				canonical = r.Name
			}
		case "report":
			if r := c.Reg.GetReport(name); r != nil {
				canonical = r.Name
			}
		case "processor":
			if p := c.Reg.GetProcessor(name); p != nil {
				canonical = p.Name
			}
		}
		if canonical == "" {
			return false, nil
		}
		return u.Has(kind, canonical, op), nil
	}
}
