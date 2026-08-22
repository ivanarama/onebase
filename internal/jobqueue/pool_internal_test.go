package jobqueue

import (
	"context"
	"testing"

	"github.com/google/uuid"

	"github.com/ivantit66/onebase/internal/storage"
)

func TestPool_ПопыткиОднойЗадачиУчитываютсяРаздельно(t *testing.T) {
	id := uuid.New()
	oldLease := storage.JobTaskLease{ID: id, Worker: "worker/1", Attempt: 1, Token: "old-token"}
	newLease := storage.JobTaskLease{ID: id, Worker: "worker/1", Attempt: 2, Token: "new-token"}
	oldCtx, cancelOld := context.WithCancel(context.Background())
	newCtx, cancelNew := context.WithCancel(context.Background())
	defer cancelOld()
	defer cancelNew()
	p := &Pool{inflight: map[storage.JobTaskLease]*inflightTask{
		oldLease: {cancel: cancelOld, lease: oldLease},
		newLease: {cancel: cancelNew, lease: newLease},
	}}

	p.loseLocalLease(oldLease)
	select {
	case <-oldCtx.Done():
	default:
		t.Fatal("потерявшая аренду попытка не остановлена")
	}
	select {
	case <-newCtx.Done():
		t.Fatal("потеря старой аренды остановила новую попытку")
	default:
	}
	oldState := p.finishInflight(oldLease)
	if !oldState.leaseLost {
		t.Fatal("старая попытка не помечена как потерявшая аренду")
	}
	if p.InFlight() != 1 {
		t.Fatalf("завершение старой попытки сняло новую с учёта: active=%d", p.InFlight())
	}

	p.cancelLocal(newLease)
	select {
	case <-newCtx.Done():
	default:
		t.Fatal("текущая попытка не получила отмену")
	}
	newState := p.finishInflight(newLease)
	if !newState.cancelled {
		t.Fatal("текущая попытка не сохранила состояние отмены")
	}
}
