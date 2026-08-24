package ui

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/ivantit66/onebase/internal/realtime"
)

func TestServerShutdownCancelsAndDrainsBackgroundJobs(t *testing.T) {
	s := &Server{
		hub:        realtime.NewHub(),
		exportJobs: newExportJobStore(time.Minute),
	}
	t.Cleanup(s.Close)
	jobDone, ok := s.beginBackgroundJob()
	if !ok {
		t.Fatal("background job rejected before shutdown")
	}
	jobCtx, releaseCtx := s.backgroundRequestContext(context.Background())
	cancelled := make(chan struct{})
	release := make(chan struct{})
	var releaseOnce sync.Once
	releaseWorker := func() { releaseOnce.Do(func() { close(release) }) }
	// Фоновую задачу надо отпустить и при провале теста: t.Cleanup(s.Close)
	// ждёт её без дедлайна, и падение превратилось бы в таймаут всего пакета
	// вместо внятного FAIL. Cleanup'ы идут LIFO, поэтому этот сработает раньше
	// зарегистрированного выше s.Close.
	t.Cleanup(releaseWorker)
	workerDone := make(chan struct{})
	go func() {
		// jobDone объявлен первым, а выполняется последним: Shutdown ждёт
		// именно его (backgroundWG.Wait), поэтому к возврату Shutdown канал
		// workerDone гарантированно закрыт. Обратный порядок оставлял гонку —
		// Shutdown мог вернуться раньше close(workerDone), и неблокирующая
		// проверка ниже краснела на исправном коде (флейк test-windows).
		defer jobDone()
		defer close(workerDone)
		defer releaseCtx()
		<-jobCtx.Done()
		close(cancelled)
		<-release
	}()

	// Задачу держим незавершённой, пока не убедимся, что Shutdown её ждёт:
	// иначе тест не отличал бы «дождался» от «вернулся сразу», а именно это
	// он и обязан проверять. Запас по дедлайну большой — ждём мы не наступления
	// таймаута, а того, что Shutdown НЕ вернулся раньше времени.
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	shutdownErr := make(chan error, 1)
	go func() { shutdownErr <- s.Shutdown(shutdownCtx) }()

	select {
	case <-cancelled:
	case err := <-shutdownErr:
		t.Fatalf("Shutdown returned before the background job observed cancellation: %v", err)
	}
	select {
	case err := <-shutdownErr:
		t.Fatalf("Shutdown returned while a background job was still running: %v", err)
	case <-time.After(50 * time.Millisecond):
	}
	releaseWorker()

	if err := <-shutdownErr; err != nil {
		t.Fatal(err)
	}
	select {
	case <-workerDone:
	default:
		t.Fatal("background worker was not drained")
	}
	if _, ok := s.beginBackgroundJob(); ok {
		t.Fatal("background job accepted after shutdown began")
	}
	select {
	case <-s.exportJobs.done:
	default:
		t.Fatal("export sweeper remained active after shutdown")
	}
}

func TestServerShutdownHonorsDeadlineForUncooperativeJob(t *testing.T) {
	s := &Server{
		hub:        realtime.NewHub(),
		exportJobs: newExportJobStore(time.Minute),
	}
	jobDone, ok := s.beginBackgroundJob()
	if !ok {
		t.Fatal("background job rejected before shutdown")
	}
	defer func() {
		jobDone()
		s.Close()
	}()

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	err := s.Shutdown(shutdownCtx)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("Shutdown error = %v", err)
	}
	jobDone()
}
