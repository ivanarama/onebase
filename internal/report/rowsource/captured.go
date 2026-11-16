package rowsource

import (
	"context"
	"time"

	"github.com/ivantit66/onebase/internal/runtime"
	"github.com/ivantit66/onebase/internal/storage"
)

// RunCapturedInputs runs only the canonical module/query/typed parameters bound
// by CaptureInputFrame, with the current host actor/metadata/policy checked both
// before and after execution. Source-main variants share the report query; B30
// per-variant query adaptation remains separate. No client query/code/schema is
// accepted here. This is NOT a successful producer seal or final filter schema:
// Result stays ordinary mutable data with no output/snapshot authority.
func RunCapturedInputs(ctx context.Context, db *storage.DB, reg *runtime.Registry, frame *InputFrame) (*Result, error) {
	ctx, cancel := context.WithTimeout(storage.ReadOnlyContext(ctx), 10*time.Second)
	defer cancel()
	selected, err := frame.revalidatedExecution(ctx, db, reg)
	if err != nil {
		return nil, err
	}
	result, err := runWithInputSubset(ctx, db, reg, selected.initial, selected.source, &selected.generation, selected.subset)
	if err != nil {
		return nil, err
	}
	if err = frame.Revalidate(ctx, db, reg); err != nil {
		return nil, err
	}
	return result, nil
}
