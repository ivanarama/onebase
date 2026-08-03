package cli

import (
	"context"
	"fmt"
	"time"

	"github.com/spf13/cobra"

	"github.com/ivantit66/onebase/internal/configdb"
	"github.com/ivantit66/onebase/internal/project"
	"github.com/ivantit66/onebase/internal/storage"
)

var gcBlobsCmd = &cobra.Command{
	Use:   "gc-blobs",
	Short: "Удалить бинарники-сироты (картинки, на которые не ссылается ни одна запись)",
	Long: `Сборка мусора хранилища бинарников (blobs) методом mark-and-sweep.

Собирает все живые ссылки из image-полей всех сущностей и удаляет только те
блобы, на которые не ссылается ни одна запись. Общий блоб (на него ссылаются
несколько записей) НЕ удаляется. Недавно загруженные блобы (моложе --min-age)
пропускаются — они могут быть ещё не привязаны к записи.

По умолчанию работает в режиме предпросмотра (dry-run) и ничего не удаляет;
для фактического удаления добавьте --delete.`,
	RunE: runGcBlobs,
}

func init() {
	gcBlobsCmd.Flags().String("project", ".", "path to project directory")
	gcBlobsCmd.Flags().String("db", "", "database URL (overrides DATABASE_URL env)")
	gcBlobsCmd.Flags().String("sqlite", "", "path to SQLite database file (alternative to --db)")
	gcBlobsCmd.Flags().String("config-source", "file", "configuration source: file or database")
	gcBlobsCmd.Flags().Duration("min-age", 24*time.Hour, "не трогать блобы моложе указанного возраста (grace-окно)")
	gcBlobsCmd.Flags().Bool("delete", false, "действительно удалить сироты (по умолчанию — только показать)")
	rootCmd.AddCommand(gcBlobsCmd)
}

func runGcBlobs(cmd *cobra.Command, _ []string) error {
	dir, _ := cmd.Flags().GetString("project")
	sqlitePath, _ := cmd.Flags().GetString("sqlite")
	configSource, _ := cmd.Flags().GetString("config-source")
	minAge, _ := cmd.Flags().GetDuration("min-age")
	doDelete, _ := cmd.Flags().GetBool("delete")

	ctx := context.Background()
	var (
		db  *storage.DB
		err error
	)
	if sqlitePath != "" {
		db, err = storage.ConnectSQLite(ctx, sqlitePath)
	} else {
		db, err = storage.Connect(ctx, dsnFromFlags(cmd))
	}
	if err != nil {
		return err
	}
	defer db.Close()

	var proj *project.Project
	if configSource == "database" {
		cfgRepo := configdb.New(db)
		if err := cfgRepo.EnsureSchema(ctx); err != nil {
			return fmt.Errorf("configdb schema: %w", err)
		}
		proj, err = project.LoadFromDB(ctx, cfgRepo)
	} else {
		proj, err = project.Load(dir)
	}
	if err != nil {
		return fmt.Errorf("load project: %w", err)
	}
	defer proj.Close()

	// file_storage.s3: чтобы GC мог удалять объекты s3-блобов, нужен клиент S3.
	// Креды берём из app.yaml проекта (для config-source=file); при отсутствии —
	// удаление s3-блоба вернёт понятную ошибку вместо тихого осиротения объекта.
	if appCfg, cfgErr := project.LoadConfig(dir); cfgErr == nil {
		if err := applyFileStorageS3(db, appCfg); err != nil {
			return err
		}
	}

	if err := db.EnsureBlobTable(ctx); err != nil {
		return err
	}

	st, err := db.SweepOrphanBlobs(ctx, proj.Entities, minAge, !doDelete)
	if err != nil {
		return err
	}

	outf("Всего блобов:        %d\n", st.TotalBlobs)
	outf("Живых ссылок:        %d\n", st.LiveRefs)
	outf("Защищено grace-окном: %d (моложе %s)\n", st.Protected, minAge)
	outf("Сирот:               %d\n", len(st.Orphans))
	if doDelete {
		outf("Удалено:             %d\n", st.Deleted)
		printReclaimHint(db, st.Deleted)
	} else if len(st.Orphans) > 0 {
		outln("Режим предпросмотра — ничего не удалено. Для удаления добавьте --delete.")
	}
	return nil
}
