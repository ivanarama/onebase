package project

import (
	"context"
	"fmt"
	oblog "github.com/ivantit66/onebase/internal/logging"
	"io"
	"os"
	"path/filepath"
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
	"github.com/ivantit66/onebase/internal/secrets"
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
	Intakes          []*metadata.Intake       // план 90: входные шлюзы приёмки (идемпотентность + DLQ)
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
	// Deprecated compatibility keys accepted since v0.9.3 used a permissive
	// YAML decoder. They were never applied by the runtime; keeping them here
	// lets existing projects start while `onebase check` reports migration
	// warnings instead of rejecting the whole configuration.
	DeprecatedStorageType        string   `yaml:"storage_type,omitempty"`
	DeprecatedStorageLocation    string   `yaml:"storage_location,omitempty"`
	DeprecatedOfficeAllowedTypes []string `yaml:"office_allowed_types,omitempty"`
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
	Enabled   bool      `yaml:"enabled"`
	Schedule  string    `yaml:"schedule"`     // cron, по умолчанию "0 2 * * *"
	KeepLast  int       `yaml:"keep_last"`    // по умолчанию 7
	Directory string    `yaml:"directory"`    // пусто = <project>/backups
	S3        *S3Config `yaml:"s3,omitempty"` // опциональная off-site выгрузка в S3
}

// S3Config describes an optional S3-compatible off-site target for backups
// (AWS S3, MinIO, Ceph RGW, …). Секреты задавайте через ${env:VAR}, чтобы ключи
// жили в окружении, а не в app.yaml / git / дампе конфигурации.
type S3Config struct {
	Endpoint  string `yaml:"endpoint"`             // host[:port], напр. s3.amazonaws.com или minio.local:9000
	Region    string `yaml:"region"`               // напр. us-east-1 (по умолчанию us-east-1)
	Bucket    string `yaml:"bucket"`               //
	Prefix    string `yaml:"prefix"`               // ключ-префикс в бакете, напр. "prod/"
	AccessKey string `yaml:"access_key"`           // или ${env:VAR}
	SecretKey string `yaml:"secret_key"`           // или ${env:VAR}
	UseSSL    *bool  `yaml:"use_ssl,omitempty"`    // nil = true (https)
	PathStyle *bool  `yaml:"path_style,omitempty"` // nil = true (scheme://endpoint/bucket/key)
	KeepLast  int    `yaml:"keep_last"`            // ротация объектов в бакете; 0 = не ротировать (только backup)
	// Stream (только file_storage): раздавать S3-вложения потоком через Range
	// вместо временной копии на диске. nil/false = временный файл (по умолчанию).
	Stream *bool `yaml:"stream,omitempty"`
}

// FileStorageConfig holds the optional S3-compatible backend for живые файлы
// (image-блобы; вложения — следующим этапом). Активируется, когда режим хранения
// (_settings ui.file_storage) = "s3"; креды берутся отсюда, а не из БД, чтобы не
// уехать в дамп конфигурации. keep_last здесь не применяется. План 110, этап 2.
type FileStorageConfig struct {
	S3 *S3Config `yaml:"s3,omitempty"`
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

// DBConfig holds optional PostgreSQL connection-pool sizing (план 111, P0-1).
// Applies only to PostgreSQL; SQLite always uses a single connection. Zero means
// "use the OneBase default" (see storage.PoolConfig). An explicit pool_max_conns
// in the DSN still wins if this is left unset. Honored by `onebase run` with
// file-based config; under --config-source=database size the pool via the DSN.
type DBConfig struct {
	PoolMaxConns int32 `yaml:"pool_max_conns,omitempty"` // максимум соединений пула (0 = дефолт 20)
	PoolMinConns int32 `yaml:"pool_min_conns,omitempty"` // тёплый минимум пула (0 = дефолт 2)
}

// AppConfig holds the optional config/app.yaml metadata.
type AppConfig struct {
	Name    string `yaml:"name"`
	Version string `yaml:"version"`
	// Авторство и лицензия конфигурации (план 69). Необязательны. Едут вместе
	// с конфигурацией (app.yaml попадает в файл / в _onebase_config / в .obz) —
	// чтобы форк или поставка клиенту имели определённого правообладателя.
	Author    string `yaml:"author,omitempty"`
	Copyright string `yaml:"copyright,omitempty"`
	License   string `yaml:"license,omitempty"`
	// Support — куда пользователю этой конфигурации сообщать об ошибках
	// (план 115). Свободный текст: почта, телефон, чат первой линии. Едет с
	// конфигурацией по той же причине, что и Author: поставка клиенту несёт
	// СВОЮ поддержку, а не трекер разработчика платформы, до которого
	// пользователь не дойдёт и куда ему не надо.
	Support     string             `yaml:"support,omitempty"`
	Lang        string             `yaml:"lang,omitempty"`
	Logo        string             `yaml:"logo,omitempty"`
	Email       *EmailConfig       `yaml:"email,omitempty"`
	Attachments *AttachmentsConfig `yaml:"attachments,omitempty"`
	// DeprecatedRussianPost preserves the permissive v0.9.3 behavior for
	// downstream project-owned integration settings. OneBase does not consume
	// this block; `onebase check` asks projects to move it out of app.yaml.
	DeprecatedRussianPost map[string]any     `yaml:"russian_post,omitempty"`
	Demo                  *DemoConfig        `yaml:"demo,omitempty"`
	Backup                *BackupConfig      `yaml:"backup,omitempty"`
	FileStorage           *FileStorageConfig `yaml:"file_storage,omitempty"`
	AI                    *AIConfig          `yaml:"ai,omitempty"`
	Limits                *LimitsConfig      `yaml:"limits,omitempty"`
	DSL                   *DSLConfig         `yaml:"dsl,omitempty"`
	DB                    *DBConfig          `yaml:"db,omitempty"`
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
	defer oblog.CloseQuiet("project", "файл", f)

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
	// Секреты (ключи ИИ, токены вебхуков, креды S3, секреты HTTP-сервисов и
	// шлюзов приёма) здесь НАМЕРЕННО не разыменовываются: ссылка env:/file:/enc:
	// остаётся в конфигурации, значение подставляется в момент использования —
	// llm.Config.Resolve, webhook.send, objstore.New, проверка аутентификации
	// сервиса/шлюза (план 83).
	//
	// Раньше ключи ИИ раскрывались прямо здесь, при загрузке app.yaml, и
	// applyAppAISettings клал в _settings.llm.config уже РАСКРЫТЫЙ ключ —
	// откуда он уезжал в обычный дамп бэкапа открытым текстом. То есть секрет,
	// аккуратно вынесенный администратором в ${env:...}, всё равно оказывался
	// в базе значением.
	return &cfg, nil
}

// resolveSecretRef разыменовывает ссылку на секрет в поле, которое потребители
// читают из конфигурации напрямую (адрес узла обмена).
//
// Ошибка разыменования (нечитаемый file:, enc: без мастер-ключа) не роняет
// загрузку конфигурации: поле остаётся пустым — подсистема, которой оно нужно,
// выключится, — а причина уходит в журнал. Сервер при этом жив: одна незаданная
// переменная не должна мешать работать остальной базе.
func resolveSecretRef(field, s string) string {
	v, err := secrets.Default().Resolve(s)
	if err != nil {
		oblog.Component("secrets").Warn("ссылка на секрет не разыменована",
			"поле", field, "ошибка", err)
		return ""
	}
	return v
}

// ResolveSecrets возвращает копию S3-конфига с разыменованными ссылками
// (env:/file:/enc:) в адресе и кредах. Вызывается в момент создания клиента —
// off-site бэкапом и хранилищем файлов, — а не при загрузке app.yaml: ключи
// доступа не должны жить в конфигурации значением (план 83).
//
// objstore намеренно оставлен листовым пакетом (чистый S3-клиент, ничего не
// знающий о ссылках на секреты OneBase), поэтому разыменование живёт здесь.
func (s *S3Config) ResolveSecrets() (S3Config, error) {
	out := *s
	r := secrets.Default()
	for _, f := range []struct {
		name string
		p    *string
	}{
		{"endpoint", &out.Endpoint},
		{"access_key", &out.AccessKey},
		{"secret_key", &out.SecretKey},
	} {
		v, err := r.Resolve(*f.p)
		if err != nil {
			return out, fmt.Errorf("%s: %w", f.name, err)
		}
		*f.p = v
	}
	return out, nil
}

// LoadFromDB loads project metadata from the _onebase_config table, writing
// to a temp directory, then calling Load on it.
func LoadFromDB(ctx context.Context, repo *configdb.Repo) (*Project, error) {
	tmpDir, err := os.MkdirTemp("", "onebase-cfg-")
	if err != nil {
		return nil, fmt.Errorf("project: mktempdir: %w", err)
	}

	if err := repo.ExportToDir(ctx, tmpDir); err != nil {
		oblog.RemoveQuiet("project", tmpDir)
		return nil, fmt.Errorf("project: export from db: %w", err)
	}

	proj, err := Load(tmpDir)
	if err != nil {
		oblog.RemoveQuiet("project", tmpDir)
		return nil, err
	}

	proj.cleanup = func() { oblog.RemoveQuiet("project", tmpDir) }
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
	if err := p.loadIntakes(); err != nil {
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
// поддерживают ссылки env:/file:/enc: и разыменовываются при проверке
// аутентификации вызова (internal/ui/services.go), а не здесь — в конфигурации
// остаётся ссылка (план 83).
func (p *Project) loadHTTPServices() error {
	services, err := httpservice.LoadDir(filepath.Join(p.Dir, "services"))
	if err != nil {
		return fmt.Errorf("project: load http services: %w", err)
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
	// Адреса узлов допускают ${env:VAR} — удобно для per-deploy хостов. Это не
	// секрет, а адрес: разыменовываем при загрузке, потребителей у него много.
	for _, pl := range plans {
		for i := range pl.Nodes {
			pl.Nodes[i].URL = resolveSecretRef("exchange."+pl.Name+".node."+pl.Nodes[i].Code+".url", pl.Nodes[i].URL)
		}
	}
	p.ExchangePlans = plans
	return nil
}

// loadIntakes читает intake/*.yaml (план 90). Каждый шлюз валидируется сразу —
// битое объявление ловится на загрузке, до рантайма. Обработчик (handler) —
// процедура модуля, резолвится транспортным слоем при вызове.
func (p *Project) loadIntakes() error {
	intakes, err := metadata.LoadIntakeDir(filepath.Join(p.Dir, "intake"))
	if err != nil {
		return fmt.Errorf("project: load intakes: %w", err)
	}
	for _, in := range intakes {
		// Валидируем по ссылке, как она записана: плейсхолдер считается заданным
		// секретом (onebase check проходит без выставленных переменных окружения).
		// Само значение подставляется при проверке подлинности отправителя
		// (internal/ui/intake_http.go), а не здесь (план 83).
		if err := in.Validate(); err != nil {
			return fmt.Errorf("project: intake %q: %w", in.Name, err)
		}
	}
	p.Intakes = intakes
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
