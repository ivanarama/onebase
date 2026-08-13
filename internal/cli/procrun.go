package cli

import (
	"context"
	"fmt"
	"strings"

	"github.com/ivantit66/onebase/internal/project"
	"github.com/ivantit66/onebase/internal/ui"
	"github.com/spf13/cobra"
)

var procrunCmd = &cobra.Command{
	Use:   "procrun",
	Short: "Запустить обработку (Выполнить) офлайн — для отладки",
	Long: `Запускает процедуру Выполнить() обработки вне HTTP-сервера и печатает
вывод Сообщить(). Файловые параметры читаются с диска (автоопределение
кодировки UTF-8/Windows-1251), как при загрузке через браузер.

Пример:
  onebase procrun --id <baseID> --proc ЗагрузкаВыписки \
    --set Действие=Предпросмотр --set Формат=Авто \
    --file ТекстВыгрузки=C:\path\kl_to_1c.txt`,
	RunE: runProcrun,
}

func init() {
	addBaseFlags(procrunCmd)
	procrunCmd.Flags().String("proc", "", "имя обработки (обязательно)")
	procrunCmd.Flags().StringArray("set", nil, "параметр обработки: ключ=значение (можно несколько)")
	procrunCmd.Flags().StringArray("file", nil, "файловый параметр: ключ=путь (можно несколько)")
	rootCmd.AddCommand(procrunCmd)
}

func runProcrun(cmd *cobra.Command, _ []string) error {
	procName, _ := cmd.Flags().GetString("proc")
	if procName == "" {
		return fmt.Errorf("укажите --proc <имя обработки>")
	}

	bc, err := resolveBase(cmd)
	if err != nil {
		return err
	}
	defer bc.Cleanup()

	ctx := context.Background()
	db, err := bc.OpenDB(ctx)
	if err != nil {
		return err
	}
	defer db.Close()

	proj, err := project.Load(bc.Dir)
	if err != nil {
		return fmt.Errorf("load project: %w", err)
	}
	defer proj.Close()

	// file_storage.s3: обработка может сохранять картинки (СохранитьКартинку),
	// поэтому S3-бэкенд нужен и в headless-режиме.
	if appCfg, cfgErr := project.LoadConfig(bc.Dir); cfgErr == nil {
		if err := applyFileStorageS3(db, appCfg); err != nil {
			return err
		}
	}

	// Offline processors use the same catalog/document writers as the server.
	// Those writers may emit mandatory audit events (for example governed
	// publish/rollback decisions), so procrun must initialize the platform
	// audit schema just like run/dev do. This is idempotent for existing bases
	// and makes a freshly migrated SQLite database usable from procrun.
	if err := db.EnsureAuditSchema(ctx); err != nil {
		return fmt.Errorf("audit schema: %w", err)
	}
	if err := db.EnsureStageHistorySchema(ctx); err != nil {
		return fmt.Errorf("stage history schema: %w", err)
	}

	strParams := parseKeyVals(cmd, "set")
	fileParams := parseKeyVals(cmd, "file")

	messages, runErr, err := ui.RunProcessorOffline(ctx, proj, db, procName, strParams, fileParams)
	if err != nil {
		return err
	}
	for _, m := range messages {
		outln(m)
	}
	if runErr != nil {
		return fmt.Errorf("ошибка выполнения: %w", runErr)
	}
	return nil
}

// parseKeyVals разбирает повторяющиеся флаги вида ключ=значение в map.
func parseKeyVals(cmd *cobra.Command, flag string) map[string]string {
	pairs, _ := cmd.Flags().GetStringArray(flag)
	out := make(map[string]string, len(pairs))
	for _, p := range pairs {
		if i := strings.IndexByte(p, '='); i >= 0 {
			out[p[:i]] = p[i+1:]
		}
	}
	return out
}
