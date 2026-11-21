package rowsource

import (
	"crypto/sha256"
	"sort"
	"strings"

	"github.com/ivantit66/onebase/internal/dsl/ast"
	"github.com/ivantit66/onebase/internal/dsl/interpreter"
	"github.com/ivantit66/onebase/internal/dsl/token"
	"github.com/ivantit66/onebase/internal/provenance"
	"github.com/shopspring/decimal"
)

// Lexical evidence is private and is not authorization, an execution receipt or
// a final filter schema. A static AST value still needs receiver/data/control
// bindings before it can participate in any protected-operation recipe.
const pureProfileVersion = "rowsource-pure/2"

type literalEvidence struct {
	module, profile [32]byte
	node            int
	typ             string
	value           [32]byte // private typed digest, never logged or sent in inspection JSON
	contextRequired bool     // date interpretation also needs the host timezone frame
}
type textSiteEvidence struct {
	node                              int
	literal                           *literalEvidence
	receiverRequired, controlRequired bool
}
type programEvidence struct {
	module, profile [32]byte
	literals        map[ast.Expr]literalEvidence
	textSites       map[*ast.AssignStmt]textSiteEvidence
}

func newProgramEvidence(code string) (*programEvidence, error) {
	// Version the complete statement/type semantics as well as the closed sets.
	// No hash of string equality is used to classify a runtime Query.Text value.
	type profileSnapshot struct {
		Version                            string
		CodecFormat                        int
		Nodes, Depth, LoopIters, Recursion int
		Functions, Methods, Constructors   []string
	}
	names := func(values map[string]bool) []string {
		out := make([]string, 0, len(values))
		for name, allowed := range values {
			if allowed {
				out = append(out, name)
			}
		}
		sort.Strings(out)
		return out
	}
	profile, err := provenance.TypedDigest(profileSnapshot{pureProfileVersion, provenance.FormatVersion, 512, 64, 100000, 32, names(pureFunctions), names(dataMethods), names(dataConstructors)})
	if err != nil {
		return nil, err
	}
	return &programEvidence{module: sha256.Sum256([]byte(code)), profile: profile, literals: map[ast.Expr]literalEvidence{}, textSites: map[*ast.AssignStmt]textSiteEvidence{}}, nil
}
func (e *programEvidence) recordLiteral(expr ast.Expr, node int) error {
	info := literalEvidence{module: e.module, profile: e.profile, node: node}
	var value any
	switch v := expr.(type) {
	case *ast.StringLit:
		if len(v.Value) > 64<<10 {
			return provenance.ErrUnsupported
		}
		info.typ = "string"
		value = v.Value
	case *ast.NumberLit:
		if len(v.Value) > 128 {
			return provenance.ErrUnsupported
		}
		d, err := decimal.NewFromString(v.Value)
		if err != nil || !interpreter.DecimalWithinSafeBounds(d) {
			return provenance.ErrUnsupported
		}
		info.typ = "number"
		value = d
	case *ast.BoolLit:
		info.typ = "bool"
		value = v.Value
	case *ast.DateLit:
		info.typ = "date"
		info.contextRequired = true
		value = v.Value
	default:
		return nil
	}
	digest, err := provenance.InputDigest(value)
	if err != nil {
		return err
	}
	info.value = digest
	e.literals[expr] = info
	return nil
}
func (e *programEvidence) literal(expr ast.Expr) (literalEvidence, bool) {
	if e == nil {
		return literalEvidence{}, false
	}
	stored, ok := e.literals[expr]
	if !ok {
		return literalEvidence{}, false
	}
	// A later mutation of this private AST also invalidates its lexical evidence.
	scratch := &programEvidence{module: e.module, profile: e.profile, literals: map[ast.Expr]literalEvidence{}}
	if err := scratch.recordLiteral(expr, stored.node); err != nil {
		return literalEvidence{}, false
	}
	current, ok := scratch.literals[expr]
	return stored, ok && current == stored
}
func (e *programEvidence) recordTextSite(stmt *ast.AssignStmt, node int) {
	member, ok := stmt.Target.(*ast.MemberExpr)
	if !ok || (!strings.EqualFold(member.Field.Literal, "Text") && !strings.EqualFold(member.Field.Literal, "Текст")) {
		return
	}
	site := textSiteEvidence{node: node, receiverRequired: true, controlRequired: true}
	if stmt.Op == token.ASSIGN {
		// Value equality, variable lookup, concat and a same-valued function return
		// cannot mint a static-text origin. Only this exact lexical StringLit can.
		if _, ok := stmt.Value.(*ast.StringLit); ok {
			if proof, known := e.literal(stmt.Value); known {
				site.literal = &proof
			}
		}
	}
	e.textSites[stmt] = site
}
