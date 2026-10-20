package ui

import (
	"context"

	"github.com/google/uuid"
	"github.com/ivantit66/onebase/internal/entityservice"
	"github.com/ivantit66/onebase/internal/runtime"
)

type hookXDTOOriginKey struct{}

type hookXDTOOrigin struct {
	entity string
	id     uuid.UUID
	state  *entityHookXDTOState
}

func hookXDTOStateFromContext(ctx context.Context, obj *runtime.Object) *entityHookXDTOState {
	if origin, ok := ctx.Value(hookXDTOOriginKey{}).(hookXDTOOrigin); ok && origin.entity == obj.Type && origin.id == obj.ID {
		return origin.state
	}
	isNew, assigned := entityservice.HookObjectState(ctx, obj)
	if assigned == nil {
		assigned = map[string]bool{}
	}
	return &entityHookXDTOState{loaded: !isNew, assigned: assigned}
}
