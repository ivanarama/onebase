package cli

import (
	"context"
	"fmt"

	"github.com/ivantit66/onebase/internal/configdb"
	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/ivantit66/onebase/internal/project"
	"github.com/ivantit66/onebase/internal/storage"
	"github.com/spf13/cobra"
)

// reindexCmd — полная пересборка полнотекстового индекса (план 82). Индекс
// поддерживается автоматически при записи объектов, поэтому команда нужна в
// трёх случаях: изменился состав `fulltext:` в метаданных, данные заливали в
// базу мимо платформы, либо индекс требуется восстановить после сбоя.
var reindexCmd = &cobra.Command{
	Use:   "reindex",
	Short: "Пересобрать полнотекстовый индекс из данных базы",
	RunE:  runReindex,
}

func init() {
	reindexCmd.Flags().String("project", ".", "path to project directory")
	reindexCmd.Flags().String("db", "", "database URL (overrides DATABASE_URL env)")
	reindexCmd.Flags().String("sqlite", "", "path to SQLite database file (alternative to --db)")
	reindexCmd.Flags().String("config-source", "file", "configuration source: file or database")
	reindexCmd.Flags().String("entity", "", "пересобрать только указанный справочник или документ")
	reindexCmd.Flags().Int("batch", 500, "сколько объектов читать и писать за одну транзакцию")
}

func runReindex(cmd *cobra.Command, _ []string) error {
	dir, _ := cmd.Flags().GetString("project")
	sqlitePath, _ := cmd.Flags().GetString("sqlite")
	configSource, _ := cmd.Flags().GetString("config-source")
	only, _ := cmd.Flags().GetString("entity")
	batch, _ := cmd.Flags().GetInt("batch")

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

	entities := proj.Entities
	if only != "" {
		var picked *metadata.Entity
		for _, e := range entities {
			if e.Name == only {
				picked = e
				break
			}
		}
		if picked == nil {
			return fmt.Errorf("объект %q не найден в конфигурации", only)
		}
		// Пересобираем только его: с одним элементом в списке общая чистка
		// «строк исчезнувших объектов» снесла бы индекс всех остальных.
		if _, err := db.EnsureFullTextSchema(ctx); err != nil {
			return err
		}
		n, err := db.ReindexEntityFullText(ctx, picked, batch)
		if err != nil {
			return err
		}
		outf("проиндексировано: %s — %d\n", picked.Name, n)
		return nil
	}

	total := 0
	stats, err := db.RebuildFullTextIndex(ctx, entities, batch, func(s storage.FTSRebuildStat) {
		outf("проиндексировано: %s — %d\n", s.Entity, s.Indexed)
	})
	for _, s := range stats {
		total += s.Indexed
	}
	if err != nil {
		return fmt.Errorf("пересборка полнотекстового индекса: %w", err)
	}
	outf("готово: объектов %d, записей в индексе %d\n", len(stats), total)
	return nil
}
