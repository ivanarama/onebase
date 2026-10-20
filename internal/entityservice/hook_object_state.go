package entityservice

import (
	"context"

	"github.com/ivantit66/onebase/internal/runtime"
)

type hookObjectStateKey struct{}

type hookObjectState struct {
	object   *runtime.Object
	isNew    bool
	assigned map[string]bool
}

func withHookObjectState(ctx context.Context, obj *runtime.Object, isNew bool) context.Context {
	return context.WithValue(ctx, hookObjectStateKey{}, &hookObjectState{
		object: obj, isNew: isNew, assigned: map[string]bool{},
	})
}

// HookObjectState supplies MakeThis with the origin of this exact lifecycle
// object and a shared assignment set for its successive hooks. Unknown objects
// are conservatively treated as stored; nested writes cannot inherit another
// object's origin merely by reusing its context. Field names are lower case.
func HookObjectState(ctx context.Context, obj *runtime.Object) (isNew bool, assigned map[string]bool) {
	if state, ok := ctx.Value(hookObjectStateKey{}).(*hookObjectState); ok && state.object == obj {
		return state.isNew, state.assigned
	}
	return false, nil
}
