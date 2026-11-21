package rowsource

import (
	"context"
	"fmt"
	"github.com/ivantit66/onebase/internal/runtime"
	"github.com/ivantit66/onebase/internal/storage"
	"strings"
	"time"

	"github.com/ivantit66/onebase/internal/access"
	"github.com/ivantit66/onebase/internal/auth"
	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/ivantit66/onebase/internal/provenance"
	"github.com/ivantit66/onebase/internal/query"
	"github.com/ivantit66/onebase/internal/report/resultfilter"
)

// This private sidecar is SQL/environment integrity plus conservative policy
// checks. It is NOT input-kind/text-staticness evidence or a producer seal.
// No new expression/native output is admitted: the old all-column Schema stays.
const sqlProofProfile = "rowsource/sql-proof-e1-v2/1"

type guardedSQLProof struct {
	database   *storage.DB
	registry   *runtime.Registry
	dialect    string
	timezone   *time.Location
	receipt    *query.ReportLineage
	result     query.Result
	params     map[string]any
	inspection [32]byte
}

func (e *execution) compileOptions(ctx context.Context, params map[string]any) (query.CompileOpts, error) {
	filters, err := access.QueryRowFiltersWithContext(ctx, auth.UserFromContext(ctx), e.reg.Entities(), e.reg.Registers(), e.reg.InfoRegisters(), e.reg.AccountRegisters(), access.RuntimeContext(e.reg, e.db))
	if err != nil {
		return query.CompileOpts{}, err
	}
	return query.CompileOpts{Params: params, Entities: e.reg.Entities(), Registers: e.reg.Registers(), InfoRegs: e.reg.InfoRegisters(), AccountRegs: e.reg.AccountRegisters(), Dialect: e.db.Dialect(), RowFilters: filters}, nil
}
func (e *execution) guardSQLProof(ctx context.Context, res query.Result, opts query.CompileOpts, receipt *query.ReportLineage) (*guardedSQLProof, error) {
	if opts.Dialect == nil || e.db == nil || opts.Dialect.Name() != e.db.Dialect().Name() {
		return nil, fmt.Errorf("row source: SQL proof dialect/host mismatch")
	}
	if receipt == nil || !receipt.Matches(res, opts) {
		return nil, fmt.Errorf("row source: SQL proof does not match current inputs")
	}
	user := auth.UserFromContext(ctx)
	rep := e.reg.GetReport(e.report)
	if rep == nil || rep.External || rep.RowSource == nil {
		return nil, fmt.Errorf("row source: SQL proof report binding changed")
	}
	if user != nil && !user.Has("report", rep.Name, "run") {
		return nil, fmt.Errorf("row source: SQL proof report run denied")
	}
	for _, source := range res.Sources {
		if e.lookup(source.Kind, source.Name) == nil {
			return nil, fmt.Errorf("row source: unresolved proof source")
		}
	}
	if access.DeniedReadSource(user, res.Sources) != "" {
		return nil, fmt.Errorf("row source: SQL proof source read denied")
	}
	if access.ReportOutputQueryMaskPlan(user, res, e.lookup).Denied != "" {
		return nil, fmt.Errorf("row source: SQL proof mandatory policy denied")
	}
	outputs, uses := receipt.Outputs(), receipt.Uses()
	if len(outputs) != len(res.Projection.Columns) {
		return nil, fmt.Errorf("row source: partial SQL proof")
	}
	nodes := 0
	var visit func(query.ReportExpression, int) error
	visit = func(expr query.ReportExpression, depth int) error {
		nodes++
		if nodes > 8192 || depth > 64 {
			return fmt.Errorf("row source: SQL proof complexity budget")
		}
		if expr.Op == "parameter" {
			count := 0
			for name := range opts.Params {
				if strings.EqualFold(name, expr.Parameter) {
					count++
				}
			}
			if count != 1 {
				return fmt.Errorf("row source: SQL parameter has no unique host binding")
			}
			// Presence in Params establishes no fixed-literal kind. Runtime input/data/
			// control origins remain separate from this SQL receipt.
		}
		if len(expr.Origins) > 1 {
			return fmt.Errorf("row source: navigation proof requires separate host policy")
		}
		for _, origin := range expr.Origins {
			if err := e.guardDirectOrigin(user, res, origin); err != nil {
				return err
			}
		}
		for _, arg := range expr.Args {
			if err := visit(arg, depth+1); err != nil {
				return err
			}
		}
		return nil
	}
	for _, output := range outputs {
		if err := visit(output.Expression, 0); err != nil {
			return nil, err
		}
	}
	// Every Use is membership/control input for EVERY output, not merely the
	// projection mentioning the same name. No special COUNT or alias exemption.
	for _, use := range uses {
		if err := visit(use.Expression, 0); err != nil {
			return nil, err
		}
	}
	if !receipt.Matches(res, opts) {
		return nil, fmt.Errorf("row source: SQL proof changed during policy checks")
	}
	digest, err := provenance.TypedDigest(struct {
		Profile string
		Outputs []query.ReportLineageOutput
		Uses    []query.ReportLineageUse
	}{sqlProofProfile, outputs, uses})
	if err != nil {
		return nil, fmt.Errorf("row source: unsupported SQL proof inspection")
	}
	params := make(map[string]any, len(opts.Params))
	for name, value := range opts.Params {
		params[name] = value
	}
	return &guardedSQLProof{database: e.db, registry: e.reg, dialect: opts.Dialect.Name(), timezone: time.Local, receipt: receipt, result: res, params: params, inspection: digest}, nil
}
func sameProofSource(a, b query.SourceRef) bool {
	return a.Kind == b.Kind && strings.EqualFold(a.Name, b.Name) && strings.EqualFold(a.TablePart, b.TablePart)
}
func (e *execution) guardDirectOrigin(user *auth.User, res query.Result, origin query.ReportFieldOrigin) error {
	if origin.Occurrence < 0 || origin.Occurrence >= len(res.SourceOccurrences) || len(origin.Path) != 1 || !sameProofSource(origin.Source, res.SourceOccurrences[origin.Occurrence].SourceRef) {
		return fmt.Errorf("row source: unknown SQL source occurrence/path")
	}
	meta := e.lookup(origin.Source.Kind, origin.Source.Name)
	if meta == nil {
		return fmt.Errorf("row source: unknown SQL origin metadata")
	}
	if origin.Source.TablePart != "" {
		var selected *metadata.Entity
		for i := range meta.TableParts {
			if strings.EqualFold(meta.TableParts[i].Name, origin.Source.TablePart) {
				if selected != nil {
					return fmt.Errorf("row source: ambiguous SQL tablepart")
				}
				selected = metadata.TablePartQueryEntity(meta, &meta.TableParts[i], meta.Name)
			}
		}
		meta = selected
		if meta == nil {
			return fmt.Errorf("row source: unknown SQL tablepart")
		}
	}
	count := 0
	for _, field := range meta.Fields {
		if strings.EqualFold(field.Name, origin.Field) {
			count++
			if field.Type != origin.Type || !strings.EqualFold(origin.Path[0], field.Name) {
				return fmt.Errorf("row source: SQL origin type/path changed")
			}
		}
	}
	self := strings.EqualFold(origin.Field, "id") && (strings.EqualFold(origin.Path[0], "id") || strings.EqualFold(origin.Path[0], "Ссылка") || strings.EqualFold(origin.Path[0], "Ref"))
	if count > 1 || count == 0 && (!self || !metadata.IsReference(origin.Type) || !strings.EqualFold(metadata.RefName(origin.Type), origin.Source.Name)) {
		return fmt.Errorf("row source: SQL origin is not an existing canonical field")
	}
	if !resultfilter.ReportAllowed(user)(origin.Source, meta, origin.Field, self) {
		return fmt.Errorf("row source: SQL control/output field policy denied")
	}
	return nil
}
func (e *execution) revalidateSQLRead(ctx context.Context, sql string, args []any) error {
	stored := e.sqlProofs[sql]
	if stored == nil {
		return fmt.Errorf("row source: SQL read without private proof")
	}
	if e.db != stored.database || e.reg != stored.registry || time.Local != stored.timezone || e.db.Dialect().Name() != stored.dialect {
		return fmt.Errorf("row source: SQL proof host/context changed")
	}
	res := stored.result
	res.Args = append([]any(nil), args...)
	opts, err := e.compileOptions(ctx, stored.params)
	if err != nil {
		return err
	}
	current, err := e.guardSQLProof(ctx, res, opts, stored.receipt)
	if err != nil {
		return err
	}
	if current.inspection != stored.inspection {
		return fmt.Errorf("row source: SQL proof inspection changed")
	}
	if len(e.sqlReads) >= maxQueries {
		return fmt.Errorf("row source: SQL proof read budget")
	}
	e.sqlReads = append(e.sqlReads, stored)
	return nil
}
