package rowsource

import (
	"context"
	"crypto/sha256"
	"fmt"
	"strings"
	"time"
	"unicode"

	"github.com/ivantit66/onebase/internal/access"
	"github.com/ivantit66/onebase/internal/auth"
	"github.com/ivantit66/onebase/internal/dsl/ast"
	"github.com/ivantit66/onebase/internal/dsl/interpreter"
	"github.com/ivantit66/onebase/internal/query"
	"github.com/ivantit66/onebase/internal/report/resultfilter"
	"github.com/ivantit66/onebase/internal/runtime"
	"github.com/ivantit66/onebase/internal/storage"
)

const (
	maxRows       = 50000
	maxReadRows   = 100000
	maxQueries    = 256
	maxBytes      = 8 << 20
	maxValueBytes = 1 << 20
)

// Extra is trusted configuration metadata, never a descriptor returned by DSL
// or a client preset. This initial contract accepts scalar string/number/bool/date
// outputs; it never infers a reference from a returned UUID-looking string.
type Extra struct{ Name, Type string }
type Source struct {
	Report, Entry, Code string
	Extras              []Extra
}
type Query struct {
	Text   string
	Params map[string]any
}

// Origin is host-generated evidence. It is deliberately excluded from client
// output: runtime adapters must retain it server-side for every later sink.
type Origin struct {
	Kind                              string
	Source                            query.SourceRef
	TablePart, Field, Type, RefEntity string
	Path                              []string
}
type Column struct {
	Name, Type, RefEntity string
	Origins               []Origin `json:"-"`
}
type Result struct {
	Rows         []map[string]any
	Columns      []Column
	ModuleSHA256 [32]byte `json:"-"`
	Generation   uint64   `json:"-"`
	Operations   []string `json:"-"`
}

type execution struct {
	db                           *storage.DB
	reg                          *runtime.Registry
	lookup                       resultfilter.Lookup
	fields                       map[string][]resultfilter.Field
	origins                      []Origin
	originKeys                   map[string]bool
	queries, readRows, readBytes int
}

// Run owns compilation and all database reads. It has no caller-provided raw
// Query/guard callback that could silently bypass RLS or mandatory report policy.
// No partial result is returned on any budget, policy, schema or identity error.
func Run(ctx context.Context, db *storage.DB, reg *runtime.Registry, initial Query, src Source) (*Result, error) {
	return runWithGeneration(ctx, db, reg, initial, src, nil)
}

func runWithGeneration(ctx context.Context, db *storage.DB, reg *runtime.Registry, initial Query, src Source, expectedGeneration *uint64) (*Result, error) {
	if db == nil || reg == nil {
		return nil, fmt.Errorf("row source: неизвестный отчёт")
	}
	generation := reg.Generation()
	if expectedGeneration != nil && generation != *expectedGeneration {
		return nil, fmt.Errorf("row source: конфигурация изменилась после выбора источника")
	}
	if reg.GetReport(src.Report) == nil {
		return nil, fmt.Errorf("row source: неизвестный отчёт")
	}
	user := auth.UserFromContext(ctx)
	if user != nil && !user.Has("report", src.Report, "run") {
		return nil, fmt.Errorf("row source: нет права запуска отчёта")
	}
	p, err := compileProgram(src.Code, src.Entry)
	if err != nil {
		return nil, err
	}
	if len(src.Extras) == 0 || len(src.Extras) > 32 {
		return nil, fmt.Errorf("row source: нужна ограниченная схема добавленных полей")
	}
	seen := map[string]bool{}
	for _, extra := range src.Extras {
		key := strings.ToLower(extra.Name)
		if !fieldName(extra.Name) || seen[key] || !words("string number bool date datetime")[extra.Type] {
			return nil, fmt.Errorf("row source: неверная схема добавленных полей")
		}
		seen[key] = true
	}
	ctx, cancel := context.WithTimeout(storage.ReadOnlyContext(ctx), 10*time.Second)
	defer cancel()
	e := &execution{db: db, reg: reg, lookup: resultfilter.RegistryLookup(reg), fields: map[string][]resultfilter.Field{}, originKeys: map[string]bool{}}
	compiled, err := e.compile(ctx, initial.Text, initial.Params)
	if err != nil {
		return nil, err
	}
	fields := e.fields[compiled.SQL]
	for _, field := range fields {
		if seen[strings.ToLower(field.Name)] {
			return nil, fmt.Errorf("row source: добавленное поле пересекается с исходным")
		}
	}

	aliases := inputAliases(compiled, fields)
	for alias := range aliases {
		if seen[strings.ToLower(alias)] {
			return nil, fmt.Errorf("row source: добавленное поле пересекается с DSL именем исходного поля")
		}
	}
	rows, values, baseline, err := e.dataset(ctx, compiled)
	if err != nil {
		return nil, err
	}
	// New interpreter with no runtime/common-module lookup, ambient globals,
	// object managers, message callbacks, attachments, transactions or secrets.
	interp := interpreter.New()
	interp.StrictLexicalScope = true
	interp.MaxRecursionDepth = 32
	interp.LookupProc = func(name string) *ast.ProcedureDecl { return p.procedures[strings.ToLower(name)] }
	factory := func(args []any) any { return &opaqueQuery{execution: e, ctx: ctx, params: map[string]any{}} }
	vars := map[string]any{"Результат": values, "__factory_Запрос": factory, "__factory_Query": factory}
	profile := interpreter.RestrictedProfile()
	profile.Context = ctx
	profile.MaxLoopIters = 100000
	var returned any
	if err = interp.RunSandboxed(p.entry, nil, profile, &returned, vars); err != nil {
		return nil, err
	}
	if err = ctx.Err(); err != nil {
		return nil, err
	}
	if reg.Generation() != generation {
		return nil, fmt.Errorf("row source: конфигурация изменилась во время выполнения")
	}
	output, err := outputRows(returned, rows, baseline, fields, src.Extras)
	if err != nil {
		return nil, err
	}
	digest := sha256.Sum256([]byte(src.Code))
	result := &Result{Rows: output, ModuleSHA256: digest, Generation: generation, Operations: p.operationNames()}
	for _, field := range fields {
		result.Columns = append(result.Columns, Column{Name: field.Name, Type: field.Type, RefEntity: field.RefEntity, Origins: []Origin{fieldOrigin(field)}})
	}
	// Conservative whole-execution lineage: every extra depends on all inputs
	// and a literal origin in this exact vetted module. This over-approximates
	// control flow; a declaration/alias is never used as origin evidence.
	origins := append([]Origin(nil), e.origins...)
	origins = append(origins, Origin{Kind: "literal"}, Origin{Kind: "validated-program"})
	for _, extra := range src.Extras {
		result.Columns = append(result.Columns, Column{Name: extra.Name, Type: extra.Type, Origins: append([]Origin(nil), origins...)})
	}
	return result, nil
}

func fieldName(name string) bool {
	if name == "" || len(name) > 128 {
		return false
	}
	for i, r := range name {
		if r != '_' && !unicode.IsLetter(r) && !(i > 0 && unicode.IsDigit(r)) {
			return false
		}
	}
	return true
}
func fieldOrigin(f resultfilter.Field) Origin {
	return Origin{Kind: "field", Source: f.Source, TablePart: f.SourceTablePart, Field: f.SourceField, Type: f.Type, RefEntity: f.RefEntity, Path: append([]string(nil), f.SourcePath...)}
}

func (e *execution) compile(ctx context.Context, text string, params map[string]any) (query.Result, error) {
	if err := ctx.Err(); err != nil {
		return query.Result{}, err
	}
	if len(text) > 64<<10 {
		return query.Result{}, fmt.Errorf("row source: слишком длинный запрос")
	}
	depth := 0
	for _, r := range text {
		if r == '(' {
			depth++
			if depth > 64 {
				return query.Result{}, fmt.Errorf("row source: запрос слишком глубоко вложен")
			}
		}
		if r == ')' && depth > 0 {
			depth--
		}
	}
	e.queries++
	if e.queries > maxQueries {
		return query.Result{}, fmt.Errorf("row source: превышен лимит запросов")
	}
	user := auth.UserFromContext(ctx)
	filters, err := access.QueryRowFiltersWithContext(ctx, user, e.reg.Entities(), e.reg.Registers(), e.reg.InfoRegisters(), e.reg.AccountRegisters(), access.RuntimeContext(e.reg, e.db))
	if err != nil {
		return query.Result{}, err
	}
	res, err := query.Compile(text, query.CompileOpts{Params: params, Entities: e.reg.Entities(), Registers: e.reg.Registers(), InfoRegs: e.reg.InfoRegisters(), AccountRegs: e.reg.AccountRegisters(), Dialect: e.db.Dialect(), RowFilters: filters})
	if err != nil {
		return query.Result{}, err
	}
	if len(res.SQL) > 256<<10 || len(res.Projection.Columns) > 128 {
		return query.Result{}, fmt.Errorf("row source: запрос превышает предел сложности")
	}
	for _, source := range res.Sources {
		if e.lookup(source.Kind, source.Name) == nil {
			return query.Result{}, fmt.Errorf("row source: неизвестный источник")
		}
	}
	if access.DeniedReadSource(user, res.Sources) != "" {
		return query.Result{}, fmt.Errorf("row source: нет права чтения источника")
	}
	plan := access.ReportOutputQueryMaskPlan(user, res, e.lookup)
	if plan.Denied != "" {
		return query.Result{}, fmt.Errorf("row source: защищённое чтение запрещено")
	}
	fields := resultfilter.Schema(res, e.lookup, resultfilter.ReportAllowed(user))
	if !res.Projection.Simple || len(fields) == 0 || len(fields) != len(res.Projection.Columns) {
		return query.Result{}, fmt.Errorf("row source: тип или происхождение проекции не доказаны")
	}
	e.fields[res.SQL] = fields
	for _, f := range fields {
		key := fmt.Sprintf("%q/%q/%q/%q/%q", f.Source.Kind, f.Source.Name, f.SourceTablePart, f.SourceField, f.Type)
		if !e.originKeys[key] {
			e.originKeys[key] = true
			e.origins = append(e.origins, fieldOrigin(f))
		}
	}
	return res, nil
}

// dataset is the only database-result -> DSL boundary for both initial and
// additional queries. Ordinary interpreter/query_proxy materialization is never
// involved: its typed-empty policy deliberately differs from this kernel.
func (e *execution) dataset(ctx context.Context, res query.Result) ([]map[string]any, *interpreter.Array, []originalRow, error) {
	rows, err := e.QueryAll(ctx, res.SQL, res.Args...)
	if err != nil {
		return nil, nil, nil, err
	}
	query.RestoreLongLabels(&res, rows, nil)
	plan := access.ReportOutputQueryMaskPlan(auth.UserFromContext(ctx), res, e.lookup)
	if plan.Denied != "" {
		return nil, nil, nil, fmt.Errorf("row source: защищённое чтение запрещено")
	}
	tracked, err := plan.ApplyTracked(rows)
	if err != nil {
		return nil, nil, nil, err
	}
	for _, row := range tracked {
		if len(row) > 0 {
			return nil, nil, nil, fmt.Errorf("row source: замаскированный вход запрещён")
		}
	}
	query.NormalizeColumns(&res, rows)
	if len(rows) > maxRows {
		return nil, nil, nil, fmt.Errorf("row source: превышен лимит строк источника")
	}
	fields := e.fields[res.SQL]
	values, baseline, err := inputRows(rows, fields, e.reg, inputAliases(res, fields))
	if err != nil {
		return nil, nil, nil, err
	}
	return rows, values, baseline, nil
}

// These are compiler-proven alternate names for an existing scalar, not new
// data sources. They are supplied consistently in initial and additional rows.
func inputAliases(res query.Result, fields []resultfilter.Field) map[string]string {
	available := map[string]bool{}
	for _, f := range fields {
		available[strings.ToLower(f.Name)] = true
	}
	aliases := map[string]string{}
	for alias, target := range res.DSLColumnAliases {
		if logical, ok := res.LongIdents[target]; ok {
			target = logical
		}
		alias, target = strings.ToLower(alias), strings.ToLower(target)
		if available[target] && !available[alias] {
			aliases[alias] = target
		}
	}
	if available["id"] && res.RefColumns["id"] != "" {
		for _, alias := range []string{"ссылка", "reference", "ref"} {
			if !available[alias] {
				aliases[alias] = "id"
			}
		}
	}
	return aliases
}

func (e *execution) QueryAll(ctx context.Context, sql string, args ...any) ([]map[string]any, error) {
	if e.fields[sql] == nil {
		return nil, fmt.Errorf("row source: запрос без host proof")
	}
	var out []map[string]any
	_, err := e.db.VisitQuery(ctx, sql, args, func(row map[string]any, columns []string) (bool, error) {
		e.readRows++
		if e.readRows > maxReadRows {
			return false, fmt.Errorf("row source: превышен общий лимит чтения")
		}
		for _, value := range row {
			size, err := valueBytes(value)
			if err != nil {
				return false, err
			}
			e.readBytes += size
			if e.readBytes > maxBytes {
				return false, fmt.Errorf("row source: превышен размер входных данных")
			}
		}
		out = append(out, row)
		return true, nil
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}
