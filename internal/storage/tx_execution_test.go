package storage

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
)

type blockingPGBeginner struct{}

func (blockingPGBeginner) Begin(ctx context.Context) (pgx.Tx, error) {
	<-ctx.Done()
	return nil, ctx.Err()
}

func TestBeginPGTransactionForExecutionHonorsAcquireCancellation(t *testing.T) {
	acquireCtx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	started := time.Now()

	_, err := beginPGTransactionForExecution(blockingPGBeginner{}, acquireCtx)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("Begin error = %v, want deadline exceeded", err)
	}
	if elapsed := time.Since(started); elapsed > time.Second {
		t.Fatalf("BEGIN ignored acquire deadline: %v", elapsed)
	}
}
