package query

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/ivantit66/onebase/internal/metadata"
)

// ReportFieldOrigin identifies a read, including its owning FROM occurrence.
// A navigation records each hop: the protected root cannot be erased by .Ref.
// These are server descriptors, never client/preset authorization tokens.
type ReportFieldOrigin struct {
	Occurrence int
	Source     SourceRef
	Field      string
	Type       metadata.FieldType
	Path       []string
}

// ReportExpression is a detached inspection view. Literal values are omitted;
// their digest identifies a fixed server expression without copying its data.
type ReportExpression struct {
	Op            string
	Type          metadata.FieldType
	Parameter     string
	LiteralDigest [32]byte
	Origins       []ReportFieldOrigin
	Args          []ReportExpression
}
type ReportLineageOutput struct {
	Name       string
	Expression ReportExpression
}
type ReportLineageUse struct {
	Clause     string
	Expression ReportExpression
}

// ReportLineage is an immutable compiler receipt, NOT permission to read PII.
// This first profile accepts one SELECT with physical JOIN sources. Derived
// scopes, UNION, virtual tables and unknown syntax return no partial receipt.
// Output policy remains mandatory even when the lineage is fully known.
type ReportLineage struct {
	outputs                         []ReportLineageOutput
	uses                            []ReportLineageUse
	resultDigest, environmentDigest [32]byte
}

func (p *ReportLineage) Outputs() []ReportLineageOutput {
	if p == nil {
		return nil
	}
	return cloneLineage(p.outputs)
}
func (p *ReportLineage) Uses() []ReportLineageUse {
	if p == nil {
		return nil
	}
	return cloneLineage(p.uses)
}
func cloneLineage[T any](value T) T {
	b, _ := json.Marshal(value)
	var out T
	_ = json.Unmarshal(b, &out)
	return out
}

// Matches invalidates the receipt after SQL/args/schema/RLS/metadata changes.
// It is still the host's job to prove actor/read/RLS and each permitted use.
func (p *ReportLineage) Matches(res Result, opts CompileOpts) bool {
	if p == nil {
		return false
	}
	a, ok := lineageResultDigest(res)
	if !ok || a != p.resultDigest {
		return false
	}
	b, ok := lineageEnvironmentDigest(opts)
	return ok && b == p.environmentDigest
}
func lineageResultDigest(res Result) ([32]byte, bool) {
	if !lineageArgumentsSupported(res.Args) {
		return [32]byte{}, false
	}
	return lineageTypedDigest(res)
}
func lineageEnvironmentDigest(opts CompileOpts) ([32]byte, bool) {
	type filter struct {
		Key       SourceRef
		Predicate any
	}
	filters := make([]filter, 0, len(opts.RowFilters))
	for key, value := range opts.RowFilters {
		filters = append(filters, filter{key, value})
	}
	sort.Slice(filters, func(i, j int) bool {
		a, b := filters[i].Key, filters[j].Key
		if a.Kind != b.Kind {
			return a.Kind < b.Kind
		}
		if a.Name != b.Name {
			return a.Name < b.Name
		}
		return a.TablePart < b.TablePart
	})
	for _, value := range opts.Params {
		if !lineageArgumentsSupported([]any{value}) {
			return [32]byte{}, false
		}
	}
	return lineageTypedDigest([]any{opts.Entities, opts.Registers, opts.InfoRegs, opts.AccountRegs, opts.Params, filters})
}

type reportExpr struct {
	op, value string
	path      []string
	args      []*reportExpr
	view      ReportExpression
	done      bool
}
type reportSource struct {
	ref       SourceRef
	qualifier string
	fields    []metadata.Field
	self      string
}
type reportOutput struct {
	name string
	expr *reportExpr
}
type reportUse struct {
	clause string
	expr   *reportExpr
}
type reportLineageParser struct {
	tokens     []tok
	pos, nodes int
	opts       CompileOpts
	sources    []reportSource
	outputs    []reportOutput
	uses       []reportUse
}

// CompileReportLineage opts into the strict analysis profile without changing
// Compile, ordinary forms, the existing report gate, or any execution path.
func CompileReportLineage(src string, opts CompileOpts) (Result, *ReportLineage, error) {
	if len(src) > 64<<10 {
		return Result{}, nil, fmt.Errorf("report lineage: source budget")
	}
	tokens, _, _, err := extractFirstN(tokenize(src))
	if err != nil {
		return Result{}, nil, err
	}
	if len(tokens) > 8192 {
		return Result{}, nil, fmt.Errorf("report lineage: token budget")
	}
	depth := 0
	for _, t := range tokens {
		if t.kind == tLParen {
			depth++
			if depth > 64 {
				return Result{}, nil, fmt.Errorf("report lineage: depth budget")
			}
		}
		if t.kind == tRParen {
			depth--
			if depth < 0 {
				return Result{}, nil, fmt.Errorf("report lineage: unbalanced expression")
			}
		}
	}
	if depth != 0 {
		return Result{}, nil, fmt.Errorf("report lineage: unbalanced expression")
	}
	tokens, analysisOpts := rewriteTablePartSources(tokens, opts)
	p := &reportLineageParser{tokens: tokens, opts: analysisOpts}
	if err = p.parse(); err != nil {
		return Result{}, nil, err
	}
	res, err := Compile(src, opts)
	if err != nil {
		return Result{}, nil, err
	}
	if !res.Projection.Simple || len(res.Projection.Columns) != len(p.outputs) {
		return Result{}, nil, fmt.Errorf("report lineage: projection mismatch")
	}
	out := &ReportLineage{}
	seen := map[string]bool{}
	for _, column := range p.outputs {
		view, err := p.bind(column.expr, "", 0)
		if err != nil {
			return Result{}, nil, err
		}
		if seen[metadata.SQLIdent(column.name)] {
			return Result{}, nil, fmt.Errorf("report lineage: duplicate output")
		}
		seen[metadata.SQLIdent(column.name)] = true
		out.outputs = append(out.outputs, ReportLineageOutput{column.name, view})
	}
	for _, use := range p.uses {
		view, err := p.bind(use.expr, use.clause, 0)
		if err != nil {
			return Result{}, nil, err
		}
		out.uses = append(out.uses, ReportLineageUse{use.clause, view})
	}
	var ok bool
	out.resultDigest, ok = lineageResultDigest(res)
	if !ok {
		return Result{}, nil, fmt.Errorf("report lineage: unsupported result value")
	}
	out.environmentDigest, ok = lineageEnvironmentDigest(opts)
	if !ok {
		return Result{}, nil, fmt.Errorf("report lineage: unsupported environment value")
	}
	return res, out, nil
}
func (p *reportLineageParser) peek() tok {
	if p.pos >= len(p.tokens) {
		return tok{kind: tEOF}
	}
	return p.tokens[p.pos]
}
func (p *reportLineageParser) kw() string {
	if p.peek().kind != tIdent {
		return ""
	}
	s, _ := sqlKW(p.peek().val)
	return s
}
func (p *reportLineageParser) take(kw string) bool {
	if p.kw() != kw {
		return false
	}
	p.pos++
	return true
}
func (p *reportLineageParser) want(kw string) error {
	if !p.take(kw) {
		return fmt.Errorf("report lineage: expected %s", kw)
	}
	return nil
}
func (p *reportLineageParser) newExpr(op, value string, args ...*reportExpr) (*reportExpr, error) {
	p.nodes++
	if p.nodes > 2048 {
		return nil, fmt.Errorf("report lineage: expression budget")
	}
	return &reportExpr{op: op, value: value, args: args}, nil
}
func (p *reportLineageParser) parse() error {
	if err := p.want("SELECT"); err != nil {
		return err
	}
	p.take("DISTINCT")
	p.take("ALL")
	ctx := preScanSourceContextWithOpts(p.tokens, p.opts)
	if len(ctx.scopes) != 1 {
		return fmt.Errorf("report lineage: unsupported SELECT scopes")
	}
	keys := newProjectionKeyResolver(p.tokens, p.opts, ctx)
	for {
		from := p.pos
		e, err := p.expr(1, 0)
		if err != nil {
			return err
		}
		if p.take("AS") {
			if p.peek().kind != tIdent || p.kw() != "" {
				return fmt.Errorf("report lineage: output alias")
			}
			// The ordinary compiler canonicalizes reference aliases and may
			// rewrite GROUP into the SELECT expression. This opt-in profile
			// refuses that special case until both bindings share one proof.
			alias := lowerFast(p.peek().val)
			if isReferenceName(alias) || alias == "id" {
				return fmt.Errorf("report lineage: reserved reference output alias")
			}
			p.pos++
		}
		column, _ := parseProjectionItem(p.tokens[from:p.pos])
		name, known := keys.outputSQLKey(column, from, p.pos)
		if !known || name == "" || column.Star {
			return fmt.Errorf("report lineage: output key unknown")
		}
		p.outputs = append(p.outputs, reportOutput{name, e})
		if p.peek().kind != tComma {
			break
		}
		p.pos++
	}
	if err := p.want("FROM"); err != nil {
		return err
	}
	if err := p.source(); err != nil {
		return err
	}
	for {
		join := false
		if p.take("INNER") || p.take("LEFT") || p.take("RIGHT") || p.take("FULL") {
			p.take("OUTER")
			if err := p.want("JOIN"); err != nil {
				return err
			}
			join = true
		} else if p.take("JOIN") {
			join = true
		}
		if !join {
			break
		}
		if err := p.source(); err != nil {
			return err
		}
		if err := p.want("ON"); err != nil {
			return err
		}
		e, err := p.expr(1, 0)
		if err != nil {
			return err
		}
		p.uses = append(p.uses, reportUse{"join", e})
	}
	if p.take("WHERE") {
		e, err := p.expr(1, 0)
		if err != nil {
			return err
		}
		p.uses = append(p.uses, reportUse{"where", e})
	}
	if p.take("GROUP") {
		if err := p.wantBy(); err != nil {
			return err
		}
		if err := p.list("group"); err != nil {
			return err
		}
	}
	if p.take("HAVING") {
		e, err := p.expr(1, 0)
		if err != nil {
			return err
		}
		p.uses = append(p.uses, reportUse{"having", e})
	}
	if p.take("ORDER") {
		if err := p.wantBy(); err != nil {
			return err
		}
		if err := p.list("order"); err != nil {
			return err
		}
	}
	if p.peek().kind != tEOF {
		return fmt.Errorf("report lineage: unsupported query tail")
	}
	return nil
}
func (p *reportLineageParser) wantBy() error {
	if p.take("BY") {
		return nil
	}
	if p.peek().kind == tIdent && strings.EqualFold(p.peek().val, "ПО") {
		p.pos++
		return nil
	}
	return fmt.Errorf("report lineage: expected BY")
}
func (p *reportLineageParser) list(clause string) error {
	for {
		e, err := p.expr(1, 0)
		if err != nil {
			return err
		}
		if lineageOrdinalExpression(e) {
			return fmt.Errorf("report lineage: ordinal use unsupported")
		}
		p.uses = append(p.uses, reportUse{clause, e})
		if clause == "order" {
			if !p.take("ASC") {
				p.take("DESC")
			}
		}
		if p.peek().kind != tComma {
			return nil
		}
		p.pos++
	}
}
func (p *reportLineageParser) source() error {
	at := p.pos
	if at+2 >= len(p.tokens) || p.tokens[at].kind != tIdent || !isSourceType(upperFast(p.tokens[at].val)) || p.tokens[at+1].kind != tDot || p.tokens[at+2].kind != tIdent {
		return fmt.Errorf("report lineage: unsupported source")
	}
	typ, name := upperFast(p.tokens[at].val), p.tokens[at+2].val
	p.pos += 3
	if p.peek().kind == tDot {
		return fmt.Errorf("report lineage: virtual source unsupported")
	}
	source := reportSource{ref: SourceRef{Kind: sourcePermKind(typ), Name: name}, qualifier: sourceToTable(typ, name)}
	if tps, ok := p.opts.tablePartSources[lowerFast(name)]; ok {
		if _, unique := lineageUniqueEntity(p.opts.Entities, tps.owner.Name); !unique {
			return fmt.Errorf("report lineage: ambiguous tablepart owner")
		}
		if _, unique := lineageUniqueEntity(p.opts.Entities, name); !unique {
			return fmt.Errorf("report lineage: ambiguous tablepart source")
		}
		source.ref.Name = tps.owner.Name
		source.ref.TablePart = tps.tp.Name
		source.fields = metadata.TablePartQueryEntity(tps.owner, tps.tp, name).Fields
		source.self = tps.owner.Name
	} else {
		count := 0
		switch source.ref.Kind {
		case "catalog", "document":
			ent, ok := lineageUniqueEntity(p.opts.Entities, name)
			if ok && string(ent.Kind) == source.ref.Kind {
				count = 1
				source.fields, source.self = ent.Fields, ent.Name
			}
		case "inforeg":
			for _, reg := range p.opts.InfoRegs {
				if reg != nil && strings.EqualFold(reg.Name, name) {
					count++
					source.fields = append(append([]metadata.Field(nil), reg.Dimensions...), reg.Resources...)
					if reg.Periodic {
						source.fields = append(source.fields, metadata.Field{Name: "period", Type: metadata.FieldTypeDate})
					}
				}
			}
		case "register":
			for _, reg := range p.opts.Registers {
				if reg != nil && strings.EqualFold(reg.Name, name) {
					count++
					source.fields = append(append(append([]metadata.Field(nil), reg.Dimensions...), reg.Resources...), reg.Attributes...)
				}
			}
			for _, reg := range p.opts.AccountRegs {
				if reg != nil && strings.EqualFold(reg.Name, name) {
					count++
					source.fields = append(append([]metadata.Field(nil), reg.Resources...), reg.Subconto...)
				}
			}
		}
		if count != 1 {
			return fmt.Errorf("report lineage: source metadata unknown or ambiguous")
		}
	}
	if p.take("AS") {
		if p.peek().kind != tIdent || p.kw() != "" {
			return fmt.Errorf("report lineage: source alias")
		}
		source.qualifier = lowerFast(p.peek().val)
		p.pos++
	}
	for _, old := range p.sources {
		if strings.EqualFold(old.qualifier, source.qualifier) {
			return fmt.Errorf("report lineage: duplicate source qualifier")
		}
	}
	p.sources = append(p.sources, source)
	return nil
}
func (p *reportLineageParser) binary() (string, int) {
	if p.peek().kind == tOp {
		switch p.peek().val {
		case "=", "<>", "!=", "<", ">", "<=", ">=":
			return p.peek().val, 3
		case "+", "-":
			return p.peek().val, 4
		case "/", "%":
			return p.peek().val, 5
		}
	}
	if p.peek().kind == tStar {
		return "*", 5
	}
	switch p.kw() {
	case "OR":
		return "OR", 1
	case "AND":
		return "AND", 2
	case "IS":
		return "IS", 3
	case "LIKE":
		return "LIKE", 3
	}
	return "", 0
}
func (p *reportLineageParser) expr(min, depth int) (*reportExpr, error) {
	if depth > 64 {
		return nil, fmt.Errorf("report lineage: expression depth")
	}
	left, err := p.atom(depth + 1)
	if err != nil {
		return nil, err
	}
	for {
		op, prec := p.binary()
		if prec < min || op == "" {
			return left, nil
		}
		p.pos++
		if op == "IS" {
			not := p.take("NOT")
			if !p.take("NULL") {
				return nil, fmt.Errorf("report lineage: unknown null predicate")
			}
			if not {
				op = "IS NOT NULL"
			} else {
				op = "IS NULL"
			}
			left, err = p.newExpr(op, "", left)
		} else {
			var right *reportExpr
			right, err = p.expr(prec+1, depth+1)
			if err == nil {
				left, err = p.newExpr(op, "", left, right)
			}
		}
		if err != nil {
			return nil, err
		}
	}
}
func (p *reportLineageParser) atom(depth int) (*reportExpr, error) {
	t := p.peek()
	p.pos++
	switch t.kind {
	case tParam:
		return p.newExpr("parameter", t.val)
	case tStr:
		return p.newExpr("string", t.val)
	case tNum:
		return p.newExpr("number", t.val)
	case tLParen:
		e, err := p.expr(1, depth+1)
		if err != nil {
			return nil, err
		}
		if p.peek().kind != tRParen {
			return nil, fmt.Errorf("report lineage: expected close parenthesis")
		}
		p.pos++
		return e, nil
	case tOp:
		if t.val == "-" || t.val == "+" {
			e, err := p.expr(6, depth+1)
			if err != nil {
				return nil, err
			}
			return p.newExpr("unary"+t.val, "", e)
		}
	case tIdent:
		kw, _ := sqlKW(t.val)
		switch kw {
		case "TRUE", "FALSE":
			return p.newExpr("bool", kw)
		case "NULL":
			return p.newExpr("null", "")
		case "NOT":
			e, err := p.expr(3, depth+1)
			if err != nil {
				return nil, err
			}
			return p.newExpr("NOT", "", e)
		case "CASE":
			var args []*reportExpr
			for p.take("WHEN") {
				cond, err := p.expr(1, depth+1)
				if err != nil {
					return nil, err
				}
				if err = p.want("THEN"); err != nil {
					return nil, err
				}
				value, err := p.expr(1, depth+1)
				if err != nil {
					return nil, err
				}
				args = append(args, cond, value)
			}
			if len(args) == 0 || !p.take("ELSE") {
				return nil, fmt.Errorf("report lineage: unsupported CASE")
			}
			value, err := p.expr(1, depth+1)
			if err != nil {
				return nil, err
			}
			args = append(args, value)
			if err = p.want("END"); err != nil {
				return nil, err
			}
			return p.newExpr("CASE", "", args...)
		}
		if p.peek().kind == tLParen {
			return p.call(t.val, depth+1)
		}
		if kw != "" {
			return nil, fmt.Errorf("report lineage: unexpected keyword")
		}
		path := []string{t.val}
		for p.peek().kind == tDot {
			p.pos++
			if p.peek().kind != tIdent || len(path) >= 8 {
				return nil, fmt.Errorf("report lineage: field path")
			}
			path = append(path, p.peek().val)
			p.pos++
		}
		e, err := p.newExpr("field", "")
		if err == nil {
			e.path = path
		}
		return e, err
	}
	return nil, fmt.Errorf("report lineage: unknown expression")
}
func (p *reportLineageParser) call(name string, depth int) (*reportExpr, error) {
	op, agg := sqlAgg(name)
	if !agg {
		switch upperFast(name) {
		case "ЕСТЬNULL", "ISNULL", "COALESCE":
			op = "COALESCE"
		case "SUBSTR", "SUBSTRING":
			op = "SUBSTR"
		case "LENGTH", "ДЛИНАСТРОКИ":
			op = "LENGTH"
		case "CAST":
			op = "CAST"
		default:
			return nil, fmt.Errorf("report lineage: unknown function")
		}
	}
	p.pos++ // (
	var args []*reportExpr
	if op == "CAST" {
		e, err := p.expr(1, depth+1)
		if err != nil {
			return nil, err
		}
		if err = p.want("AS"); err != nil {
			return nil, err
		}
		typ := upperFast(p.peek().val)
		if p.peek().kind != tIdent || (typ != "TEXT" && typ != "NUMERIC" && typ != "INTEGER") {
			return nil, fmt.Errorf("report lineage: unsupported cast")
		}
		p.pos++
		if p.peek().kind != tRParen {
			return nil, fmt.Errorf("report lineage: cast boundary")
		}
		p.pos++
		return p.newExpr("CAST", typ, e)
	}
	distinct := p.take("DISTINCT")
	if op == "COUNT" && p.peek().kind == tStar {
		p.pos++
		args = append(args, &reportExpr{op: "row-count"})
	} else {
		for {
			e, err := p.expr(1, depth+1)
			if err != nil {
				return nil, err
			}
			args = append(args, e)
			if p.peek().kind != tComma {
				break
			}
			p.pos++
		}
	}
	if p.peek().kind != tRParen {
		return nil, fmt.Errorf("report lineage: function boundary")
	}
	p.pos++
	if distinct {
		op += " DISTINCT"
	}
	return p.newExpr(op, "", args...)
}
func (p *reportLineageParser) bind(e *reportExpr, clause string, depth int) (ReportExpression, error) {
	if depth > 64 {
		return ReportExpression{}, fmt.Errorf("report lineage: bind depth")
	}
	if e.done {
		return e.view, nil
	}
	view := ReportExpression{Op: e.op}
	switch e.op {
	case "parameter":
		view.Parameter = e.value
	case "string", "number", "bool", "null":
		view.Type = metadata.FieldType(e.op)
		view.LiteralDigest = sha256.Sum256([]byte(e.op + ":" + e.value))
	case "row-count":
		view.Type = metadata.FieldTypeNumber
	case "field":
		if clause == "order" && len(e.path) == 1 {
			for _, out := range p.outputs {
				if strings.EqualFold(out.name, e.path[0]) {
					v, err := p.bind(out.expr, "", depth+1)
					if err != nil {
						return view, err
					}
					return v, nil
				}
			}
		}
		var err error
		view.Origins, view.Type, err = p.resolve(e.path)
		if err != nil {
			return view, err
		}
	default:
		for _, arg := range e.args {
			child, err := p.bind(arg, "", depth+1)
			if err != nil {
				return view, err
			}
			view.Args = append(view.Args, child)
		}
		var err error
		view.Type, err = lineageExprType(e.op, e.value, view.Args)
		if err != nil {
			return view, err
		}
	}
	e.view, e.done = view, true
	return view, nil
}
func lineageExprType(op, value string, args []ReportExpression) (metadata.FieldType, error) {
	fail := func() (metadata.FieldType, error) {
		return "", fmt.Errorf("report lineage: expression type not proven")
	}
	isNumber := func(i int) bool { return i < len(args) && args[i].Type == metadata.FieldTypeNumber }
	switch op {
	case "OR", "AND", "NOT", "=", "<>", "!=", "<", ">", "<=", ">=", "IS NULL", "IS NOT NULL", "LIKE":
		return metadata.FieldTypeBool, nil
	case "+", "-", "*", "/", "%":
		if len(args) == 2 && isNumber(0) && isNumber(1) {
			return metadata.FieldTypeNumber, nil
		}
	case "unary+", "unary-":
		if len(args) == 1 && isNumber(0) {
			return metadata.FieldTypeNumber, nil
		}
	case "COUNT", "COUNT DISTINCT":
		if len(args) == 1 {
			return metadata.FieldTypeNumber, nil
		}
	case "SUM", "AVG":
		if len(args) == 1 && isNumber(0) {
			return metadata.FieldTypeNumber, nil
		}
	case "MIN", "MAX":
		if len(args) == 1 && args[0].Type != "" {
			return args[0].Type, nil
		}
	case "COALESCE":
		var typ metadata.FieldType
		for _, arg := range args {
			if arg.Type == "null" {
				continue
			}
			if arg.Type == "" || (typ != "" && typ != arg.Type) {
				return fail()
			}
			typ = arg.Type
		}
		if len(args) >= 2 && typ != "" {
			return typ, nil
		}
	case "SUBSTR":
		if (len(args) == 2 || len(args) == 3) && args[0].Type == metadata.FieldTypeString && isNumber(1) && (len(args) == 2 || isNumber(2)) {
			return metadata.FieldTypeString, nil
		}
	case "LENGTH":
		if len(args) == 1 && args[0].Type == metadata.FieldTypeString {
			return metadata.FieldTypeNumber, nil
		}
	case "CAST":
		if len(args) == 1 {
			if value == "TEXT" {
				return metadata.FieldTypeString, nil
			}
			return metadata.FieldTypeNumber, nil
		}
	case "CASE":
		if len(args) >= 3 && len(args)%2 == 1 {
			typ := args[len(args)-1].Type
			for i := 0; i < len(args)-1; i += 2 {
				if args[i].Type != metadata.FieldTypeBool || args[i+1].Type != typ || typ == "" {
					return fail()
				}
			}
			return typ, nil
		}
	}
	return fail()
}
func (p *reportLineageParser) resolve(path []string) ([]ReportFieldOrigin, metadata.FieldType, error) {
	fail := func() ([]ReportFieldOrigin, metadata.FieldType, error) {
		return nil, "", fmt.Errorf("report lineage: field source unknown or ambiguous")
	}
	var candidates []int
	fieldPath := path
	if len(path) > 1 {
		for i, s := range p.sources {
			if strings.EqualFold(s.qualifier, path[0]) {
				candidates = append(candidates, i)
			}
		}
		if len(candidates) > 0 {
			fieldPath = path[1:]
		} else {
			return fail()
		}
	} else {
		for i, s := range p.sources {
			if _, ok := lineageOwnField(s, path[0]); ok {
				candidates = append(candidates, i)
			}
		}
	}
	if len(candidates) != 1 || len(fieldPath) == 0 {
		return fail()
	}
	id := candidates[0]
	source := p.sources[id]
	var origins []ReportFieldOrigin
	for hop, name := range fieldPath {
		field, ok := lineageOwnField(source, name)
		if !ok {
			return fail()
		}
		origins = append(origins, ReportFieldOrigin{Occurrence: id, Source: source.ref, Field: field.Name, Type: field.Type, Path: append([]string(nil), fieldPath[:hop+1]...)})
		ref := field.RefEntity
		if metadata.IsReference(field.Type) {
			target := metadata.RefName(field.Type)
			if ref != "" && ref != target {
				return fail()
			}
			ref = target
		}
		if hop == len(fieldPath)-1 {
			if ref != "" && field.Name != "id" && !(source.self != "" && (strings.EqualFold(name, "Ссылка") || strings.EqualFold(name, "Ref"))) {
				return fail()
			}
			return origins, field.Type, nil
		}
		if ref == "" {
			return fail()
		}
		var entity *metadata.Entity
		for _, ent := range p.opts.Entities {
			if ent != nil && strings.EqualFold(ent.Name, ref) {
				if entity != nil {
					return fail()
				}
				entity = ent
			}
		}
		if entity == nil {
			return fail()
		}
		source = reportSource{ref: SourceRef{Kind: string(entity.Kind), Name: entity.Name}, fields: entity.Fields, self: entity.Name}
	}
	return fail()
}
func lineageOwnField(source reportSource, name string) (metadata.Field, bool) {
	var field metadata.Field
	count := 0
	for _, f := range source.fields {
		if strings.EqualFold(f.Name, name) {
			field = f
			count++
		}
	}
	if count > 0 {
		return field, count == 1
	}
	if source.self != "" && (strings.EqualFold(name, "id") || strings.EqualFold(name, "Ссылка") || strings.EqualFold(name, "Ref")) {
		return metadata.Field{Name: "id", Type: metadata.FieldType("reference:" + source.self)}, true
	}
	if strings.EqualFold(name, "deletion_mark") {
		return metadata.Field{Name: "deletion_mark", Type: metadata.FieldTypeBool}, true
	}
	if source.ref.Kind == "document" && strings.EqualFold(name, "posted") {
		return metadata.Field{Name: "posted", Type: metadata.FieldTypeBool}, true
	}
	return metadata.Field{}, false
}

func lineageUniqueEntity(entities []*metadata.Entity, name string) (*metadata.Entity, bool) {
	var found *metadata.Entity
	for _, ent := range entities {
		if ent != nil && strings.EqualFold(ent.Name, name) {
			if found != nil {
				return nil, false
			}
			found = ent
		}
	}
	return found, found != nil
}

func lineageOrdinalExpression(e *reportExpr) bool {
	if e.op == "number" {
		return true
	}
	return (e.op == "unary+" || e.op == "unary-") && len(e.args) == 1 && lineageOrdinalExpression(e.args[0])
}
