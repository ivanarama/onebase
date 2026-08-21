package app

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
)

type httpRuntime interface {
	Listen() (net.Listener, error)
	Serve(net.Listener) error
	Shutdown(context.Context) error
	ResyncWSIntakes()
}

type schedulerRuntime interface {
	RunReady(context.Context, chan<- struct{}) error
	BeginQuiesce()
}

type queueRuntime interface {
	Run(context.Context) error
}

// SetBeforeDrain registers cleanup (for example, a config watcher) that must
// run after new scheduled work is rejected and before workers are cancelled.
func (a *Application) SetBeforeDrain(fn func()) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.beforeDrain = fn
}

// SetQueueErrorHandler registers reporting for queue drain failures. Queue
// errors remain diagnostic and do not turn an otherwise clean shutdown into a
// failed server generation.
func (a *Application) SetQueueErrorHandler(fn func(error)) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.queueError = fn
}

// Run reserves the listener and starts the scheduler, queue and HTTP server.
// It returns once the scheduler is ready; Build therefore remains free of
// network and goroutine side effects.
func (a *Application) Run(ctx context.Context) error {
	if a == nil || a.httpRuntime == nil {
		return errors.New("app run: application is not built")
	}
	a.mu.Lock()
	if a.running {
		a.mu.Unlock()
		return errors.New("app run: application is already running")
	}
	listener, err := a.httpRuntime.Listen()
	if err != nil {
		a.mu.Unlock()
		return errors.Join(err, a.Close(context.Background()))
	}
	workerCtx, cancel := context.WithCancel(ctx)
	a.listener = listener
	a.cancel = cancel
	a.running = true
	a.mu.Unlock()

	var schedulerDone chan error
	ready := make(chan struct{})
	if a.scheduler != nil {
		schedulerDone = make(chan error, 1)
		go func() { schedulerDone <- a.scheduler.RunReady(workerCtx, ready) }()
	} else {
		close(ready)
	}
	var queueDone chan error
	if a.queue != nil {
		queueDone = make(chan error, 1)
		go func() { queueDone <- a.queue.Run(workerCtx) }()
	}
	a.mu.Lock()
	a.schedulerDone = schedulerDone
	a.queueDone = queueDone
	a.mu.Unlock()

	select {
	case <-ready:
	case startErr := <-schedulerDone:
		cancel()
		_ = listener.Close()
		if queueDone != nil {
			<-queueDone
		}
		a.mu.Lock()
		a.schedulerDone = nil
		a.queueDone = nil
		a.running = false
		a.mu.Unlock()
		cleanupErr := a.Close(context.Background())
		if startErr == nil {
			return errors.Join(errors.New("app run: scheduler stopped before readiness"), cleanupErr)
		}
		return errors.Join(fmt.Errorf("start scheduler: %w", startErr), cleanupErr)
	}

	a.httpRuntime.ResyncWSIntakes()
	go func() {
		serveErr := a.httpRuntime.Serve(listener)
		if errors.Is(serveErr, http.ErrServerClosed) {
			serveErr = nil
		}
		a.mu.Lock()
		a.serveErr = serveErr
		close(a.serveStopped)
		a.mu.Unlock()
	}()
	return nil
}

// Stopped closes if the HTTP serving loop exits unexpectedly or after Close.
func (a *Application) Stopped() <-chan struct{} {
	if a == nil {
		ch := make(chan struct{})
		close(ch)
		return ch
	}
	return a.serveStopped
}

// Close rejects new scheduled work, stops external producers, drains workers
// in parallel, and only then shuts down HTTP/frontend resources.
func (a *Application) Close(ctx context.Context) error {
	if a == nil {
		return nil
	}
	a.closeOnce.Do(func() { a.closeErr = a.close(ctx) })
	return a.closeErr
}

func (a *Application) close(ctx context.Context) error {
	if a.httpRuntime == nil {
		return nil
	}
	if ctx == nil {
		ctx = context.Background()
	}
	a.mu.Lock()
	beforeDrain := a.beforeDrain
	queueError := a.queueError
	cancel := a.cancel
	schedulerDone := a.schedulerDone
	queueDone := a.queueDone
	running := a.running
	listener := a.listener
	a.mu.Unlock()

	if a.scheduler != nil {
		a.scheduler.BeginQuiesce()
	}
	if beforeDrain != nil {
		beforeDrain()
	}
	if cancel != nil {
		cancel()
	}

	var schedulerErr error
	if schedulerDone != nil {
		select {
		case schedulerErr = <-schedulerDone:
		case <-ctx.Done():
			schedulerErr = ctx.Err()
		}
	}
	if queueDone != nil {
		select {
		case queueErr := <-queueDone:
			if queueErr != nil && queueError != nil {
				queueError(queueErr)
			}
		case <-ctx.Done():
		}
	}

	httpErr := a.httpRuntime.Shutdown(ctx)
	if listener != nil {
		_ = listener.Close()
	}
	var serveErr error
	if running {
		select {
		case <-a.serveStopped:
			a.mu.Lock()
			serveErr = a.serveErr
			a.mu.Unlock()
		case <-ctx.Done():
			serveErr = ctx.Err()
		}
	}
	return errors.Join(serveErr, httpErr, schedulerErr)
}
