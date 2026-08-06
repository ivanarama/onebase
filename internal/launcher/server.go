package launcher

import (
	"embed"
	"io/fs"
	"net"
	"net/http"
	"sync"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/ivantit66/onebase/internal/auth"
	"github.com/ivantit66/onebase/internal/i18n"
	"github.com/ivantit66/onebase/internal/webassets"
	"github.com/ivantit66/onebase/internal/websec"
)

//go:embed static
var staticFiles embed.FS

func init() {
	sub, _ := fs.Sub(staticFiles, "static")
	staticHTTP = http.FileServer(http.FS(sub))
}

var staticHTTP http.Handler

// noStore гасит кэширование embed-статики (configurator.js, Monaco, ECharts,
// SlickGrid). Эти байты живут в бинаре и обновляются только при пересборке, а
// embed.FS отдаёт стабильный Last-Modified — поэтому WebView2/браузер отвечают
// 304 Not Modified и бесконечно переиспользуют копию из предыдущей сборки
// (обычный F5 против этого бессилен). no-store заставляет клиента всегда
// тянуть свежие байты после обновления onebase-gui.exe.
func noStore(h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h.ServeHTTP(&noStoreResponseWriter{ResponseWriter: w}, r)
	})
}

// noStoreResponseWriter applies the policy at the moment headers are written.
// This matters for vendor handlers: they set their own immutable cache policy
// inside ServeHTTP and would otherwise overwrite a header set by middleware
// before the call.
type noStoreResponseWriter struct {
	http.ResponseWriter
}

func (w *noStoreResponseWriter) WriteHeader(statusCode int) {
	w.Header().Set("Cache-Control", "no-store")
	w.ResponseWriter.WriteHeader(statusCode)
}

func (w *noStoreResponseWriter) Write(p []byte) (int, error) {
	w.Header().Set("Cache-Control", "no-store")
	return w.ResponseWriter.Write(p)
}

// Server is the launcher HTTP server (list of registered bases).
type Server struct {
	h        *handler
	ln       net.Listener
	quit     chan struct{}
	quitOnce sync.Once
	httpSrv  *http.Server
}

// requestQuit просит лаунчер завершиться: закрывает канал Done, на котором
// висит окно. Через sync.Once, потому что сигналов теперь два — кнопка выхода
// и применение обновления, — а повторный close(chan) паникует.
func (s *Server) requestQuit() {
	s.quitOnce.Do(func() { close(s.quit) })
}

// NewServer creates a launcher server bound to a random available port.
func NewServer(store *Store, runner *Runner) (*Server, error) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, err
	}
	h := &handler{
		store: store, runner: runner, isoBrowser: systemBrowser{},
		cfgLoginLimit: auth.NewLoginLimiter(5, time.Minute),
	}
	if b, err := i18n.Load(i18n.EmbeddedLocales, ""); err == nil {
		launcherBundle = b
	}
	srv := &Server{h: h, ln: ln, quit: make(chan struct{})}
	h.quitFn = srv.requestQuit
	return srv, nil
}

// URL returns the base URL of the launcher server.
func (s *Server) URL() string { return "http://" + s.ln.Addr().String() }

// Done returns a channel that is closed when /quit is received.
func (s *Server) Done() <-chan struct{} { return s.quit }

// Close shuts down the HTTP server and closes auth pools. Запущенные базы
// НАМЕРЕННО переживают закрытие лаунчера (план 78): лаунчер — окно запуска,
// а не владелец сеансов, как в 1С — открытые окна Предприятия продолжают
// работать. Раньше здесь был StopAll («иначе дети-зомби»), но с усыновлением
// (handler.baseRunning) следующий экземпляр лаунчера видит живые базы,
// открывает их и умеет останавливать — зомби больше не проблема. Явная
// остановка всего — кнопка «Стоп всё» (killAll).
func (s *Server) Close() {
	CloseAuthPools()
	if s.httpSrv != nil {
		bestEffort("закрыть HTTP-сервер лаунчера", s.httpSrv.Close())
	}
}

func (s *Server) ListenAndServe() error {
	r := chi.NewRouter()
	r.Use(middleware.Recoverer)
	// Конфигуратор — чувствительная поверхность (консоль кода, миграции):
	// те же базовые защитные заголовки и Origin-проверка CSRF, что у базы
	// (план 53, этап 3). Модальные iframe конфигуратора — same-origin,
	// frame-ancestors 'self' + localhost их не ломает.
	r.Use(websec.SecurityHeaders)
	r.Use(websec.CSRFProtect)

	// Static assets (embedded). noStore — чтобы после пересборки бинаря клиент
	// не обслуживал прошивку embed-статики из кэша (см. комментарий у noStore).
	r.Handle("/static/*", noStore(http.StripPrefix("/static/", staticHTTP)))
	// Monaco editor (shared vendored tree) — отдельный путь, чтобы не
	// конфликтовать с catch-all /static/*. Конфигуратор и редактор форм
	// грузят его офлайн вместо CDN.
	r.Handle("/vendor/monaco/*", noStore(http.StripPrefix("/vendor/monaco/", webassets.MonacoHandler())))
	// ECharts (тот же вендоренный пакет, что и у базы) — предпросмотр виджета
	// в конфигураторе рисуется тем же графическим движком, что и рабочий стол.
	r.Handle("/vendor/echarts/*", noStore(http.StripPrefix("/vendor/echarts/", webassets.EChartsHandler())))
	// SlickGrid (6pac fork, MIT) — грид для редактируемых табличных частей в
	// managed-формах. Самохостинг вместо CDN: UI работает офлайн.
	r.Handle("/vendor/slickgrid/*", noStore(http.StripPrefix("/vendor/slickgrid/", webassets.SlickGridHandler())))

	// Launcher pages (no auth)
	r.Get("/", s.h.index)
	r.Get("/browse-dir", s.h.browseDir)
	r.Get("/browse-file", s.h.browseFile)
	r.Get("/bases/new", s.h.newForm)
	r.Post("/bases", s.h.create)
	r.Get("/bases/{id}/edit", s.h.editForm)
	r.Post("/bases/{id}", s.h.update)
	r.Post("/bases/{id}/delete", s.h.delete)
	r.Post("/bases/{id}/move", s.h.move)
	r.Post("/bases/{id}/start", s.h.start)
	r.Post("/bases/{id}/start-native", s.h.startNative)
	r.Post("/bases/{id}/start-isolated", s.h.startIsolated)
	r.Post("/bases/{id}/profiles/clean", s.h.cleanProfiles)
	r.Post("/bases/{id}/stop", s.h.stop)
	r.Post("/bases/{id}/config/export", s.h.configExport)
	r.Post("/bases/{id}/config/import", s.h.configImport)

	// Configurator login/logout (no auth)
	r.Get("/bases/{id}/configurator/login", s.h.cfgLoginPage)
	r.Post("/bases/{id}/configurator/login", s.h.cfgLoginSubmit)
	r.Get("/bases/{id}/configurator/logout", s.h.cfgLogout)
	r.Get("/bases/{id}/configurator/logo", s.h.configuratorLogo)

	// Configurator routes (auth required — admin only)
	r.Group(func(r chi.Router) {
		r.Use(s.h.cfgAuthMiddleware)
		r.Get("/bases/{id}/configurator", s.h.configuratorPage)
		r.Post("/bases/{id}/configurator/convert", s.h.configuratorConvert)
		r.Post("/bases/{id}/configurator/module", s.h.configuratorSaveModule)
		r.Post("/bases/{id}/configurator/fields", s.h.configuratorSaveFields)
		r.Post("/bases/{id}/configurator/entity-delete", s.h.configuratorDeleteEntity)
		r.Post("/bases/{id}/configurator/form", s.h.configuratorSaveForm)
		r.Post("/bases/{id}/configurator/register-fields", s.h.configuratorSaveRegisterFields)
		r.Post("/bases/{id}/configurator/inforeg-fields", s.h.configuratorSaveInfoRegFields)
		r.Post("/bases/{id}/configurator/account-register", s.h.configuratorSaveAccountRegister)
		r.Post("/bases/{id}/configurator/predefined", s.h.configuratorSavePredefined)
		r.Post("/bases/{id}/configurator/enum", s.h.configuratorSaveEnum)
		r.Post("/bases/{id}/configurator/constant", s.h.configuratorSaveConstant)
		r.Post("/bases/{id}/configurator/report", s.h.configuratorSaveReport)
		r.Post("/bases/{id}/configurator/common-module", s.h.configuratorSaveCommonModule)
		r.Post("/bases/{id}/configurator/processor", s.h.configuratorSaveProcessor)
		r.Post("/bases/{id}/configurator/new", s.h.configuratorNewObject)
		r.Post("/bases/{id}/configurator/printform", s.h.configuratorSavePrintForm)
		r.Post("/bases/{id}/configurator/layout", s.h.configuratorSaveLayout)
		r.Post("/bases/{id}/configurator/new-layout", s.h.configuratorNewLayout)
		r.Post("/bases/{id}/configurator/layout/preview", s.h.configuratorLayoutPreview)
		r.Post("/bases/{id}/configurator/layout/import-pdf", s.h.configuratorImportPDFLayout)
		r.Post("/bases/{id}/configurator/new-printform", s.h.configuratorNewPrintForm)
		// Управляемые формы (план 37, этап 4).
		r.Get("/bases/{id}/configurator/forms", s.h.configuratorFormsList)
		r.Get("/bases/{id}/configurator/forms/edit", s.h.configuratorFormsEdit)
		r.Post("/bases/{id}/configurator/forms/save", s.h.configuratorFormsSave)
		r.Post("/bases/{id}/configurator/forms/delete", s.h.configuratorFormsDelete)
		r.Post("/bases/{id}/configurator/forms/validate", s.h.configuratorFormsValidate)
		r.Post("/bases/{id}/configurator/forms/preview", s.h.configuratorFormsPreview)
		r.Post("/bases/{id}/configurator/forms/edit-op", s.h.configuratorFormsEditOp) // визуальный конструктор (#164)
		r.Post("/bases/{id}/configurator/forms/import-1c", s.h.configuratorFormsImport1C)
		r.Get("/bases/{id}/configurator/file", s.h.configuratorFileRaw) // raw-просмотр файла, issue #132
		r.Post("/bases/{id}/configurator/app", s.h.configuratorSaveApp)
		r.Post("/bases/{id}/configurator/subsystem", s.h.configuratorSaveSubsystem)
		r.Post("/bases/{id}/configurator/widget", s.h.configuratorSaveWidget)
		r.Post("/bases/{id}/configurator/widget-delete", s.h.configuratorDeleteWidget)
		r.Post("/bases/{id}/configurator/widget-preview", s.h.configuratorWidgetPreview)
		r.Post("/bases/{id}/configurator/page", s.h.configuratorSavePage)
		r.Post("/bases/{id}/configurator/page-delete", s.h.configuratorDeletePage)
		r.Post("/bases/{id}/configurator/journal", s.h.configuratorSaveJournal)
		r.Post("/bases/{id}/configurator/journal-delete", s.h.configuratorDeleteJournal)
		r.Post("/bases/{id}/configurator/home-page", s.h.configuratorSaveHomePage)
		r.Post("/bases/{id}/configurator/home-page-yaml", s.h.configuratorSaveHomePageYAML)
		r.Post("/bases/{id}/configurator/check", s.h.configuratorCheck)
		r.Post("/bases/{id}/configurator/check-all", s.h.configuratorCheckAll)
		r.Post("/bases/{id}/configurator/migrate", s.h.configuratorMigrate)
		r.Get("/bases/{id}/configurator/launch-state", s.h.configuratorLaunchState)
		r.Post("/bases/{id}/configurator/restart", s.h.configuratorRestart)
		r.Post("/bases/{id}/configurator/reorder", s.h.configuratorReorder)
		r.Get("/bases/{id}/configurator/config/export-zip", s.h.configExportZip)
		r.Post("/bases/{id}/configurator/config/import-zip", s.h.configImportZip)
		r.Get("/bases/{id}/configurator/admin/users", s.h.cfgAdminUsers)
		r.Post("/bases/{id}/configurator/admin/users/create", s.h.cfgAdminUserCreate)
		r.Post("/bases/{id}/configurator/admin/users/delete", s.h.cfgAdminUserDelete)
		r.Post("/bases/{id}/configurator/admin/users/passwd", s.h.cfgAdminUserPasswd)
		r.Post("/bases/{id}/configurator/admin/users/deny-passwd", s.h.cfgAdminUserDenyPasswd)
		r.Post("/bases/{id}/configurator/admin/users/show-in-list", s.h.cfgAdminUserShowInList)
		r.Post("/bases/{id}/configurator/admin/users/ai-data", s.h.cfgAdminUserAIData)
		r.Post("/bases/{id}/configurator/admin/users/lang", s.h.cfgAdminUserLang)
		r.Get("/bases/{id}/configurator/admin/sessions", s.h.cfgAdminSessions)
		r.Post("/bases/{id}/configurator/admin/sessions/kick", s.h.cfgAdminSessionKick)
		r.Post("/bases/{id}/configurator/admin/sessions/limit", s.h.cfgAdminSessionLimit)
		r.Get("/bases/{id}/configurator/admin/audit", s.h.cfgAdminAudit)
		r.Post("/bases/{id}/configurator/admin/audit/save", s.h.cfgAdminAuditSave)
		r.Get("/bases/{id}/configurator/admin/settings", s.h.cfgAdminSettings)
		r.Post("/bases/{id}/configurator/admin/settings/save", s.h.cfgAdminSettingsSave)
		r.Get("/bases/{id}/configurator/admin/config-history", s.h.cfgAdminConfigHistory)
		r.Post("/bases/{id}/configurator/admin/config-history/rollback", s.h.cfgAdminConfigHistoryRollback)
		r.Get("/bases/{id}/configurator/admin/config-history/{version}/export-zip", s.h.cfgAdminConfigHistoryExportZip)
		r.Get("/bases/{id}/configurator/admin/config-history/{version}/export-obz", s.h.cfgAdminConfigHistoryExportOBZ)
		r.Get("/bases/{id}/configurator/admin/rollup", s.h.cfgAdminRollup)
		r.Post("/bases/{id}/configurator/admin/rollup/preview", s.h.cfgAdminRollupPreview)
		r.Post("/bases/{id}/configurator/admin/rollup/run", s.h.cfgAdminRollupRun)
		r.Get("/bases/{id}/configurator/admin/ai", s.h.cfgAdminAI)
		r.Get("/bases/{id}/configurator/admin/ai-history", s.h.cfgAdminAIHistory)
		r.Post("/bases/{id}/configurator/admin/ai/save", s.h.cfgAdminAISave)
		r.Post("/bases/{id}/configurator/admin/ai/datascope", s.h.cfgAdminAIDataScope)
		r.Post("/bases/{id}/configurator/admin/ai/budget", s.h.cfgAdminAIBudgetSave)
		r.Post("/bases/{id}/configurator/admin/ai/test", s.h.cfgAdminAITest)
		r.Get("/bases/{id}/configurator/ai-enabled", s.h.cfgAIEnabled)
		r.Get("/bases/{id}/configurator/langref", s.h.configuratorLangref)
		r.Post("/bases/{id}/configurator/ai-assist", s.h.cfgAIAssist)
		r.Post("/bases/{id}/configurator/ai-explain", s.h.cfgAIExplain)
		r.Post("/bases/{id}/configurator/ai-query", s.h.cfgAIQuery)
		r.Post("/bases/{id}/configurator/ai-generate", s.h.cfgAIGenerate)
		r.Post("/bases/{id}/configurator/ai-apply", s.h.cfgAIApply)
		r.Get("/bases/{id}/configurator/admin/about", s.h.cfgAdminAbout)
		r.Get("/bases/{id}/configurator/admin/roles", s.h.cfgAdminRoles)
		r.Post("/bases/{id}/configurator/admin/roles/save", s.h.cfgAdminRoleSave)
		r.Post("/bases/{id}/configurator/admin/roles/delete", s.h.cfgAdminRoleDelete)
		r.Get("/bases/{id}/configurator/admin/users/roles", s.h.cfgAdminUserRoles)
		r.Post("/bases/{id}/configurator/admin/users/roles/save", s.h.cfgAdminUserRolesSave)
		r.Post("/bases/{id}/configurator/backup/create", s.h.backupCreate)
		r.Get("/bases/{id}/configurator/backup/{file}/download", s.h.backupDownload)
		r.Post("/bases/{id}/configurator/backup/{file}/delete", s.h.backupDelete)
		r.Post("/bases/{id}/configurator/backup/settings", s.h.backupSettings)
		r.Post("/bases/{id}/configurator/backup/upload", s.h.backupUpload)
		r.Post("/bases/{id}/configurator/backup/{file}/restore", s.h.backupRestore)
		r.Get("/bases/{id}/configurator/backup/full-export", s.h.backupFullExport)
		r.Post("/bases/{id}/configurator/backup/full-import", s.h.backupFullImport)
	})

	// Debug proxy — вне cfgAuth-группы намеренно: хендлер сам проверяет
	// сессию админа и отвечает 401 JSON (а не 302→HTML, который ломал JS-fetch).
	// На app-стороне debug-запрос дополнительно требует X-OneBase-Debug-Token.
	r.HandleFunc("/bases/{id}/debug/{action}", s.h.debugProxy) // GET + POST

	// Одноразовый bootstrap-код (план 53) — тоже вне cfgAuth-группы: хендлер
	// сам проверяет сессию админа и отвечает JSON для JS-fetch.
	r.Post("/bases/{id}/one-time-code", s.h.oneTimeCodeProxy)

	r.Post("/killall", s.h.killAll)
	r.Post("/quit", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		s.requestQuit()
	})

	// Обновление платформы (план 92). Маршруты локальные — лаунчер слушает
	// только 127.0.0.1, — но сами хендлеры ещё раз сверяются с политикой и
	// правами на каталог бинаря: на общей установке кнопки быть не должно.
	r.Get("/updates", s.h.updatesPage)
	r.Post("/updates/check", s.h.updatesCheck)
	r.Post("/updates/download", s.h.updatesDownload)
	r.Post("/updates/apply", s.h.updatesApply)
	r.Post("/updates/rollback", s.h.updatesRollback)
	r.Post("/updates/channel", s.h.updatesChannel)

	// Тихая фоновая проверка обновлений платформы (план 92): результат
	// складывается в ~/.onebase/updates/state.json, откуда его читают шапка
	// лаунчера и «О программе».
	s.h.startUpdateWatcher()

	// Slowloris-защита (см. api/server.go): только ReadHeaderTimeout + IdleTimeout.
	// WriteTimeout не ставим — launcher проксирует SSE-события отладчика.
	s.httpSrv = &http.Server{
		Handler:           r,
		ReadHeaderTimeout: 15 * time.Second,
		IdleTimeout:       120 * time.Second,
	}
	return s.httpSrv.Serve(s.ln)
}
