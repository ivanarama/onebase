package api

import (
	"context"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/ivantit66/onebase/internal/auth"
	"github.com/ivantit66/onebase/internal/entityservice"
	"github.com/ivantit66/onebase/internal/incident"
	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/ivantit66/onebase/internal/metrics"
	"github.com/ivantit66/onebase/internal/printform"
	"github.com/ivantit66/onebase/internal/sheet"
	"github.com/ivantit66/onebase/internal/webhook"
)

// RouteMounter is the HTTP surface supplied by the frontend. The concrete UI
// package is assembled by internal/app and is intentionally unknown to API.
type RouteMounter interface {
	MountPWA(chi.Router)
	MountServices(chi.Router)
	MountExchange(chi.Router)
	MountStatic(chi.Router)
	Mount(chi.Router)
	MountDebug(chi.Router)
}

// PrintFormRenderer supplies the same masked data and declarative sheet used by UI.
// The caller must authorise object and row access before rendering.
type PrintFormRenderer interface {
	BuildDeclarativeSheet(*http.Request, *metadata.Entity, uuid.UUID, *printform.LayoutForm) (*sheet.Document, *printform.RenderContext, error)
}

// AppServices are shared application services implemented by the frontend.
type AppServices interface {
	PrintFormRenderer
	EntitySvc() *entityservice.Service
	Incidents() *incident.Store
	SSESubscriberCount() int
	ServiceCacheStats() (hits, misses, evictions uint64, bytes int64)
}

// FrontendLifecycle is the small lifecycle contract needed by the HTTP shell.
type FrontendLifecycle interface {
	BeginShutdown()
	Shutdown(context.Context) error
	InvalidateWidgetCache()
	InvalidateServiceCache()
	ResyncWSIntakes()
	PublishDevReload()
}

type Frontend interface {
	RouteMounter
	AppServices
	FrontendLifecycle
}

// Config contains values shared with the frontend but consumed by API while
// constructing authentication, debug and REST routes.
type Config struct {
	AppName       string
	MaxFileSizeMB int
	AllowedTypes  []string
	Webhooks      *webhook.Dispatcher
	LoginLimit    *auth.LoginLimiter
	Metrics       *metrics.Registry
	DebugToken    string
}
