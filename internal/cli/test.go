package cli

import (
	"context"
	"fmt"
	"os"

	"github.com/ivantit66/onebase/internal/project"
	"github.com/ivantit66/onebase/internal/storage"
	"github.com/ivantit66/onebase/internal/ui"
	"github.com/spf13/cobra"
)

var testCmd = &cobra.Command{
	Use:   "test",
	Short: "Прогнать тесты уровня конфигурации (обработки kind: test)",
	Long: `Находит тест-обработки (в YAML указан kind: test), выполняет процедуру
Выполнить() каждой офлайн — как procrun — и собирает результаты встроенных
проверок Утверждать.*. Печатает по проверке на строку и итог; при провале
любой проверки или ошибке выполнения завершается с ненулевым кодом — пригодно
для pre-commit/CI.

Внутри теста доступны:
  Утверждать.Равно/НеРавно/Истина/Ложь/Заполнено/Провалить(…, "описание");
  Утверждать.РольМожет/РольНеМожет(Роль, Вид, Объект, Операция, "описание")
                                     — проверка матрицы прав роли из roles/*.yaml
                                        (Вид: документ/справочник/регистр/…);
  Утверждать.ПолеМаскируется/ПолеВидно/МаскаПоля(Роль, Вид, Объект, Поле, …)
                                     — полевой доступ / маскирование ПДн (план 88);
  Утверждать.СтрокиОграничены/СтрокиНеОграничены(Роль, Вид, Объект, Операция, …)
                                     — строковый доступ / RLS-фильтр (план 79);
  Часы.Установить(Дата)/Сбросить()   — заморозка ТекущаяДата()/ТекущаяДатаВремя();
  Мок.Email/Http/ОС/ИИ               — рекордеры внешних эффектов (почта/сеть/
                                        команды/ИИ не уходят наружу).

По умолчанию каждый тест идёт в своей транзакции с откатом (--isolation) — тесты
не оставляют данных. Формат вывода --format pretty|tap|junit (tap/junit — для CI).

Примеры:
  onebase test --project . --sqlite prodbase.db
  onebase test --project . --run Телефон
  onebase test --project . --sqlite :memory: --format junit --out report.xml`,
	RunE:          runTest,
	SilenceUsage:  true,
	SilenceErrors: true,
}

func init() {
	addBaseFlags(testCmd)
	testCmd.Flags().String("run", "", "маска по имени теста (регистронезависимая подстрока)")
	testCmd.Flags().String("isolation", "transaction",
		"изоляция данных: transaction (откат после теста) | none | schema (эфемерная схема PostgreSQL)")
	testCmd.Flags().String("format", "pretty", "формат отчёта: pretty | tap | junit")
	testCmd.Flags().String("out", "", "файл для отчёта (по умолчанию stdout)")
	rootCmd.AddCommand(testCmd)
}

func runTest(cmd *cobra.Command, _ []string) error {
	isolation, _ := cmd.Flags().GetString("isolation")
	switch isolation {
	case "", ui.IsolationTransaction, ui.IsolationNone, ui.IsolationSchema:
	default:
		return fmt.Errorf("неизвестный режим --isolation %q (доступны transaction, none, schema)", isolation)
	}
	format, _ := cmd.Flags().GetString("format")
	switch format {
	case "", ui.FormatPretty, ui.FormatTAP, ui.FormatJUnit:
	default:
		return fmt.Errorf("неизвестный формат --format %q (доступны pretty, tap, junit)", format)
	}

	bc, err := resolveBase(cmd)
	if err != nil {
		return err
	}
	defer bc.Cleanup()

	ctx := context.Background()

	// schema-изоляция: весь прогон во временной схеме PostgreSQL, удаляемой
	// CASCADE в конце. Внутри тесты идут без пер-тестовой транзакции (none) —
	// режим для тестов, которые сами управляют транзакциями. Только PostgreSQL.
	db, runIsolation, cleanupSchema, err := openDBForIsolation(ctx, bc, isolation)
	if err != nil {
		return err
	}
	defer db.Close()
	if cleanupSchema != nil {
		defer cleanupSchema()
	}

	proj, err := project.Load(bc.Dir)
	if err != nil {
		return fmt.Errorf("load project: %w", err)
	}
	defer proj.Close()

	// Тесты гоняются на готовой схеме: применяем миграции (идемпотентно) —
	// иначе на свежей/`:memory:` базе запись справочников/документов падает на
	// «no such table». Плюс схема аудита, как procrun/run/dev.
	if err := applyAllMigrations(ctx, db, proj); err != nil {
		return fmt.Errorf("migrate: %w", err)
	}
	if err := db.EnsureAuditSchema(ctx); err != nil {
		return fmt.Errorf("audit schema: %w", err)
	}
	if err := db.EnsureStageHistorySchema(ctx); err != nil {
		return fmt.Errorf("stage history schema: %w", err)
	}

	filter, _ := cmd.Flags().GetString("run")
	res, err := ui.RunTests(ctx, proj, db, ui.TestRunOptions{Filter: filter, Isolation: runIsolation})
	if err != nil {
		return err
	}

	if err := emitReport(cmd, res, format); err != nil {
		return err
	}

	if len(res.Cases) == 0 {
		// Нет тестов — не провал (конфигурация может их ещё не завести), но
		// сообщаем явно, чтобы «зелёный» прогон не вводил в заблуждение.
		fmt.Fprintln(os.Stderr, "Тесты не найдены (обработки с kind: test).")
		return nil
	}

	if !res.OK() {
		if cleanupSchema != nil {
			cleanupSchema() // defer не выполнится при os.Exit — чистим схему явно
		}
		db.Close()
		bc.Cleanup()
		os.Exit(1)
	}
	return nil
}

// openDBForIsolation открывает БД для прогона. Для schema-изоляции создаёт
// эфемерную схему PostgreSQL и возвращает cleanup для её удаления; внутренняя
// (пер-тестовая) изоляция при этом — none. Для transaction/none открывает базу
// обычным способом.
func openDBForIsolation(ctx context.Context, bc *baseConfig, isolation string) (db *storage.DB, runIsolation string, cleanupSchema func(), err error) {
	if isolation != ui.IsolationSchema {
		db, err = bc.OpenDB(ctx)
		return db, isolation, nil, err
	}
	if bc.DBType == "sqlite" {
		return nil, "", nil, fmt.Errorf("--isolation schema доступна только на PostgreSQL (для SQLite используйте transaction или :memory:)")
	}
	schema := storage.NewEphemeralSchemaName()
	// ConnectWithSchema deliberately bypasses baseConfig.OpenDB, so guard the
	// public restore marker and acquire its lifetime lease before that connector
	// performs any database-wide setup or creates the ephemeral schema.
	guard, guardErr := openCLIStorage(ctx, "postgres", "", bc.DSN)
	if guardErr != nil {
		return nil, "", nil, guardErr
	}
	db, err = storage.ConnectWithSchema(ctx, bc.DSN, schema)
	if err != nil {
		guard.Close()
		return nil, "", nil, err
	}
	db.AddCloseHook(func() error {
		guard.Close()
		return nil
	})
	if err = db.CreateSchema(ctx, schema); err != nil {
		db.Close()
		return nil, "", nil, fmt.Errorf("создать временную схему %s: %w", schema, err)
	}
	cleanup := func() {
		if derr := db.DropSchemaCascade(context.Background(), schema); derr != nil {
			fmt.Fprintf(os.Stderr, "предупреждение: не удалось удалить временную схему %s: %v\n", schema, derr)
		}
	}
	// Внутри эфемерной схемы тесты идут без пер-тестового отката.
	return db, ui.IsolationNone, cleanup, nil
}

// emitReport пишет отчёт в файл (--out) или в stdout. При записи в файл в
// stderr печатается краткая сводка, чтобы прогон не выглядел «немым».
func emitReport(cmd *cobra.Command, res ui.TestRunResult, format string) error {
	outPath, _ := cmd.Flags().GetString("out")
	if outPath == "" {
		return ui.WriteReport(os.Stdout, res, format)
	}
	f, err := os.Create(outPath)
	if err != nil {
		return fmt.Errorf("создать файл отчёта %q: %w", outPath, err)
	}
	defer closeRead("файл отчёта", f)
	if err := ui.WriteReport(f, res, format); err != nil {
		return err
	}
	tests, passed, _, _ := res.Totals()
	fmt.Fprintf(os.Stderr, "Отчёт (%s) записан в %s — тестов: %d, успешно: %d\n",
		format, outPath, tests, passed)
	return nil
}
