package compose

import (
	"strings"

	"github.com/ivantit66/onebase/internal/dsl/lexer"
	"github.com/ivantit66/onebase/internal/dsl/token"
	"github.com/ivantit66/onebase/internal/report"
)

type measureContext struct {
	grand, previous, first Row
	grandSelf, firstSelf   bool
}

// Context is passive scalar data, evaluated by the same sandbox as ordinary
// expressions. It never becomes a callback or an object with methods.
func contextBindings(expr string, current Row, measures []report.Measure, ctx measureContext) (Row, bool) {
	if len(expr) > maxDependencyExprBytes {
		return current, true
	}
	fields := map[string]bool{}
	for _, m := range measures {
		fields[strings.ToLower(m.Field)] = true
	}
	var bindings Row
	lx := lexer.New(expr, "")
	for count := 0; count < maxDependencyExprTokens; count++ {
		t := lx.NextToken()
		if t.Type == token.EOF {
			break
		}
		if t.Type != token.IDENT || fields[strings.ToLower(t.Literal)] {
			continue
		}
		scope, field, ok := report.MeasureReference(t.Literal)
		if !ok {
			continue
		}
		var source Row
		switch scope {
		case "grand":
			source = ctx.grand
			if ctx.grandSelf {
				source = current
			}
		case "previous":
			source = ctx.previous
		case "first":
			source = ctx.first
			if ctx.firstSelf {
				source = current
			}
		}
		var value any
		for name, v := range source {
			if strings.EqualFold(name, field) {
				value = v
				break
			}
		}
		// An absent neighbouring column / undefined referenced measure has an
		// undefined result. It is not a runtime failure or an implicit zero.
		if value == nil {
			return current, false
		}
		if bindings == nil {
			bindings = make(Row, len(current)+3)
			for name, v := range current {
				bindings[name] = v
			}
		}
		bindings[t.Literal] = value
	}
	if bindings == nil {
		return current, true
	}
	return bindings, true
}
