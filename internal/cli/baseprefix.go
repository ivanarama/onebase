package cli

import (
	"context"
	"fmt"
	"strings"

	"github.com/ivantit66/onebase/internal/storage"
	"github.com/spf13/cobra"
)

// onebase base prefix — префикс ЭТОЙ базы (план 117D).
//
// Префикс живёт в данных базы, а не в конфигурации: конфигурация одинакова во
// всех базах, поэтому «понять, откуда загружен объект» через неё невозможно by
// design — обе выдали бы один и тот же префикс.
//
// Команда отдельная, а не флаг `exchange init`, потому что префикс нужен и без
// обмена: отличать тестовую базу от боевой в печатных формах, например. Прятать
// его в настройку обмена значило бы связать с тем, с чем он не связан.

var baseCmd = &cobra.Command{
	Use:           "base",
	Short:         "Параметры этой информационной базы",
	SilenceUsage:  true,
	SilenceErrors: true,
}

var basePrefixCmd = &cobra.Command{
	Use:   "prefix",
	Short: "Показать или задать префикс этой базы",
	Long: `Префикс подставляется в коды и номера объектов, у которых объявлено
numerator.base_prefix: true. Он живёт в данных базы, а не в конфигурации —
иначе все базы выдавали бы один и тот же префикс, и по коду нельзя было бы
понять, откуда объект приехал.

Без --set команда показывает текущее значение. Пустое --set снимает префикс.

Примеры:
  onebase base prefix --sqlite base.db
  onebase base prefix --set Ф- --sqlite base.db`,
	RunE:          runBasePrefix,
	SilenceUsage:  true,
	SilenceErrors: true,
}

func init() {
	addBaseFlags(basePrefixCmd)
	basePrefixCmd.Flags().String("set", "", "новый префикс базы (пустая строка снимает)")
	baseCmd.AddCommand(basePrefixCmd)
	rootCmd.AddCommand(baseCmd)
}

func runBasePrefix(cmd *cobra.Command, _ []string) error {
	ctx := context.Background()
	sqlitePath, _ := cmd.Flags().GetString("sqlite")

	var (
		db  *storage.DB
		err error
	)
	if sqlitePath != "" {
		db, err = openCLIStorage(ctx, "sqlite", sqlitePath, "")
	} else {
		db, err = openCLIStorage(ctx, "postgres", "", dsnFromFlags(cmd))
	}
	if err != nil {
		return err
	}
	defer db.Close()

	if cmd.Flags().Changed("set") {
		value, _ := cmd.Flags().GetString("set")
		value = strings.TrimSpace(value)
		if err := db.SaveBasePrefix(ctx, value); err != nil {
			return err
		}
		if value == "" {
			outf("Префикс базы снят\n")
			return nil
		}
		outf("Префикс базы: %s\n", value)
		return nil
	}

	current := db.GetBasePrefix(ctx)
	if current == "" {
		outf("Префикс базы: не задан\n")
		outf("Задать: onebase base prefix --set <префикс>\n")
		return nil
	}
	outf("Префикс базы: %s\n", current)
	return nil
}

// resetBasePrefixAfterRestore гасит префикс при восстановлении копии в ДРУГУЮ
// базу: клон, сохранивший префикс оригинала, выдавал бы те же коды, и обмен
// склеил бы разные объекты. Возвращает прежнее значение для сообщения.
func resetBasePrefixAfterRestore(ctx context.Context, db *storage.DB) (string, error) {
	prev := db.GetBasePrefix(ctx)
	if prev == "" {
		return "", nil
	}
	if err := db.SaveBasePrefix(ctx, ""); err != nil {
		return "", fmt.Errorf("сброс префикса базы: %w", err)
	}
	return prev, nil
}
