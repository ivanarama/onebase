package entityservice

import (
	"context"

	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/ivantit66/onebase/internal/storage"
)

// SaveObserver is notified at the durable boundary of successful same-entity
// mutations performed with the derived context. It is intentionally
// observational: a caller cannot use it to approve or reject persistence after
// commit.
type SaveObserver func(entity *metadata.Entity, result SaveResult)

type saveObserverContextKey struct{}

// ContextWithSaveObserver returns a context that observes successful entity
// saves made through it. Observers compose so a narrower lifecycle can add its
// own accounting without hiding an observer installed by its caller.
func ContextWithSaveObserver(ctx context.Context, observer SaveObserver) context.Context {
	if ctx == nil || observer == nil {
		return ctx
	}
	previous, _ := ctx.Value(saveObserverContextKey{}).(SaveObserver)
	if previous == nil {
		return context.WithValue(ctx, saveObserverContextKey{}, observer)
	}
	return context.WithValue(ctx, saveObserverContextKey{}, SaveObserver(func(entity *metadata.Entity, result SaveResult) {
		previous(entity, result)
		observer(entity, result)
	}))
}

func saveObserverFromContext(ctx context.Context) SaveObserver {
	if ctx == nil {
		return nil
	}
	observer, _ := ctx.Value(saveObserverContextKey{}).(SaveObserver)
	return observer
}

// NotifySaveObserver registers an exact successful mutation with observers in
// ctx. Paths which intentionally bypass Service.Save call this after obtaining
// the authoritative persisted version.
// Delivery follows the enclosing transaction: rollback suppresses it, while a
// nested savepoint waits for the outer commit.
func NotifySaveObserver(ctx context.Context, entity *metadata.Entity, result SaveResult) {
	observer := saveObserverFromContext(ctx)
	if observer == nil {
		return
	}
	notify := func() { observer(entity, result) }
	if !storage.DeferUntilTxCommit(ctx, notify) {
		notify()
	}
}
