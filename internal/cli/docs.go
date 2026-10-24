package cli

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/ivantit66/onebase/internal/fsmode"
	"github.com/ivantit66/onebase/internal/userhelp"
	"github.com/spf13/cobra"
)

func newDocsCommand() *cobra.Command {
	docs := &cobra.Command{Use: "docs", Short: "Пользовательская справка конфигурации", SilenceUsage: true, SilenceErrors: true}
	initDoc := &cobra.Command{Use: "init", Short: "Добавить каркас руководства без перезаписи существующих файлов", RunE: func(cmd *cobra.Command, _ []string) error {
		dir, _ := cmd.Flags().GetString("project")
		return userhelp.Scaffold(dir)
	}}
	initDoc.Flags().String("project", ".", "каталог конфигурации")
	check := &cobra.Command{Use: "check", Short: "Информационная проверка справки (предупреждения не влияют на код выхода)", RunE: runDocsCheck}
	export := &cobra.Command{Use: "export", Short: "Собрать автономный HTML-сайт и bundle.json для внешних издателей", RunE: runDocsExport}
	publish := &cobra.Command{Use: "publish", Short: "План или публикация руководства в Confluence Server/DC либо Cloud", RunE: runDocsPublish}
	for _, cmd := range []*cobra.Command{check, export, publish} {
		addBaseFlags(cmd)
	}
	check.Flags().Bool("json", false, "машиночитаемый отчёт")
	export.Flags().String("output", "", "отдельный каталог результата (обязателен)")
	publish.Flags().String("url", "", "корень Confluence, включая /confluence или /wiki")
	publish.Flags().String("space", "", "ключ пространства Server/DC или числовой ID Cloud")
	publish.Flags().String("parent", "", "ID существующей родительской страницы")
	publish.Flags().String("user", "", "логин для Basic Auth; пусто — Bearer PAT")
	publish.Flags().String("token-env", "ONEBASE_CONFLUENCE_TOKEN", "имя переменной окружения с паролем/API token/PAT")
	publish.Flags().String("state", ".onebase-help-confluence.json", "локальный файл ID и версий страниц; храните для повторной публикации")
	publish.Flags().Bool("cloud", false, "Confluence Cloud (API v2, URL с /wiki)")
	publish.Flags().Bool("apply", false, "выполнить публикацию; без флага показать только план")
	docs.AddCommand(initDoc, check, export, publish, newDocsCaptureCommand())
	return docs
}

func init() { rootCmd.AddCommand(newDocsCommand()) }

func docsBook(cmd *cobra.Command) (*userhelp.Book, *baseConfig, error) {
	bc, err := resolveBase(cmd)
	if err != nil {
		return nil, nil, err
	}
	return userhelp.Load(bc.Dir), bc, nil
}

func runDocsCheck(cmd *cobra.Command, _ []string) error {
	b, bc, err := docsBook(cmd)
	if err != nil {
		return err
	}
	defer bc.Cleanup()
	if asJSON, _ := cmd.Flags().GetBool("json"); asJSON {
		return json.NewEncoder(cmd.OutOrStdout()).Encode(map[string]any{"enabled": b.Enabled, "auto_update": b.AutoUpdate, "chapters": len(b.Chapters), "warnings": b.Warnings})
	}
	_, _ = fmt.Fprintf(cmd.OutOrStdout(), "documentation: enabled=%t auto_update=%t chapters=%d\n", b.Enabled, b.AutoUpdate, len(b.Chapters))
	for _, warning := range b.Warnings {
		_, _ = fmt.Fprintln(cmd.OutOrStdout(), "warning:", warning)
	}
	return nil
}

func runDocsExport(cmd *cobra.Command, _ []string) error {
	b, bc, err := docsBook(cmd)
	if err != nil {
		return err
	}
	defer bc.Cleanup()
	output, _ := cmd.Flags().GetString("output")
	if output == "" {
		return fmt.Errorf("--output is required")
	}
	// Keep generated exports out of the project's sources and DB/backup bundle.
	source, err := filepath.Abs(bc.Dir)
	if err != nil {
		return err
	}
	dest, err := filepath.Abs(output)
	if err != nil {
		return err
	}
	rel, err := filepath.Rel(source, dest)
	if err != nil && strings.EqualFold(filepath.VolumeName(source), filepath.VolumeName(dest)) {
		return err
	}
	if err == nil && (rel == "." || (!strings.HasPrefix(rel, ".."+string(filepath.Separator)) && rel != ".." && !filepath.IsAbs(rel))) {
		return fmt.Errorf("--output must be outside the configuration directory")
	}
	for _, warning := range b.Warnings {
		_, _ = fmt.Fprintln(cmd.ErrOrStderr(), "warning:", warning)
	}
	return b.Export(dest)
}

func runDocsPublish(cmd *cobra.Command, _ []string) error {
	b, bc, err := docsBook(cmd)
	if err != nil {
		return err
	}
	defer bc.Cleanup()
	if !b.Available() {
		return fmt.Errorf("user documentation is disabled or empty")
	}
	for _, warning := range b.Warnings {
		_, _ = fmt.Fprintln(cmd.ErrOrStderr(), "warning:", warning)
	}
	apply, _ := cmd.Flags().GetBool("apply")
	if !apply {
		for _, c := range b.Chapters {
			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "%s: %s (parent: %s)\n", c.ID, c.Title, c.Parent)
		}
		_, _ = fmt.Fprintln(cmd.OutOrStdout(), "Plan only; use --apply to publish.")
		return nil
	}
	baseURL, _ := cmd.Flags().GetString("url")
	space, _ := cmd.Flags().GetString("space")
	parent, _ := cmd.Flags().GetString("parent")
	user, _ := cmd.Flags().GetString("user")
	env, _ := cmd.Flags().GetString("token-env")
	cloud, _ := cmd.Flags().GetBool("cloud")
	statePath, _ := cmd.Flags().GetString("state")
	if parent == "" {
		return fmt.Errorf("--parent is required")
	}
	pub, err := userhelp.NewConfluence(userhelp.ConfluenceOptions{BaseURL: baseURL, Space: space, User: user, Token: os.Getenv(env), Cloud: cloud})
	if err != nil {
		return err
	}
	lock, err := os.OpenFile(statePath+".lock", os.O_CREATE|os.O_EXCL|os.O_WRONLY, fsmode.SecretFile)
	if err != nil {
		return fmt.Errorf("publication lock: %w", err)
	}
	defer func() {
		if err := lock.Close(); err != nil {
			_, _ = fmt.Fprintln(cmd.ErrOrStderr(), err)
		}
		if err := os.Remove(statePath + ".lock"); err != nil {
			_, _ = fmt.Fprintln(cmd.ErrOrStderr(), err)
		}
	}()
	state := userhelp.PublishState{Target: pub.Target(parent)}
	if raw, err := os.ReadFile(statePath); err == nil {
		if err := json.Unmarshal(raw, &state); err != nil {
			return fmt.Errorf("publication state: %w", err)
		}
		if state.Target != pub.Target(parent) {
			return fmt.Errorf("state belongs to a different Confluence target; use another --state file")
		}
	} else if !os.IsNotExist(err) {
		return err
	}
	return b.Publish(cmd.Context(), pub, parent, &state, func() error { return saveDocsState(statePath, state) })
}

func saveDocsState(name string, state userhelp.PublishState) error {
	f, err := os.CreateTemp(filepath.Dir(name), ".help-state-*")
	if err != nil {
		return err
	}
	defer func() { _ = os.Remove(f.Name()) }()
	writeErr := json.NewEncoder(f).Encode(state)
	closeErr := f.Close()
	if writeErr != nil {
		return writeErr
	}
	if closeErr != nil {
		return closeErr
	}
	return os.Rename(f.Name(), name)
}
