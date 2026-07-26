package project

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"unicode"
	"unicode/utf8"

	"github.com/ivantit66/onebase/internal/configdb"
	"github.com/ivantit66/onebase/internal/dsl/ast"
	"github.com/ivantit66/onebase/internal/dsl/lexer"
	"github.com/ivantit66/onebase/internal/dsl/loader"
	"github.com/ivantit66/onebase/internal/dsl/parser"
	"github.com/ivantit66/onebase/internal/httpservice"
	"github.com/ivantit66/onebase/internal/llm"
	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/ivantit66/onebase/internal/page"
	"github.com/ivantit66/onebase/internal/printform"
	"github.com/ivantit66/onebase/internal/processor"
	"github.com/ivantit66/onebase/internal/report"
	"github.com/ivantit66/onebase/internal/webhook"
	"gopkg.in/yaml.v3"
)

type Project struct {
	Dir              string
	Entities         []*metadata.Entity
	Registers        []*metadata.Register
	InfoRegisters    []*metadata.InfoRegister
	Enums            []*metadata.Enum
	Constants        []*metadata.Constant
	Reports          []*report.Report
	PrintForms       []*printform.PrintForm
	DSLPrintForms    []*printform.DSLPrintForm
	LayoutForms      []*printform.LayoutForm // декларативные формы (standalone .layout.yaml)
	Programs         map[string]*ast.Program // entity name → parsed DSL (модуль объекта)
	ManagerPrograms  map[string]*ast.Program // entity name → parsed DSL (модуль менеджера)
	ServicePrograms  map[string]*ast.Program // план 61: service name → обработчики .service.os (отдельный namespace, чтобы не затирать модуль одноимённого документа)
	PagePrograms     map[string]*ast.Program // план 66: page name → обработчики .page.os (отдельный namespace, как у сервисов)
	Processors       []*processor.Processor
	HTTPServices     []*httpservice.Service   // план 61: опубликованные HTTP-сервисы
	Pages            []*page.Page             // план 66: страницы (произвольные представления на DSL)
	ExchangePlans    []*metadata.ExchangePlan // план 86: планы обмена данными между базами
	Modules          map[string]*ast.Program  // module name → parsed procs
	Subsystems       []*metadata.Subsystem
	Journals         []*metadata.Journal
	ScheduledJobs    []*metadata.ScheduledJob
	ChartsOfAccounts []*metadata.ChartOfAccounts
	AccountRegisters []*metadata.AccountRegister
	Widgets          []*metadata.Widget
	HomePage         *metadata.HomePage
	cleanup          func()
	cleanupOnce      sync.Once
}

// Close releases resources (e.g., temp dirs) associated with this Project.
func (p *Project) Close() {
	if p == nil {
		return
	}
	p.cleanupOnce.Do(func() {
		if p.cleanup != nil {
			p.cleanup()
			p.cleanup = nil
		}
	})
}

// EmailConfig holds SMTP configuration from app.yaml section "email".
type EmailConfig struct {
	SMTPHost    string `yaml:"smtp_host"`
	SMTPPort    int    `yaml:"smtp_port"`
	SMTPUser    string `yaml:"smtp_user"`
	SMTPPass    string `yaml:"smtp_password"`
	FromName    string `yaml:"from_name"`
	FromAddress string `yaml:"from_address"`
}

// AttachmentsConfig holds file attachment settings from app.yaml.
type AttachmentsConfig struct {
	MaxFileSizeMB int      `yaml:"max_file_size_mb"`
	AllowedTypes  []string `yaml:"allowed_types"`
}

// DemoConfig holds demo-mode settings from app.yaml section "demo".
type DemoConfig struct {
	Enabled       bool   `yaml:"enabled"`
	ResetBackup   string `yaml:"reset_backup"`   // путь к .obz относительно директории проекта
	ResetSchedule string `yaml:"reset_schedule"` // cron, по умолчанию "0 2 * * *"
	Message       string `yaml:"message"`        // текст баннера
}

// BackupConfig holds automatic backup settings from app.yaml section "backup".
type BackupConfig struct {
	Enabled   bool   `yaml:"enabled"`
	Schedule  string `yaml:"schedule"`  // cron, по умолчанию "0 2 * * *"
	KeepLast  int    `yaml:"keep_last"` // по умолчанию 7
	Directory string `yaml:"directory"` // пусто = <project>/backups
}

// AIConfig holds non-secret AI assistant settings from app.yaml section "ai".
// Secrets and provider routes stay in "llm"; this block is for deploy-time
// policy knobs that also live in _settings.
type AIConfig struct {
	DataScope     string `yaml:"data_scope,omitempty"` // admin_only|rbac|all
	DailyTokenCap *int   `yaml:"daily_token_cap,omitempty"`
}

// LimitsConfig holds optional runtime guardrails for heavy operations. Zero
// values mean "disabled" to preserve existing configuration behavior.
type LimitsConfig struct {
	RequestTimeoutSec      int `yaml:"request_timeout_sec,omitempty"`
	ReportTimeoutSec       int `yaml:"report_timeout_sec,omitempty"`
	ReportMaxRows          int `yaml:"report_max_rows,omitempty"`
	ReportConcurrency      int `yaml:"report_concurrency,omitempty"`
	ExportTimeoutSec       int `yaml:"export_timeout_sec,omitempty"`
	ExportMaxRows          int `yaml:"export_max_rows,omitempty"`
	ExportConcurrency      int `yaml:"export_concurrency,omitempty"`
	ProcessorTimeoutSec    int `yaml:"processor_timeout_sec,omitempty"`
	ProcessorConcurrency   int `yaml:"processor_concurrency,omitempty"`
	HTTPServiceTimeoutSec  int `yaml:"http_service_timeout_sec,omitempty"`
	HTTPServiceConcurrency int `yaml:"http_service_concurrency,omitempty"`
	SlowOperationMS        int `yaml:"slow_operation_ms,omitempty"`
}

// DSLConfig holds opt-in compatibility switches for the DSL runtime.
type DSLConfig struct {
	StrictLexicalScope bool `yaml:"strict_lexical_scope,omitempty"`
}

// AppConfig holds the optional config/app.yaml metadata.
type AppConfig struct {
	Name    string `yaml:"name"`
	Version string `yaml:"version"`
	// Авторство и лицензия конфигурации (план 69). Необязательны. Едут вместе
	// с конфигурацией (app.yaml попадает в файл / в _onebase_config / в .obz) —
	// чтобы форк или поставка клиенту имели определённого правообладателя.
	Author      string             `yaml:"author,omitempty"`
	Copyright   string             `yaml:"copyright,omitempty"`
	License     string             `yaml:"license,omitempty"`
	Lang        string             `yaml:"lang,omitempty"`
	Logo        string             `yaml:"logo,omitempty"`
	Email       *EmailConfig       `yaml:"email,omitempty"`
	Attachments *AttachmentsConfig `yaml:"attachments,omitempty"`
	Demo        *DemoConfig        `yaml:"demo,omitempty"`
	Backup      *BackupConfig      `yaml:"backup,omitempty"`
	AI          *AIConfig          `yaml:"ai,omitempty"`
	Limits      *LimitsConfig      `yaml:"limits,omitempty"`
	DSL         *DSLConfig         `yaml:"dsl,omitempty"`
	// LLM — необязательный конфиг ИИ-помощника прямо в конфигурации. Когда задан,
	// применяется к базе при старте (см. run.go) и имеет приоритет над _settings.
	// Ключи задавайте через ${env:VAR}, чтобы секрет жил в окружении, а не в
	// app.yaml/git/.obz. Удобно для демо/прод-деплоя.
	LLM *llm.Config `yaml:"llm,omitempty"`
	// Webhooks — исходящие веб-хуки на события платформы (план 29):
	// document.save/post/unpost/delete, catalog.save/delete. Токены в URL и
	// заголовках задавайте через ${env:VAR} — секрет живёт в окружении.
	Webhooks []webhook.Config `yaml:"webhooks,omitempty"`
}

// LoadConfig reads config/app.yaml from the project directory.
func LoadConfig(dir string) (*AppConfig, error) {
	path := filepath.Join(dir, "config", "app.yaml")
	f, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return &AppConfig{Name: filepath.Base(dir)}, nil
		}
		return nil, fmt.Errorf("project: read %s: %w", path, err)
	}
	defer f.Close()

	var cfg AppConfig
	dec := yaml.NewDecoder(f)
	dec.KnownFields(true)
	if err := dec.Decode(&cfg); err != nil {
		if err == io.EOF {
			return &AppConfig{Name: filepath.Base(dir)}, nil
		}
		return nil, fmt.Errorf("project: parse %s: %w", path, err)
	}
	var extra any
	if err := dec.Decode(&extra); err != io.EOF {
		if err == nil {
			return nil, fmt.Errorf("project: parse %s: multiple YAML documents are not allowed", path)
		}
		return nil, fmt.Errorf("project: parse %s: %w", path, err)
	}
	if cfg.LLM != nil {
		expandLLMEnv(cfg.LLM)
	}
	expandWebhookEnv(cfg.Webhooks)
	return &cfg, nil
}

// expandWebhookEnv подставляет ${env:VAR} в секрет-носители веб-хуков
// (URL с токеном бота, заголовки авторизации, тело).
func expandWebhookEnv(hooks []webhook.Config) {
	for i := range hooks {
		hooks[i].URL = expandEnvRefs(hooks[i].URL)
		hooks[i].Body = expandEnvRefs(hooks[i].Body)
		for k, v := range hooks[i].Headers {
			hooks[i].Headers[k] = expandEnvRefs(v)
		}
	}
}

// envRefPattern matches ${env:VAR} references that are substituted from the
// process environment — used for secrets like API keys so they live in env,
// not in app.yaml / git / .obz.
var envRefPattern = regexp.MustCompile(`\$\{env:([^}]+)\}`)

func expandEnvRefs(s string) string {
	return envRefPattern.ReplaceAllStringFunc(s, func(m string) string {
		name := envRefPattern.FindStringSubmatch(m)[1]
		return os.Getenv(strings.TrimSpace(name))
	})
}

// expandLLMEnv substitutes ${env:VAR} in secret-bearing fields of the LLM
// config (endpoint keys, base URLs, custom headers).
func expandLLMEnv(c *llm.Config) {
	for i := range c.Endpoints {
		c.Endpoints[i].APIKey = expandEnvRefs(c.Endpoints[i].APIKey)
		c.Endpoints[i].BaseURL = expandEnvRefs(c.Endpoints[i].BaseURL)
		for k, v := range c.Endpoints[i].Headers {
			c.Endpoints[i].Headers[k] = expandEnvRefs(v)
		}
	}
}

// LoadFromDB loads project metadata from the _onebase_config table, writing
// to a temp directory, then calling Load on it.
func LoadFromDB(ctx context.Context, repo *configdb.Repo) (*Project, error) {
	tmpDir, err := os.MkdirTemp("", "onebase-cfg-")
	if err != nil {
		return nil, fmt.Errorf("project: mktempdir: %w", err)
	}

	if err := repo.ExportToDir(ctx, tmpDir); err != nil {
		os.RemoveAll(tmpDir)
		return nil, fmt.Errorf("project: export from db: %w", err)
	}

	proj, err := Load(tmpDir)
	if err != nil {
		os.RemoveAll(tmpDir)
		return nil, err
	}

	proj.cleanup = func() { os.RemoveAll(tmpDir) }
	return proj, nil
}

func Load(dir string) (*Project, error) {
	p := &Project{
		Dir:             dir,
		Programs:        make(map[string]*ast.Program),
		ManagerPrograms: make(map[string]*ast.Program),
		ServicePrograms: make(map[string]*ast.Program),
		PagePrograms:    make(map[string]*ast.Program),
		Modules:         make(map[string]*ast.Program),
	}
	if err := p.loadMetadata(); err != nil {
		return nil, err
	}
	if err := metadata.Validate(p.Entities, p.Enums); err != nil {
		return nil, err
	}
	if err := metadata.ValidateConstants(p.Constants, p.Entities, p.Enums); err != nil {
		return nil, err
	}
	if err := p.loadDSL(); err != nil {
		return nil, err
	}
	if err := p.loadFormModules(); err != nil {
		return nil, err
	}
	if err := p.loadPrintForms(); err != nil {
		return nil, err
	}
	if err := p.loadProcessors(); err != nil {
		return nil, err
	}
	if err := p.loadProcessorForms(); err != nil {
		return nil, err
	}
	if err := p.loadHTTPServices(); err != nil {
		return nil, err
	}
	if err := p.loadPages(); err != nil {
		return nil, err
	}
	if err := p.loadExchangePlans(); err != nil {
		return nil, err
	}
	if err := p.loadSubsystems(); err != nil {
		return nil, err
	}
	if err := p.loadJournals(); err != nil {
		return nil, err
	}
	if err := p.loadScheduled(); err != nil {
		return nil, err
	}
	if err := p.loadAccounts(); err != nil {
		return nil, err
	}
	if err := p.loadAccountRegs(); err != nil {
		return nil, err
	}
	if err := p.loadWidgets(); err != nil {
		return nil, err
	}
	if err := p.loadHomePage(); err != nil {
		return nil, err
	}
	// Проверяем, что имена всех объектов и реквизитов пригодны как
	// неэкранированные SQL-идентификаторы (они подставляются в SQL без кавычек).
	// Здесь, в конце, потому что account-регистры грузятся выше после Validate.
	if err := metadata.ValidateIdentifiers(
		p.Entities, p.Registers, p.InfoRegisters, p.AccountRegisters, p.Enums, p.Constants,
	); err != nil {
		return nil, err
	}
	return p, nil
}

func (p *Project) loadWidgets() error {
	widgets, err := metadata.LoadWidgetDir(filepath.Join(p.Dir, "widgets"))
	if err != nil {
		return fmt.Errorf("project: load widgets: %w", err)
	}
	p.Widgets = widgets
	return nil
}

func (p *Project) loadHomePage() error {
	hp, err := metadata.LoadHomePage(filepath.Join(p.Dir, "config", "home_page.yaml"))
	if err != nil {
		return fmt.Errorf("project: load home_page: %w", err)
	}
	p.HomePage = hp
	return nil
}

func (p *Project) loadProcessors() error {
	procs, err := processor.LoadDir(filepath.Join(p.Dir, "processors"))
	if err != nil {
		return fmt.Errorf("project: load processors: %w", err)
	}
	p.Processors = procs
	if err := p.loadProcessorLayouts(); err != nil {
		return err
	}
	return nil
}

// loadProcessorLayouts подхватывает для каждой обработки заготовку макета
// src/<имя>.proc.layout.yaml (если она лежит рядом с .proc.os), которую
// генерирует конвертер 1С→OneBase. Имя файла строится по той же схеме, что и
// .proc.os (см. converter/writer): нижний регистр, пробелы → подчёркивания.
// Загруженный макет позже инжектируется в DSL как переменная «Макет» во всех
// путях запуска обработки.
//
// Режим конфигурации из БД (LoadFromDB) работает прозрачно: ExportToDir
// выгружает ВСЕ файлы конфигурации (включая src/*.proc.layout.yaml) во
// временный каталог и затем вызывает Load(tmpDir) — поэтому отдельной ветки
// для БД здесь не требуется, файловая загрузка покрывает оба случая.
func (p *Project) loadProcessorLayouts() error {
	srcDir := filepath.Join(p.Dir, "src")
	for _, proc := range p.Processors {
		base := strings.ToLower(strings.ReplaceAll(proc.Name, " ", "_"))
		osPath := filepath.Join(srcDir, base+".proc.os")
		layoutPath := printform.FindLayoutFile(osPath)
		if layoutPath == "" {
			continue
		}
		lt, err := printform.LoadLayout(layoutPath)
		if err != nil {
			return fmt.Errorf("project: load processor layout %s: %w", layoutPath, err)
		}
		proc.Layout = lt
	}
	return nil
}

// loadHTTPServices читает services/*.yaml (план 61). Секреты (auth token/hmac)
// поддерживают ${env:VAR} — значение живёт в окружении, не в YAML/git/.obz.
func (p *Project) loadHTTPServices() error {
	services, err := httpservice.LoadDir(filepath.Join(p.Dir, "services"))
	if err != nil {
		return fmt.Errorf("project: load http services: %w", err)
	}
	for _, s := range services {
		s.Secret = expandEnvRefs(s.Secret)
	}
	p.HTTPServices = services
	return nil
}

// loadPages читает pages/*.yaml (план 66). Обработчики (.page.os) грузятся в
// loadDSL в отдельный namespace PagePrograms.
func (p *Project) loadPages() error {
	pages, err := page.LoadDir(filepath.Join(p.Dir, "pages"))
	if err != nil {
		return fmt.Errorf("project: load pages: %w", err)
	}
	p.Pages = pages
	return nil
}

// loadExchangePlans читает exchange/*.yaml (план 86). Обработчики конфликтов
// (.exchange.os) грузятся отдельно; для файлового цикла достаточно метаданных.
func (p *Project) loadExchangePlans() error {
	plans, err := metadata.LoadExchangePlanDir(filepath.Join(p.Dir, "exchange"))
	if err != nil {
		return fmt.Errorf("project: load exchange plans: %w", err)
	}
	// Адреса узлов допускают ${env:VAR} — удобно для per-deploy хостов.
	for _, pl := range plans {
		for i := range pl.Nodes {
			pl.Nodes[i].URL = expandEnvRefs(pl.Nodes[i].URL)
		}
	}
	p.ExchangePlans = plans
	return nil
}

func (p *Project) loadProcessorForms() error {
	managedLoader := loader.NewManagedFormLoader()
	for _, proc := range p.Processors {
		managed, err := managedLoader.LoadEntityForms(p.Dir, proc.Name)
		if err != nil {
			return fmt.Errorf("load managed forms for processor %s: %w", proc.Name, err)
		}
		proc.Forms = managed
	}
	return nil
}

func (p *Project) loadSubsystems() error {
	subs, err := metadata.LoadSubsystemDir(filepath.Join(p.Dir, "subsystems"))
	if err != nil {
		return fmt.Errorf("project: load subsystems: %w", err)
	}
	p.Subsystems = subs
	return nil
}

func (p *Project) loadJournals() error {
	journals, err := metadata.LoadJournalDir(filepath.Join(p.Dir, "journals"))
	if err != nil {
		return fmt.Errorf("project: load journals: %w", err)
	}
	p.Journals = journals
	return nil
}

func (p *Project) loadScheduled() error {
	jobs, err := metadata.LoadScheduledDir(filepath.Join(p.Dir, "scheduled"))
	if err != nil {
		return fmt.Errorf("project: load scheduled: %w", err)
	}
	p.ScheduledJobs = jobs
	return nil
}

func (p *Project) loadAccounts() error {
	charts, err := metadata.LoadChartOfAccountsDir(filepath.Join(p.Dir, "accounts"))
	if err != nil {
		return fmt.Errorf("project: load accounts: %w", err)
	}
	p.ChartsOfAccounts = charts
	return nil
}

func (p *Project) loadAccountRegs() error {
	regs, err := metadata.LoadAccountRegisterDir(filepath.Join(p.Dir, "accountregs"))
	if err != nil {
		return fmt.Errorf("project: load account registers: %w", err)
	}
	p.AccountRegisters = regs
	return nil
}

func (p *Project) loadPrintForms() error {
	forms, dslForms, layoutForms, err := printform.LoadDir(filepath.Join(p.Dir, "printforms"))
	if err != nil {
		return fmt.Errorf("project: load printforms: %w", err)
	}
	p.PrintForms = forms
	p.DSLPrintForms = dslForms
	p.LayoutForms = layoutForms
	return nil
}

func (p *Project) loadMetadata() error {
	type entry struct {
		subdir string
		kind   metadata.Kind
	}
	for _, e := range []entry{
		{"catalogs", metadata.KindCatalog},
		{"documents", metadata.KindDocument},
	} {
		dir := filepath.Join(p.Dir, e.subdir)
		items, err := os.ReadDir(dir)
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			return fmt.Errorf("readdir %s: %w", dir, err)
		}
		for _, item := range items {
			if item.IsDir() || !strings.HasSuffix(item.Name(), ".yaml") {
				continue
			}
			ent, err := metadata.LoadFile(filepath.Join(dir, item.Name()), e.kind)
			if err != nil {
				return err
			}
			p.Entities = append(p.Entities, ent)
		}
	}
	// load registers
	regDir := filepath.Join(p.Dir, "registers")
	items, err := os.ReadDir(regDir)
	if err == nil {
		for _, item := range items {
			if item.IsDir() || !strings.HasSuffix(item.Name(), ".yaml") {
				continue
			}
			reg, err := metadata.LoadRegisterFile(filepath.Join(regDir, item.Name()))
			if err != nil {
				return err
			}
			p.Registers = append(p.Registers, reg)
		}
	}
	// load info registers
	irDir := filepath.Join(p.Dir, "inforegs")
	irItems, err := os.ReadDir(irDir)
	if err == nil {
		for _, item := range irItems {
			if item.IsDir() || !strings.HasSuffix(item.Name(), ".yaml") {
				continue
			}
			ir, err := metadata.LoadInfoRegisterFile(filepath.Join(irDir, item.Name()))
			if err != nil {
				return err
			}
			p.InfoRegisters = append(p.InfoRegisters, ir)
		}
	}
	// load enums
	enumDir := filepath.Join(p.Dir, "enums")
	enumItems, err := os.ReadDir(enumDir)
	if err == nil {
		for _, item := range enumItems {
			if item.IsDir() || !strings.HasSuffix(item.Name(), ".yaml") {
				continue
			}
			e, err := metadata.LoadEnumFile(filepath.Join(enumDir, item.Name()))
			if err != nil {
				return err
			}
			p.Enums = append(p.Enums, e)
		}
	}
	// load constants (all .yaml files from constants/)
	constDir := filepath.Join(p.Dir, "constants")
	constItems, err := os.ReadDir(constDir)
	if err == nil {
		for _, item := range constItems {
			if item.IsDir() || !strings.HasSuffix(item.Name(), ".yaml") {
				continue
			}
			consts, err := metadata.LoadConstantsFile(filepath.Join(constDir, item.Name()))
			if err != nil {
				return err
			}
			p.Constants = append(p.Constants, consts...)
		}
	}
	// load reports
	repDir := filepath.Join(p.Dir, "reports")
	repItems, err := os.ReadDir(repDir)
	if err == nil {
		for _, item := range repItems {
			if item.IsDir() || !strings.HasSuffix(item.Name(), ".yaml") {
				continue
			}
			rep, err := report.LoadFile(filepath.Join(repDir, item.Name()))
			if err != nil {
				return err
			}
			p.Reports = append(p.Reports, rep)
		}
	}
	return nil
}

func (p *Project) loadDSL() error {
	srcDir := filepath.Join(p.Dir, "src")
	items, err := os.ReadDir(srcDir)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("readdir %s: %w", srcDir, err)
	}
	for _, item := range items {
		if item.IsDir() || !strings.HasSuffix(item.Name(), ".os") {
			continue
		}
		name := item.Name()
		isModule := strings.HasSuffix(name, ".module.os")
		isProc := strings.HasSuffix(name, ".proc.os")
		isPosting := strings.HasSuffix(name, ".posting.os")
		isReport := strings.HasSuffix(name, ".rep.os")
		isManager := strings.HasSuffix(name, ".manager.os")
		isService := strings.HasSuffix(name, ".service.os")
		isPage := strings.HasSuffix(name, ".page.os")

		fullPath := filepath.Join(srcDir, name)
		data, err := os.ReadFile(fullPath)
		if err != nil {
			return err
		}
		l := lexer.New(string(data), fullPath)
		pr := parser.New(l)
		prog, err := pr.ParseProgram()
		if err != nil {
			return err
		}

		// Исполняемый раздел модуля (тело модуля, issue #171). Парсер собирает
		// операторы вне процедур в prog.Body; допустимость решаем по типу:
		// у обработки тело становится точкой входа Выполнить, у остальных
		// модулей (объект/менеджер/общий/сервис/страница/отчёт) — ошибка, как
		// в 1С, где исполняемого раздела у этих модулей нет.
		if len(prog.Body) > 0 {
			if !isProc {
				return fmt.Errorf("%s: тело модуля (операторы вне процедур) допустимо только в обработках (.proc.os) — поместите код в процедуру", name)
			}
			for _, p := range prog.Procedures {
				if strings.EqualFold(p.Name.Literal, "Выполнить") {
					return fmt.Errorf("%s: в обработке есть и тело модуля, и процедура Выполнить — оставьте что-то одно", name)
				}
			}
			prog.Procedures = append(prog.Procedures, ast.NewProcedureFromBody("Выполнить", fullPath, prog.ModuleVars, prog.Body))
			prog.Body = nil
		}

		if isModule {
			base := strings.TrimSuffix(name, ".module.os")
			moduleName := fileNameToEntityBase(base)
			p.Modules[moduleName] = prog
			continue
		}

		if isManager {
			base := strings.TrimSuffix(name, ".manager.os")
			entityName := fileNameToEntityBase(base)
			if actual := p.findEntityName(entityName); actual != "" {
				entityName = actual
			}
			p.ManagerPrograms[entityName] = prog
			continue
		}

		if isProc {
			base := strings.TrimSuffix(name, ".proc.os")
			entityName := fileNameToEntityBase(base)
			p.Programs[entityName] = prog
			continue
		}
		if isService {
			// Обработчики HTTP-сервиса (план 61). Кладём в ОТДЕЛЬНУЮ карту
			// ServicePrograms (не в Programs!): иначе сервис, названный как
			// одноимённый документ, затирал бы модуль документа вместе со
			// слитой ОбработкаПроведения — и документ молча проводился без
			// движений. Роутер достаёт процедуру через GetServiceProcedure
			// с регистронезависимым фолбэком, поэтому имя файла должно
			// совпадать с именем сервиса (без учёта регистра).
			base := strings.TrimSuffix(name, ".service.os")
			entityName := fileNameToEntityBase(base)
			p.ServicePrograms[entityName] = prog
			continue
		}
		if isPage {
			// Обработчик страницы (план 66) — в ОТДЕЛЬНЫЙ namespace PagePrograms,
			// как у сервисов: страница может называться как одноимённый документ,
			// и затирать его модуль нельзя. Роутер достаёт процедуру через
			// GetPageProcedure (регистронезависимо), поэтому имя файла должно
			// совпадать с именем страницы (без учёта регистра).
			base := strings.TrimSuffix(name, ".page.os")
			entityName := fileNameToEntityBase(base)
			p.PagePrograms[entityName] = prog
			continue
		}
		if isReport {
			base := strings.TrimSuffix(name, ".rep.os")
			entityName := fileNameToEntityBase(base)
			if actual := p.findReportName(entityName); actual != "" {
				entityName = actual
			}
			p.Programs[entityName] = prog
			continue
		}

		var entityName string
		if isPosting {
			base := strings.TrimSuffix(name, ".posting.os")
			entityName = fileNameToEntityBase(base)
		} else {
			entityName = fileNameToEntity(name)
		}
		if actual := p.findEntityName(entityName); actual != "" {
			entityName = actual
		}
		if isPosting {
			if existing, ok := p.Programs[entityName]; ok {
				existing.Procedures = append(existing.Procedures, prog.Procedures...)
			} else {
				p.Programs[entityName] = prog
			}
		} else {
			p.Programs[entityName] = prog
		}
	}
	return nil
}

func (p *Project) loadFormModules() error {
	srcDir := filepath.Join(p.Dir, "src")
	formLoader := loader.NewFormLoader()
	managedLoader := loader.NewManagedFormLoader()

	for _, ent := range p.Entities {
		// 1. Управляемые формы (план 37): <projectRoot>/forms/<entity>/*.form.yaml.
		//    Если папки нет — managed остаётся nil, ничего не помечается managed.
		managed, err := managedLoader.LoadEntityForms(p.Dir, ent.Name)
		if err != nil {
			return fmt.Errorf("load managed forms for %s: %w", ent.Name, err)
		}

		// 2. Авто-формы (legacy): src/<entity>*.form.os.
		legacy, err := formLoader.LoadEntityForms(srcDir, ent.Name)
		if err != nil {
			return fmt.Errorf("load form modules for %s: %w", ent.Name, err)
		}

		// 3. Мерж: managed приоритетны. Legacy-формы с тем же Name отбрасываются,
		//    остальные добавляются в конец и помечаются autogen.
		taken := make(map[string]struct{}, len(managed))
		for _, f := range managed {
			taken[f.Name] = struct{}{}
		}
		merged := append([]*metadata.FormModule(nil), managed...)
		for _, f := range legacy {
			if _, dup := taken[f.Name]; dup {
				continue
			}
			if f.LayoutKind == "" {
				f.LayoutKind = metadata.FormLayoutAutogen
			}
			merged = append(merged, f)
		}

		ent.Forms = merged
	}
	return nil
}

// findEntityName returns the canonical entity name matching s case-insensitively.
func (p *Project) findEntityName(s string) string {
	sl := strings.ToLower(s)
	for _, e := range p.Entities {
		if strings.ToLower(e.Name) == sl {
			return e.Name
		}
	}
	return ""
}

func (p *Project) findReportName(s string) string {
	sl := strings.ToLower(s)
	for _, r := range p.Reports {
		if strings.ToLower(r.Name) == sl {
			return r.Name
		}
	}
	return ""
}

// fileNameToEntity converts "invoice.os" → "Invoice", "счёт.os" → "Счёт".
func fileNameToEntity(name string) string {
	return fileNameToEntityBase(strings.TrimSuffix(name, ".os"))
}

// fileNameToEntityBase capitalises the first rune of a bare name (no extension).
func fileNameToEntityBase(base string) string {
	if base == "" {
		return base
	}
	r, size := utf8.DecodeRuneInString(base)
	return string(unicode.ToUpper(r)) + base[size:]
}
