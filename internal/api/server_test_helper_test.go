package api

import (
	"os"
	"time"

	"github.com/ivantit66/onebase/internal/auth"
	"github.com/ivantit66/onebase/internal/dsl/interpreter"
	"github.com/ivantit66/onebase/internal/metrics"
	"github.com/ivantit66/onebase/internal/runtime"
	"github.com/ivantit66/onebase/internal/scheduler"
	"github.com/ivantit66/onebase/internal/storage"
	"github.com/ivantit66/onebase/internal/ui"
)

func newTestServer(reg *runtime.Registry, store *storage.DB, interp *interpreter.Interpreter, authRepo *auth.Repo, host string, port int, uiCfg ui.Config, sched *scheduler.Scheduler) *Server {
	debugToken := os.Getenv("ONEBASE_DEBUG_TOKEN")
	loginLimit := auth.NewLoginLimiter(5, time.Minute)
	uiCfg.DebugToken = debugToken
	uiCfg.LoginLimit = loginLimit
	if debugToken != "" {
		uiCfg.Metrics = metrics.New()
	}
	frontend := ui.New(reg, store, interp, authRepo, uiCfg, sched)
	if sched != nil {
		sched.SetVarsBuilder(frontend.BuildJobDSLVars)
	}
	return New(reg, store, interp, authRepo, host, port, Config{
		AppName: uiCfg.AppName, MaxFileSizeMB: uiCfg.MaxFileSizeMB,
		AllowedTypes: uiCfg.AllowedTypes, Webhooks: uiCfg.Webhooks,
		LoginLimit: loginLimit, Metrics: uiCfg.Metrics, DebugToken: debugToken,
	}, frontend, sched)
}
