package cli

import (
	"context"
	"fmt"
	"os"

	"github.com/ivantit66/onebase/internal/storage"
	"github.com/spf13/cobra"
)

// Команда `onebase vacuum` (план 114) — обслуживание хранилища после массового
// удаления: свёртки регистров, сборки бинарников, чистки движений.
//
// Отдельной командой, а не шагом внутри rollup/gc-blobs: на SQLite это
// исключительная блокировка базы и вторая копия файла на диске, и решать, когда
// это уместно, должен администратор, а не команда, которую он запустил ради
// другого.

var vacuumCmd = &cobra.Command{
	Use:   "vacuum",
	Short: "Сжать базу и обновить статистику после массового удаления",
	Long: `Возвращает место, освободившееся после массового удаления (свёртка,
сборка бинарников, очистка движений).

SQLite: VACUUM — файл перезаписывается и уменьшается. На время операции база
заблокирована, а на диске нужно место под вторую копию файла.

PostgreSQL: VACUUM (ANALYZE) — освобождает место внутри файлов и обновляет
статистику планировщика, работе не мешая. VACUUM FULL не выполняется: он
переписывает таблицы под эксклюзивной блокировкой, и решать за администратора,
что базу можно остановить, платформа не вправе.

Примеры:
  onebase vacuum --sqlite base.db
  onebase vacuum --db postgres://localhost/onebase`,
	RunE:          runVacuum,
	SilenceUsage:  true,
	SilenceErrors: true,
}

func init() {
	vacuumCmd.Flags().String("db", "", "PostgreSQL DSN (или переменная DATABASE_URL)")
	vacuumCmd.Flags().String("sqlite", "", "путь к файлу SQLite (альтернатива --db)")
	rootCmd.AddCommand(vacuumCmd)
}

func runVacuum(cmd *cobra.Command, _ []string) error {
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

	before := fileSizeOrZero(sqlitePath)
	outln(db.ReclaimHint())
	if err := db.Reclaim(ctx); err != nil {
		return err
	}
	after := fileSizeOrZero(sqlitePath)
	if before > 0 && after > 0 {
		outf("Готово. Файл базы: %s → %s (освобождено %s).\n",
			humanBytes(before), humanBytes(after), humanBytes(before-after))
		return nil
	}
	outln("Готово.")
	return nil
}

// fileSizeOrZero возвращает размер файла; 0 — если путь пуст или файл недоступен
// (для PostgreSQL размер измерять нечем, и это не ошибка).
func fileSizeOrZero(path string) int64 {
	if path == "" {
		return 0
	}
	st, err := os.Stat(path)
	if err != nil {
		return 0
	}
	return st.Size()
}

func humanBytes(n int64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%d Б", n)
	}
	// Приставки списком, а не индексом по строке: индекс по строке берёт байт,
	// а кириллическая буква в UTF-8 занимает два — получалось «Ð» вместо «М».
	prefixes := []string{"К", "М", "Г", "Т"}
	div, exp := int64(unit), 0
	for v := n / unit; v >= unit && exp < len(prefixes)-1; v /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %sБ", float64(n)/float64(div), prefixes[exp])
}
