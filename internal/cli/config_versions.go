package cli

import (
	"context"
	"encoding/json"
	"os"

	"github.com/ivantit66/onebase/internal/configdb"
	"github.com/spf13/cobra"
)

var configCmd = &cobra.Command{
	Use:           "config",
	Short:         "Операции с конфигурацией, хранящейся в БД",
	SilenceUsage:  true,
	SilenceErrors: true,
}

var configVersionsCmd = &cobra.Command{
	Use:           "versions",
	Short:         "Показать snapshots конфигурации из _config_versions",
	RunE:          runConfigVersions,
	SilenceUsage:  true,
	SilenceErrors: true,
}

var configDiffCmd = &cobra.Command{
	Use:           "diff <before> <after>",
	Short:         "Показать file-level diff двух snapshots конфигурации",
	Args:          cobra.ExactArgs(2),
	RunE:          runConfigDiff,
	SilenceUsage:  true,
	SilenceErrors: true,
}

var configRollbackCmd = &cobra.Command{
	Use:           "rollback <version>",
	Short:         "Откатить _onebase_config к snapshot и создать новый snapshot отката",
	Args:          cobra.ExactArgs(1),
	RunE:          runConfigRollback,
	SilenceUsage:  true,
	SilenceErrors: true,
}

func init() {
	addBaseFlags(configVersionsCmd)
	configVersionsCmd.Flags().Int("limit", 20, "сколько версий показать")
	configVersionsCmd.Flags().Bool("json", false, "вывести JSON")

	addBaseFlags(configDiffCmd)
	configDiffCmd.Flags().Bool("json", false, "вывести JSON")

	addBaseFlags(configRollbackCmd)
	configRollbackCmd.Flags().String("message", "", "сообщение нового snapshot отката")
	configRollbackCmd.Flags().Bool("json", false, "вывести JSON")

	configCmd.AddCommand(configVersionsCmd, configDiffCmd, configRollbackCmd)
	rootCmd.AddCommand(configCmd)
}

func configRepoFromFlags(cmd *cobra.Command) (*baseConfig, *configdb.Repo, func(), error) {
	bc, err := resolveBase(cmd)
	if err != nil {
		return nil, nil, nil, err
	}
	ctx := context.Background()
	db, err := bc.OpenDB(ctx)
	if err != nil {
		bc.Cleanup()
		return nil, nil, nil, err
	}
	repo := configdb.New(db)
	cleanup := func() {
		db.Close()
		bc.Cleanup()
	}
	if err := repo.EnsureSchema(ctx); err != nil {
		cleanup()
		return nil, nil, nil, err
	}
	if err := repo.EnsureVersionSchema(ctx); err != nil {
		cleanup()
		return nil, nil, nil, err
	}
	return bc, repo, cleanup, nil
}

func runConfigVersions(cmd *cobra.Command, _ []string) error {
	_, repo, cleanup, err := configRepoFromFlags(cmd)
	if err != nil {
		return err
	}
	defer cleanup()
	limit, _ := cmd.Flags().GetInt("limit")
	versions, err := repo.ListVersions(context.Background(), limit)
	if err != nil {
		return err
	}
	if jsonOut, _ := cmd.Flags().GetBool("json"); jsonOut {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(versions)
	}
	if len(versions) == 0 {
		outln("Версий нет")
		return nil
	}
	for _, v := range versions {
		outf("%s  %s  %s  %s\n", v.ID, v.CreatedAt.Format("2006-01-02 15:04:05"), v.AuthorLogin, v.Message)
	}
	return nil
}

func runConfigDiff(cmd *cobra.Command, args []string) error {
	_, repo, cleanup, err := configRepoFromFlags(cmd)
	if err != nil {
		return err
	}
	defer cleanup()
	diff, err := repo.DiffVersions(context.Background(), args[0], args[1])
	if err != nil {
		return err
	}
	if jsonOut, _ := cmd.Flags().GetBool("json"); jsonOut {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(diff)
	}
	if len(diff) == 0 {
		outln("Различий нет")
		return nil
	}
	for _, d := range diff {
		outf("%s  %s\n", d.Kind, d.Path)
	}
	return nil
}

func runConfigRollback(cmd *cobra.Command, args []string) error {
	_, repo, cleanup, err := configRepoFromFlags(cmd)
	if err != nil {
		return err
	}
	defer cleanup()
	msg, _ := cmd.Flags().GetString("message")
	v, err := repo.RollbackToVersion(context.Background(), args[0], configdb.VersionOptions{Message: msg})
	if err != nil {
		return err
	}
	if jsonOut, _ := cmd.Flags().GetBool("json"); jsonOut {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(v)
	}
	outf("Откат выполнен. Новая версия: %s (%s)\n", v.ID, v.Message)
	return nil
}
