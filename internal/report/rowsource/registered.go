package rowsource

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/ivantit66/onebase/internal/auth"
	"github.com/ivantit66/onebase/internal/runtime"
	"github.com/ivantit66/onebase/internal/storage"
)

// CodeLookup is a trusted host module loader. It receives a canonical registry
// report key, never a client file path. It must load that report's .rep.os source,
// respect ctx, and must never be supplied by a request/preset or injected into DSL.
type CodeLookup func(ctx context.Context, reportName string) (string, error)

// RunRegistered binds entry and extra columns to current trusted report metadata.
// initial is the host-selected variant query with already typed host parameters;
// this source-main model does not implement B30 per-variant query selection.
// No ordinary Query, HTTP, ready-TabDoc, preset or snapshot path is switched here.
func RunRegistered(ctx context.Context, db *storage.DB, reg *runtime.Registry, name string, initial Query, lookup CodeLookup) (*Result, error) {
	if db == nil || reg == nil || lookup == nil {
		return nil, fmt.Errorf("row source: нет доверенного loader источника")
	}
	ctx, cancel := context.WithTimeout(storage.ReadOnlyContext(ctx), 10*time.Second)
	defer cancel()
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	generation := reg.Generation()
	rep := reg.GetReport(name)
	if rep == nil || rep.RowSource == nil || rep.External {
		return nil, fmt.Errorf("row source: источник строк отчёта не настроен")
	}
	user := auth.UserFromContext(ctx)
	if user != nil && !user.Has("report", rep.Name, "run") {
		return nil, fmt.Errorf("row source: нет права запуска отчёта")
	}
	if errs := rep.RowSource.Errors(); len(errs) > 0 {
		return nil, fmt.Errorf("%s", strings.Join(errs, "; "))
	}
	// Copy before calling the host loader. A registry replacement during loading
	// must invalidate this exact definition rather than mix old entry/schema and
	// new RLS/metadata. Run checks this generation again, closing the handoff race.
	subset := cloneInputSubset(rep.RowSource.Inputs)
	src := Source{Report: rep.Name, Entry: rep.RowSource.Procedure}
	for _, c := range rep.RowSource.Columns {
		src.Extras = append(src.Extras, Extra{Name: c.Name, Type: c.Type})
	}
	code, err := lookup(ctx, rep.Name)
	if err != nil {
		return nil, fmt.Errorf("row source: модуль отчёта недоступен: %w", err)
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	src.Code = code
	return runWithInputSubset(ctx, db, reg, initial, src, &generation, subset)
}
