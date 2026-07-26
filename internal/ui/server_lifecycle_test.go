package ui

import (
	"context"
	"errors"
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
	workerDone := make(chan struct{})
	go func() {
		defer close(workerDone)
		defer jobDone()
		defer releaseCtx()
		<-jobCtx.Done()
	}()

	shutdownCtx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := s.Shutdown(shutdownCtx); err != nil {
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
