package ui

import (
	"html/template"
	"net/http"
	"sort"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/ivantit66/onebase/internal/auth"
	"github.com/ivantit66/onebase/internal/debugger"
	"github.com/ivantit66/onebase/internal/dsl/interpreter"
	"github.com/ivantit66/onebase/internal/entityservice"
	"github.com/ivantit66/onebase/internal/extform"
	"github.com/ivantit66/onebase/internal/i18n"
	"github.com/ivantit66/onebase/internal/i18n/i18nerr"
	"github.com/ivantit66/onebase/internal/mailer"
	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/ivantit66/onebase/internal/realtime"
	"github.com/ivantit66/onebase/internal/runtime"
	"github.com/ivantit66/onebase/internal/scheduler"
	"github.com/ivantit66/onebase/internal/storage"
	"github.com/ivantit66/onebase/internal/webhook"
	"github.com/ivantit66/onebase/internal/widget"
	"time"
)

// Config holds static info shown in «О программе».
type Config struct {
	AppName       string
	AppVersion    string
	AppAuthor     string // автор конфигурации (app.yaml: author)
	AppCopyright  string // правообладатель конфигурации (app.yaml: copyright)
	AppLicense    string // лицензия конфигурации (app.yaml: license)
	PlatAuthor    string // правообладатель платформы (version.Author)
	PlatLicense   string // лицензия платформы (version.License)
	DSN           string
	PlatVersion   string
	Logo          string // path to logo file (png/svg/jpg)
	Mailer        *mailer.Mailer
	MaxFileSizeMB int // 0 = use default 50
	DemoMode      bool
	DemoMessage   string
	Lang          string       // base language from config
	Bundle        *i18n.Bundle // translations
	// DebugToken — общий секрет для debug API. Пустой → debug-маршруты не
	// монтируются (см. api.New). Непустой → каждый запрос к /debug/global/*
	// должен нести его в заголовке X-OneBase-Debug-Token.
	DebugToken string
	// Webhooks — диспетчер исходящих веб-хуков из app.yaml (план 29).
	// nil = не настроены. Прокидывается в entityservice (save/post) и
	// используется обработчиками unpost/delete.
	Webhooks *webhook.Dispatcher
	// LoginLimit — общий лимитер попыток входа (тот же, что у формы /login).
	// Используется basic-auth HTTP-сервисов для защиты от брутфорса. nil →
	// New создаёт собственный, чтобы поле никогда не было пустым.
	LoginLimit *auth.LoginLimiter
}

type Server struct {
	reg              *runtime.Registry
	store            *storage.DB
	interp           *interpreter.Interpreter
	authRepo         *auth.Repo
	cfg              Config
	sched            *scheduler.Scheduler
	mailer           *mailer.Mailer
	maxFileSizeBytes int64
	globalDebug      *debugger.GlobalDebugController
	messages         *MessageStore
	widgetCache      *widget.Cache
	lockMgr          *runtime.LockManager   // #2 managed locks
	entitySvc        *entityservice.Service // упсёрт + ТЧ + движения + проведение, разделяется с api
	aiChatLimit      *aiWindowLimiter       // лимит частоты ИИ-чата на пользователя (план 54)
	endpointLimit    endpointLimiter        // rate-limit HTTP-сервисов (план 61)
	loginLimit       *auth.LoginLimiter     // брутфорс-защита basic-auth сервисов (общий с формой входа)
	extforms         *extform.Repo          // внешний контур: печатные формы из БД
	extreports       *extform.ReportRepo    // внешний контур: отчёты из БД
	extprocessors    *extform.ProcessorRepo // внешний контур: обработки из БД
	tmpl             *template.Template
	hub              *realtime.Hub // real-time-шина уведомлений сервер→браузер (план 74)
}

func New(reg *runtime.Registry, store *storage.DB, interp *interpreter.Interpreter, authRepo *auth.Repo, cfg Config, sched *scheduler.Scheduler) *Server {
	maxBytes := int64(cfg.MaxFileSizeMB) * 1024 * 1024
	if maxBytes <= 0 {
		maxBytes = 50 * 1024 * 1024
	}
	loginLimit := cfg.LoginLimit
	if loginLimit == nil {
		loginLimit = auth.NewLoginLimiter(5, time.Minute)
	}
	s := &Server{reg: reg, store: store, interp: interp, authRepo: authRepo, cfg: cfg, sched: sched, mailer: cfg.Mailer, maxFileSizeBytes: maxBytes, globalDebug: debugger.NewGlobalDebugController(), messages: NewMessageStore(), widgetCache: widget.NewCache(60 * time.Second), lockMgr: runtime.NewLockManager(), aiChatLimit: newAIWindowLimiter(10, time.Minute), loginLimit: loginLimit, extforms: extform.New(store), extreports: extform.NewReports(store), extprocessors: extform.NewProcessors(store), tmpl: template.Must(newTemplate(cfg.Bundle)), hub: realtime.NewHub()}
	s.entitySvc = &entityservice.Service{
		Store:  store,
		Reg:    reg,
		Interp: interp,
		// PrepareHook/EnrichTPRows зовут уже существующие методы Server'а —
		// enrichHeaderRefs (замена UUID → *Ref в шапке) и enrichTPRowsWithRefs
		// (то же для строк ТЧ).
		PrepareHook:  s.enrichHeaderRefs,
		EnrichTPRows: s.enrichTPRowsWithRefs,
		// BuildVars — полный набор с locks/users/документами + Сообщить с
		// захватом и в message store, и в локальный slice msgs (для SaveResult).
		BuildVars: s.buildDSLVarsWithMessages,
		// MakeThis — обёртка над *runtime.Object с поддержкой методов ТЧ
		// (this.Товары.Добавить() и т.п.). Без неё ОбработкаЗаполнения не
		// смогла бы построить строки табличной части в приёмнике.
		MakeThis: func(obj *runtime.Object, e *metadata.Entity) interpreter.This {
			return &formObjectThis{obj: obj, entity: e}
		},
		// Исходящие веб-хуки (план 29): save/post диспетчеризуются из Save.
		Hooks: cfg.Webhooks,
	}
	// Отладчик подключается к исполнению через DebugSource: каждый запуск DSL
	// захватывает текущую сессию глобального контроллера в свой execCtx.
	// Устанавливается однократно здесь, до начала обслуживания HTTP, — сам
	// Interpreter после этого неизменяем (план 52: раньше debug_handlers
	// мутировали interp.DebugHook на лету, что гонило с конкурентными запусками).
	interp.DebugSource = func() interpreter.DebugHook {
		if sess := s.globalDebug.Session(); sess != nil {
			return sess
		}
		return nil
	}
	if sched != nil {
		sched.SetMessageSink(func(userID, text string) { s.messages.Push(userID, text) })
	}
	return s
}

// Messages returns the per-user message store (used to inject Сообщить sink).
func (s *Server) Messages() *MessageStore { return s.messages }

// EntitySvc возвращает разделяемый сервис сохранения сущностей. Используется
// REST API (internal/api) чтобы вызывать OnWrite/OnPost + ТЧ + движения +
// проведение по той же логике, что и UI submit/submitEdit — иначе бизнес-
// правила работали бы только через web-форму.
func (s *Server) EntitySvc() *entityservice.Service { return s.entitySvc }

// InvalidateWidgetCache drops every cached widget result. The dev/reload path
// calls this so users see fresh data after metadata changes.
func (s *Server) InvalidateWidgetCache() { s.widgetCache.Invalidate() }

// GlobalDebug returns the global debug controller for the server.
func (s *Server) GlobalDebug() *debugger.GlobalDebugController { return s.globalDebug }

// tr translates a key using the resolved language. Falls back to the key itself.
func (s *Server) tr(lang, key string) string {
	if s.cfg.Bundle != nil {
		return s.cfg.Bundle.T(lang, key)
	}
	return key
}

func (s *Server) Mount(r chi.Router) {
	mountStatic(r)
	r.Get("/ui", s.index)
	r.Get("/ui/", s.index)
	r.Get("/ui/app", s.appShell)           // оболочка вкладок (issue #129/#130, фаза 1)
	r.Post("/ui/form-mode", s.setFormMode) // переключение режима открытия форм

	// Gengen — ДО catch-all роутов! (иначе /ui/dev/gengen матчится как {kind}/{entity})
	r.Get("/ui/dev/gengen", s.gengenPage)
	r.Post("/ui/dev/gengen/analyze", s.gengenAnalyze)
	r.Post("/ui/dev/gengen/generate", s.gengenGenerate)
	r.Post("/ui/dev/gengen/merge", s.gengenMerge)

	// Страницы (план 66) — ДО catch-all {kind}/{entity}: статический сегмент
	// «page» имеет приоритет над параметром, но регистрируем рядом с gengen,
	// чтобы намерение было явным.
	r.Get("/ui/page/{name}", s.page)
	// Кнопка-действие (план 66): POST вызывает процедуру-действие из .page.os и
	// PRG-редиректом возвращает на саму страницу. Статический «page» в приоритете
	// над catch-all {kind}/{entity}.
	r.Post("/ui/page/{name}/action/{action}", s.pageAction)

	// SSE-поток уведомлений сервер→браузер (план 74). Регистрируем ДО catch-all
	// {kind}/{entity}, чтобы «events» не матчился как вид объекта.
	r.Get("/ui/events", s.eventsStream)

	r.Get("/ui/{kind}/{entity}", s.list)
	r.Get("/ui/{kind}/{entity}/new", s.form)
	r.Post("/ui/{kind}/{entity}/new", s.submit)
	// Inline-создание элемента справочника из ссылочного поля документа
	// (как в 1С: «+» рядом с полем выбора). JS-клиент не знает kind целевой
	// сущности — этот маршрут резолвит kind по имени и редиректит на форму
	// создания в popup-режиме.
	r.Get("/ui/_ref-create/{entity}", s.refCreateRedirect)
	// Открытие карточки элемента справочника из picker'а (иконка-лупа):
	// JS знает только имя сущности и id, kind резолвим на сервере.
	r.Get("/ui/_ref-open/{entity}/{id}", s.refOpenRedirect)
	r.Get("/ui/{kind}/{entity}/{id}", s.formEdit)
	r.Post("/ui/{kind}/{entity}/{id}", s.submitEdit)
	// Рантайм событий управляемых форм (план 37, этап 8): обработчики
	// кнопок (Нажатие) и полей (ПриИзменении). Возвращает JSON с
	// обновлёнными values и сообщениями от Сообщить().
	r.Post("/ui/{kind}/{entity}/form-event", s.handleManagedFormEvent)
	r.Get("/ui/register/{name}", s.registerMovements)
	r.Get("/ui/register/{name}/balances", s.registerBalances)
	r.Get("/ui/inforeg/{name}", s.infoRegList)
	r.Get("/ui/inforeg/{name}/new", s.infoRegForm)
	r.Post("/ui/inforeg/{name}/new", s.infoRegSubmit)
	r.Post("/ui/inforeg/{name}/delete", s.infoRegDelete)
	r.Get("/ui/report/{name}", s.reportForm)
	r.Post("/ui/report/{name}", s.reportRun)
	r.Post("/ui/report/{name}/settings/save", s.reportSettingsSave)
	r.Post("/ui/report/{name}/settings/reset", s.reportSettingsReset)
	r.Get("/ui/processor/{name}", s.processorForm)
	r.Post("/ui/processor/{name}", s.processorRun)
	r.Post("/ui/processor/{name}/form-event", s.handleProcessorFormEvent)

	// Document posting
	r.Post("/ui/{kind}/{entity}/{id}/post", s.postDocument)
	r.Post("/ui/{kind}/{entity}/{id}/unpost", s.unpostDocument)

	// Delete record / mark for deletion
	r.Post("/ui/{kind}/{entity}/{id}/activity", s.setRecordActivity)
	r.Post("/ui/{kind}/{entity}/{id}/delete", s.deleteRecord)
	r.Post("/ui/{kind}/{entity}/delete-marked", s.deleteMarked)

	// Global delete-marked page
	r.Get("/ui/delete-marked", s.deleteMarkedAll)
	r.Post("/ui/delete-marked", s.deleteMarkedAll)

	// Admin: user management
	r.Get("/ui/admin/users", s.adminUsers)
	r.Get("/ui/admin/users/new", s.adminUserNew)
	r.Post("/ui/admin/users/new", s.adminUserCreate)
	r.Get("/ui/admin/users/{id}", s.adminUserCard)
	r.Post("/ui/admin/users/{id}", s.adminUserCard)
	r.Post("/ui/admin/users/{id}/delete", s.adminUserDelete)
	r.Get("/ui/admin/users/{id}/passwd", s.adminUserPasswd)
	r.Post("/ui/admin/users/{id}/passwd", s.adminUserPasswd)
	r.Post("/ui/admin/users/{id}/deny-passwd", s.adminUserDenyPasswd)

	// Self-service: change own password
	r.Get("/ui/profile/passwd", s.selfPasswd)
	r.Post("/ui/profile/passwd", s.selfPasswd)
	// Self-service: change language
	r.Post("/ui/profile/lang", s.setLang)

	// Admin: active sessions
	r.Get("/ui/admin/sessions", s.adminSessions)
	r.Post("/ui/admin/sessions/{login}/kick", s.adminKickUser)

	// Admin: roles
	r.Get("/ui/admin/roles", s.adminRoles)
	r.Get("/ui/admin/users/{id}/roles", s.adminUserRoles)
	r.Post("/ui/admin/users/{id}/roles", s.adminUserRolesUpdate)

	// Admin: audit log
	r.Get("/ui/admin/audit", s.adminAudit)
	r.Get("/ui/admin/webhooks", s.adminWebhooks)
	r.Get("/ui/{kind}/{entity}/{id}/history", s.recordHistory)

	// Admin: orphan movements cleanup
	r.Get("/ui/admin/cleanup", s.adminCleanup)
	r.Post("/ui/admin/cleanup", s.adminCleanup)

	// Admin: external print forms (внешний контур расширяемости)
	r.Get("/ui/admin/extforms", s.adminExtForms)
	r.Post("/ui/admin/extforms", s.adminExtFormUpload)
	r.Post("/ui/admin/extforms/{id}/toggle", s.adminExtFormToggle)
	r.Post("/ui/admin/extforms/{id}/delete", s.adminExtFormDelete)
	r.Get("/ui/admin/extforms/{id}/export", s.adminExtFormExport)

	// Admin: external reports (внешний контур расширяемости)
	r.Get("/ui/admin/extreports", s.adminExtReports)
	r.Post("/ui/admin/extreports", s.adminExtReportUpload)
	r.Post("/ui/admin/extreports/{id}/toggle", s.adminExtReportToggle)
	r.Post("/ui/admin/extreports/{id}/delete", s.adminExtReportDelete)
	r.Get("/ui/admin/extreports/{id}/export", s.adminExtReportExport)

	// Admin: external processors (исполняют DSL — загрузка только админ)
	r.Get("/ui/admin/extprocessors", s.adminExtProcessors)
	r.Post("/ui/admin/extprocessors", s.adminExtProcessorUpload)
	r.Post("/ui/admin/extprocessors/{id}/toggle", s.adminExtProcessorToggle)
	r.Post("/ui/admin/extprocessors/{id}/trust", s.adminExtProcessorTrust)
	r.Post("/ui/admin/extprocessors/{id}/delete", s.adminExtProcessorDelete)
	r.Get("/ui/admin/extprocessors/{id}/export", s.adminExtProcessorExport)

	// Admin: scheduled jobs
	r.Get("/ui/admin/scheduled", s.scheduledList)
	r.Get("/ui/admin/scheduled/{name}", s.scheduledDetail)
	r.Post("/ui/admin/scheduled/{name}/run-now", s.scheduledRunNow)

	// Account registers
	r.Get("/ui/accounts/{plan}", s.accountsList)
	r.Get("/ui/accountreg/{name}", s.accountRegMovements)
	r.Get("/ui/accountreg/{name}/balances", s.accountRegBalances)

	// Query builder
	r.Get("/ui/query-builder", s.queryBuilder)

	// Developer tools
	r.Get("/ui/dev/query-console", s.queryConsolePage)
	r.Post("/ui/dev/query-exec", s.queryConsoleExec)
	r.Post("/ui/dev/query-analyze", s.queryConsoleAnalyze)
	r.Get("/ui/dev/entity-search", s.devEntitySearch)
	r.Get("/ui/dev/code-console", s.codeConsolePage)
	r.Post("/ui/dev/code-exec", s.codeConsoleExec)

	// All functions (admin only)
	r.Get("/ui/all-functions", s.allFunctions)

	// Constants
	r.Get("/ui/constants", s.constantsList)
	r.Post("/ui/constants", s.constantsSave)

	// Print forms — единые маршруты для всех видов (декларативные/DSL/legacy),
	// план 64, этап 3. Старый /print-dsl/ оставлен как 301-редирект на /print/.
	r.Get("/ui/{kind}/{entity}/{id}/print/{form}", s.printDocument)
	r.Get("/ui/{kind}/{entity}/{id}/print/{form}/pdf", s.printDocumentPDF)
	r.Get("/ui/{kind}/{entity}/{id}/print-dsl/{pfName}", s.redirectDSLPrint)
	r.Get("/ui/{kind}/{entity}/{id}/print-dsl/{pfName}/pdf", s.redirectDSLPrint)

	// Attachments
	r.Get("/ui/{kind}/{entity}/{id}/attachments", s.attachmentsList)
	r.Post("/ui/{kind}/{entity}/{id}/attachments", s.attachmentUpload)
	r.Get("/ui/attachments/{aid}/download", s.attachmentDownload)
	r.Post("/ui/attachments/{aid}/delete", s.attachmentDelete)

	// Поле типа image: загрузка картинки (в контексте сущности, право write) и
	// отдача бинарника по UUID. Ссылка (UUID) хранится в самой колонке поля.
	r.Post("/ui/{kind}/{entity}/_image", s.imageUpload)
	r.Get("/ui/_image/{id}", s.imageServe)

	// Excel exports
	r.Get("/ui/{kind}/{entity}/excel", s.listExcel)
	r.Get("/ui/report/{name}/excel", s.reportExcel)
	r.Get("/ui/journal/{name}/excel", s.journalExcel)

	// Journals
	r.Get("/ui/journal/{name}", s.journalList)

	// Оборудование кассира (мост браузер→локальный device-agent)
	r.Get("/ui/pos", s.posPage)
	r.Get("/ui/settings/agent", s.agentSettings)

	// About
	r.Get("/ui/about", s.about)
	r.Get("/ui/logo", s.logo)

	// Messages panel
	r.Get("/ui/messages", s.messagesList)
	r.Post("/ui/messages/clear", s.messagesClear)

	// AI assistant chat (план 51, F3)
	r.Get("/ui/ai/enabled", s.aiEnabled)
	r.Post("/ui/ai/chat", s.aiChat)
}

// MountDebug registers debug API routes gated by an internal shared token
// (X-OneBase-Debug-Token). api.New mounts this only when a token is configured
// (ONEBASE_DEBUG_TOKEN), so a plain `onebase run` (published base) exposes no
// debug surface at all. The configurator reaches these via its server-side
// debug proxy, which attaches the token.
func (s *Server) MountDebug(r chi.Router) {
	r.Route("/debug/global", func(r chi.Router) {
		r.Use(debugTokenMiddleware(s.cfg.DebugToken))
		r.Post("/enable", s.debugGlobalEnable)
		r.Post("/disable", s.debugGlobalDisable)
		r.Get("/status", s.debugGlobalStatus)
		r.Post("/breakpoint", s.debugGlobalBreakpoint)
		r.Post("/continue", s.debugGlobalContinue)
		r.Post("/step", s.debugGlobalStep)
		r.Post("/stop", s.debugGlobalStop)
		r.Post("/evaluate", s.debugGlobalEvaluate)
	})
}

type navSection struct {
	Kind     string
	Entities []*metadata.Entity
}

type navItem struct {
	Label string
	URL   string
}

type navGroup struct {
	Kind  string
	Items []navItem
	// Open — раскрыта ли группа по умолчанию, когда меню рендерится
	// сворачиваемым. Лёгкие группы (Справочники/Документы) открыты, тяжёлые
	// (Регистры/Отчёты/Обработки/Журналы) свёрнуты, чтобы меню не растягивалось.
	Open bool
}

func (s *Server) buildNav(r *http.Request, sub string) []navGroup {
	if sub == "" {
		// Глобальная «Главная». Если в config/home_page.yaml задан блок nav —
		// меню скоупится по нему (как у подсистемы). Иначе — плоский список
		// всех читаемых объектов («нейтральный старт»).
		if hp := s.reg.HomePage(); hp != nil && hp.Nav != nil && !hp.Nav.IsEmpty() {
			return s.buildNavFromContents(r, hp.Nav, "")
		}
		return s.buildFlatNav(r)
	}
	if cur := s.reg.GetSubsystem(sub); cur != nil {
		return s.buildNavForSubsystem(r, cur, sub)
	}
	return s.buildFlatNav(r)
}

func strSet(names []string) map[string]bool {
	m := make(map[string]bool, len(names))
	for _, n := range names {
		m[n] = true
	}
	return m
}

func (s *Server) buildNavForSubsystem(r *http.Request, sub *metadata.Subsystem, subName string) []navGroup {
	return s.buildNavFromContents(r, &sub.Contents, "?subsystem="+subName)
}

// buildNavFromContents строит левое меню по набору объектов (contents),
// фильтруя их по правам пользователя (s.can). q — суффикс URL (например
// "?subsystem=Продажи") для сохранения контекста подсистемы в ссылках.
func (s *Server) buildNavFromContents(r *http.Request, contents *metadata.SubsystemContents, q string) []navGroup {
	lang := s.resolveLang(r)
	var nav []navGroup

	if len(contents.Catalogs) > 0 || len(contents.Documents) > 0 {
		catSet := strSet(contents.Catalogs)
		docSet := strSet(contents.Documents)
		entities := s.reg.Entities()
		sort.Slice(entities, func(i, j int) bool { return entities[i].Name < entities[j].Name })
		var catalogs, documents []navItem
		for _, e := range entities {
			if !s.can(r, string(e.Kind), e.Name, "read") {
				continue
			}
			url := "/ui/" + strings.ToLower(string(e.Kind)) + "/" + e.Name + q
			if e.Kind == metadata.KindCatalog && catSet[e.Name] {
				catalogs = append(catalogs, navItem{Label: e.DisplayName(lang), URL: url})
			} else if e.Kind == metadata.KindDocument && docSet[e.Name] {
				documents = append(documents, navItem{Label: e.DisplayName(lang), URL: url})
			}
		}
		if len(catalogs) > 0 {
			nav = append(nav, navGroup{Kind: s.tr(lang, "Справочники"), Items: catalogs, Open: true})
		}
		if len(documents) > 0 {
			nav = append(nav, navGroup{Kind: s.tr(lang, "Документы"), Items: documents, Open: true})
		}
	}

	if len(contents.Registers) > 0 {
		regSet := strSet(contents.Registers)
		registers := s.reg.Registers()
		sort.Slice(registers, func(i, j int) bool { return registers[i].Name < registers[j].Name })
		var regItems []navItem
		for _, reg := range registers {
			if !regSet[reg.Name] {
				continue
			}
			if !s.can(r, "register", reg.Name, "read") {
				continue
			}
			regItems = append(regItems, navItem{
				Label: reg.DisplayName(lang) + " (" + s.tr(lang, "движения") + ")",
				URL:   "/ui/register/" + strings.ToLower(reg.Name) + q,
			})
			regItems = append(regItems, navItem{
				Label: reg.DisplayName(lang) + " (" + s.tr(lang, "остатки") + ")",
				URL:   "/ui/register/" + strings.ToLower(reg.Name) + "/balances" + q,
			})
		}
		if len(regItems) > 0 {
			nav = append(nav, navGroup{Kind: s.tr(lang, "Регистры"), Items: regItems})
		}
	}

	if len(contents.InfoRegs) > 0 {
		irSet := strSet(contents.InfoRegs)
		inforegs := s.reg.InfoRegisters()
		sort.Slice(inforegs, func(i, j int) bool { return inforegs[i].Name < inforegs[j].Name })
		var irItems []navItem
		for _, ir := range inforegs {
			if !irSet[ir.Name] {
				continue
			}
			if !s.can(r, "inforeg", ir.Name, "read") {
				continue
			}
			label := ir.Name
			if ir.Periodic {
				label += " (" + s.tr(lang, "периодический") + ")"
			}
			irItems = append(irItems, navItem{Label: label, URL: "/ui/inforeg/" + strings.ToLower(ir.Name) + q})
		}
		if len(irItems) > 0 {
			nav = append(nav, navGroup{Kind: s.tr(lang, "Регистры сведений"), Items: irItems})
		}
	}

	if len(contents.Reports) > 0 {
		repSet := strSet(contents.Reports)
		reps := s.reg.Reports()
		sort.Slice(reps, func(i, j int) bool { return reps[i].Name < reps[j].Name })
		var repItems []navItem
		for _, rep := range reps {
			if !repSet[rep.Name] {
				continue
			}
			if !s.can(r, "report", rep.Name, "run") {
				continue
			}
			label := rep.Title
			if label == "" {
				label = rep.Name
			}
			repItems = append(repItems, navItem{Label: label, URL: "/ui/report/" + strings.ToLower(rep.Name) + q})
		}
		if len(repItems) > 0 {
			nav = append(nav, navGroup{Kind: s.tr(lang, "Отчёты"), Items: repItems})
		}
	}

	if len(contents.Processors) > 0 {
		procSet := strSet(contents.Processors)
		procs := s.reg.Processors()
		sort.Slice(procs, func(i, j int) bool { return procs[i].Name < procs[j].Name })
		var procItems []navItem
		for _, proc := range procs {
			if !procSet[proc.Name] {
				continue
			}
			if !s.can(r, "processor", proc.Name, "run") {
				continue
			}
			label := proc.Title
			if label == "" {
				label = proc.Name
			}
			procItems = append(procItems, navItem{Label: label, URL: "/ui/processor/" + strings.ToLower(proc.Name) + q})
		}
		if len(procItems) > 0 {
			nav = append(nav, navGroup{Kind: s.tr(lang, "Обработки"), Items: procItems})
		}
	}

	if len(contents.Journals) > 0 {
		jSet := strSet(contents.Journals)
		journals := s.reg.Journals()
		sort.Slice(journals, func(i, j int) bool { return journals[i].Name < journals[j].Name })
		var jItems []navItem
		for _, j2 := range journals {
			if !jSet[j2.Name] {
				continue
			}
			jItems = append(jItems, navItem{Label: j2.DisplayName(lang), URL: "/ui/journal/" + strings.ToLower(j2.Name) + q})
		}
		if len(jItems) > 0 {
			nav = append(nav, navGroup{Kind: s.tr(lang, "Журналы"), Items: jItems})
		}
	}

	if len(contents.Pages) > 0 {
		pageSet := strSet(contents.Pages)
		pages := s.reg.Pages()
		sort.Slice(pages, func(i, j int) bool { return pages[i].Name < pages[j].Name })
		var pageItems []navItem
		for _, pg := range pages {
			if !pageSet[pg.Name] || !s.canSeePage(r, pg) {
				continue
			}
			pageItems = append(pageItems, navItem{Label: pg.DisplayName(lang), URL: "/ui/page/" + pg.Name + q})
		}
		if len(pageItems) > 0 {
			nav = append(nav, navGroup{Kind: s.tr(lang, "Страницы"), Items: pageItems, Open: true})
		}
	}

	return nav
}

func (s *Server) buildFlatNav(r *http.Request) []navGroup {
	lang := s.resolveLang(r)
	entities := s.reg.Entities()
	sort.Slice(entities, func(i, j int) bool { return entities[i].Name < entities[j].Name })

	var catalogs, documents []navItem
	for _, e := range entities {
		if !s.can(r, string(e.Kind), e.Name, "read") {
			continue
		}
		url := "/ui/" + strings.ToLower(string(e.Kind)) + "/" + e.Name
		item := navItem{Label: e.DisplayName(lang), URL: url}
		if e.Kind == metadata.KindCatalog {
			catalogs = append(catalogs, item)
		} else {
			documents = append(documents, item)
		}
	}

	registers := s.reg.Registers()
	sort.Slice(registers, func(i, j int) bool { return registers[i].Name < registers[j].Name })
	var regItems []navItem
	for _, reg := range registers {
		if !s.can(r, "register", reg.Name, "read") {
			continue
		}
		regItems = append(regItems, navItem{
			Label: reg.DisplayName(lang) + " (" + s.tr(lang, "движения") + ")",
			URL:   "/ui/register/" + strings.ToLower(reg.Name),
		})
		regItems = append(regItems, navItem{
			Label: reg.DisplayName(lang) + " (" + s.tr(lang, "остатки") + ")",
			URL:   "/ui/register/" + strings.ToLower(reg.Name) + "/balances",
		})
	}

	var nav []navGroup
	if len(catalogs) > 0 {
		nav = append(nav, navGroup{Kind: s.tr(lang, "Справочники"), Items: catalogs, Open: true})
	}
	if len(documents) > 0 {
		nav = append(nav, navGroup{Kind: s.tr(lang, "Документы"), Items: documents, Open: true})
	}
	if len(regItems) > 0 {
		nav = append(nav, navGroup{Kind: s.tr(lang, "Регистры"), Items: regItems})
	}

	inforegs := s.reg.InfoRegisters()
	sort.Slice(inforegs, func(i, j int) bool { return inforegs[i].Name < inforegs[j].Name })
	var inforegItems []navItem
	for _, ir := range inforegs {
		if !s.can(r, "inforeg", ir.Name, "read") {
			continue
		}
		label := ir.DisplayName(lang)
		if ir.Periodic {
			label += " (" + s.tr(lang, "периодический") + ")"
		}
		inforegItems = append(inforegItems, navItem{
			Label: label,
			URL:   "/ui/inforeg/" + strings.ToLower(ir.Name),
		})
	}
	if len(inforegItems) > 0 {
		nav = append(nav, navGroup{Kind: s.tr(lang, "Регистры сведений"), Items: inforegItems})
	}

	reps := s.reg.Reports()
	sort.Slice(reps, func(i, j int) bool { return reps[i].Name < reps[j].Name })
	var repItems []navItem
	for _, rep := range reps {
		if !s.can(r, "report", rep.Name, "run") {
			continue
		}
		label := rep.DisplayName(lang)
		if rep.External {
			label += " (" + s.tr(lang, "внешний") + ")"
		}
		repItems = append(repItems, navItem{
			Label: label,
			URL:   "/ui/report/" + strings.ToLower(rep.Name),
		})
	}
	if len(repItems) > 0 {
		nav = append(nav, navGroup{Kind: s.tr(lang, "Отчёты"), Items: repItems})
	}

	procs := s.reg.Processors()
	sort.Slice(procs, func(i, j int) bool { return procs[i].Name < procs[j].Name })
	isAdmin := s.isAdmin(r)
	var procItems []navItem
	for _, proc := range procs {
		if !s.can(r, "processor", proc.Name, "run") {
			continue
		}
		// Внешняя недоверенная обработка видна только администратору.
		if proc.External && !proc.Trusted && !isAdmin {
			continue
		}
		label := proc.DisplayName(lang)
		if proc.External {
			label += " (" + s.tr(lang, "внешняя") + ")"
		}
		procItems = append(procItems, navItem{
			Label: label,
			URL:   "/ui/processor/" + strings.ToLower(proc.Name),
		})
	}
	if len(procItems) > 0 {
		nav = append(nav, navGroup{Kind: s.tr(lang, "Обработки"), Items: procItems})
	}

	journals := s.reg.Journals()
	sort.Slice(journals, func(i, j int) bool { return journals[i].Name < journals[j].Name })
	var journalItems []navItem
	for _, j := range journals {
		journalItems = append(journalItems, navItem{Label: j.DisplayName(lang), URL: "/ui/journal/" + strings.ToLower(j.Name)})
	}
	if len(journalItems) > 0 {
		nav = append(nav, navGroup{Kind: s.tr(lang, "Журналы"), Items: journalItems})
	}

	if len(s.reg.Constants()) > 0 {
		nav = append(nav, navGroup{Kind: "Настройки", Items: []navItem{
			{Label: s.tr(lang, "Константы"), URL: "/ui/constants"},
		}})
	}
	return nav
}

// resolveLang determines the effective UI language for the current request.
func (s *Server) resolveLang(r *http.Request) string {
	if s.cfg.Bundle == nil {
		return "ru"
	}
	var userLang string
	if u := auth.UserFromContext(r.Context()); u != nil {
		userLang = u.Lang
	}
	accept := r.Header.Get("Accept-Language")
	return i18n.Resolve(userLang, s.cfg.Lang, accept, s.cfg.Bundle)
}

// errText локализует сообщение об ошибке для языка текущего запроса.
func (s *Server) errText(r *http.Request, err error) string {
	return i18nerr.Localize(s.cfg.Bundle, s.resolveLang(r), err)
}

// setLang saves the user's preferred language.
func (s *Server) setLang(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, s.errText(r, err), 400)
		return
	}
	lang := r.FormValue("lang")
	u := auth.UserFromContext(r.Context())
	if u != nil && s.authRepo != nil {
		_ = s.authRepo.SetUserLang(r.Context(), u.ID, lang)
	}
	http.Redirect(w, r, r.Header.Get("Referer"), http.StatusSeeOther)
}
