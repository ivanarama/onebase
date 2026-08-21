// Package app is the composition root for the HTTP application. It is the
// only package that knows both the API shell and the concrete UI frontend.
package app

import (
	"context"
	"errors"
	"net"
	"os"
	"sync"
	"time"

	"github.com/ivantit66/onebase/internal/api"
	"github.com/ivantit66/onebase/internal/auth"
	"github.com/ivantit66/onebase/internal/dsl/interpreter"
	"github.com/ivantit66/onebase/internal/metrics"
	"github.com/ivantit66/onebase/internal/runtime"
	"github.com/ivantit66/onebase/internal/scheduler"
	"github.com/ivantit66/onebase/internal/storage"
	"github.com/ivantit66/onebase/internal/ui"
)

type Config struct {
	Registry    *runtime.Registry
	Store       *storage.DB
	Interpreter *interpreter.Interpreter
	AuthRepo    *auth.Repo
	Host        string
	Port        int
	UI          ui.Config
	Scheduler   *scheduler.Scheduler
}

type Application struct {
	server        *api.Server
	httpRuntime   httpRuntime
	scheduler     schedulerRuntime
	queue         queueRuntime
	mu            sync.Mutex
	listener      net.Listener
	cancel        context.CancelFunc
	schedulerDone <-chan error
	queueDone     <-chan error
	serveStopped  chan struct{}
	serveErr      error
	running       bool
	beforeDrain   func()
	queueError    func(error)
	closeOnce     sync.Once
	closeErr      error
}

// Build assembles shared services, the concrete frontend and the API router.
// It deliberately does not open a listener or start background workers.
func Build(ctx context.Context, cfg Config) (*Application, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if cfg.Registry == nil || cfg.Store == nil || cfg.Interpreter == nil || cfg.AuthRepo == nil {
		return nil, errors.New("app build: registry, store, interpreter and auth repo are required")
	}

	debugToken := os.Getenv("ONEBASE_DEBUG_TOKEN")
	loginLimit := auth.NewLoginLimiter(5, time.Minute)
	cfg.UI.DebugToken = debugToken
	cfg.UI.LoginLimit = loginLimit
	if debugToken != "" {
		cfg.UI.Metrics = metrics.New()
	}

	frontend := ui.New(cfg.Registry, cfg.Store, cfg.Interpreter, cfg.AuthRepo, cfg.UI, cfg.Scheduler)
	if cfg.Scheduler != nil {
		// Scheduled jobs need the same DSL environment as interactive requests.
		cfg.Scheduler.SetVarsBuilder(frontend.BuildJobDSLVars)
	}
	server := api.New(cfg.Registry, cfg.Store, cfg.Interpreter, cfg.AuthRepo, cfg.Host, cfg.Port, api.Config{
		AppName:       cfg.UI.AppName,
		MaxFileSizeMB: cfg.UI.MaxFileSizeMB,
		AllowedTypes:  cfg.UI.AllowedTypes,
		Webhooks:      cfg.UI.Webhooks,
		LoginLimit:    loginLimit,
		Metrics:       cfg.UI.Metrics,
		DebugToken:    debugToken,
	}, frontend, cfg.Scheduler)
	return &Application{
		server: server, httpRuntime: server,
		scheduler: cfg.Scheduler, queue: cfg.UI.JobQueue,
		serveStopped: make(chan struct{}),
	}, nil
}

func (a *Application) Server() *api.Server {
	if a == nil {
		return nil
	}
	return a.server
}
