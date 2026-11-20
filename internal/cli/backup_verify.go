package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/ivantit66/onebase/internal/backup"
	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/spf13/cobra"
)

func init() {
	cmd := &cobra.Command{
		Use:     "verify",
		Short:   "Проверить восстановление SQLite-копии по числу проведённых документов",
		Example: "  onebase backup verify --file backup.db --document РеализацияТоваров --expect-posted 42",
		Args:    cobra.NoArgs,
		RunE:    runBackupVerify,
	}
	cmd.Flags().String("file", "", "автономный файл резервной копии SQLite")
	cmd.Flags().String("document", "", "имя документа в конфигурации")
	cmd.Flags().Int64("expect-posted", 0, "ожидаемое число проведённых документов этого вида на момент копии")
	cmd.Flags().Bool("json", false, "вывести отчёт в JSON")
	for _, name := range []string{"file", "document", "expect-posted"} {
		mustMarkRequired(cmd, name)
	}
	backupCmd.AddCommand(cmd)
}

type backupVerifyReport struct {
	OK             bool      `json:"ok"`
	File           string    `json:"file"`
	Document       string    `json:"document"`
	ExpectedPosted int64     `json:"expected_posted"`
	ActualPosted   *int64    `json:"actual_posted,omitempty"`
	CheckedAt      time.Time `json:"checked_at"`
	DurationMS     int64     `json:"duration_ms"`
	Error          string    `json:"error,omitempty"`
}

func runBackupVerify(cmd *cobra.Command, _ []string) error {
	start := time.Now()
	file, _ := cmd.Flags().GetString("file")
	document, _ := cmd.Flags().GetString("document")
	expected, _ := cmd.Flags().GetInt64("expect-posted")
	asJSON, _ := cmd.Flags().GetBool("json")
	report := backupVerifyReport{File: file, Document: document, ExpectedPosted: expected, CheckedAt: start.UTC()}

	var err error
	switch {
	case strings.TrimSpace(file) == "":
		err = errors.New("укажите файл резервной копии в --file")
	case !metadata.ValidIdent(document):
		err = fmt.Errorf("недопустимое имя документа: %q", document)
	case expected < 0:
		err = errors.New("--expect-posted должен быть неотрицательным")
	default:
		report.ActualPosted, err = countPostedInBackup(cmd.Context(), file, document)
		if err == nil && *report.ActualPosted != expected {
			err = fmt.Errorf("расхождение числа проведённых документов: ожидалось %d, получено %d", expected, *report.ActualPosted)
		}
	}
	report.DurationMS = time.Since(start).Milliseconds()
	report.OK = err == nil
	if err != nil {
		report.Error = err.Error()
	}
	out := cmd.OutOrStdout()
	switch {
	case asJSON:
		err = json.NewEncoder(out).Encode(report)
	case report.OK:
		_, err = fmt.Fprintf(out, "%s  %s  %s: восстановление проверено за %d мс, проведено %d (ожидалось %d)\n",
			report.CheckedAt.Format(time.RFC3339), file, document, report.DurationMS, *report.ActualPosted, expected)
	default:
		_, err = fmt.Fprintf(out, "%s  %s  %s: проверка не пройдена (%d мс): %s\n",
			report.CheckedAt.Format(time.RFC3339), file, document, report.DurationMS, report.Error)
	}
	if err != nil {
		return err
	}
	if !report.OK {
		return errSilentExit
	}
	return nil
}

func countPostedInBackup(ctx context.Context, source, document string) (_ *int64, err error) {
	dir, err := os.MkdirTemp("", "onebase-backup-verify-*")
	if err != nil {
		return nil, err
	}
	defer func() {
		if cleanupErr := os.RemoveAll(dir); cleanupErr != nil {
			err = errors.Join(err, fmt.Errorf("удаление временной базы: %w", cleanupErr))
		}
	}()
	image := filepath.Join(dir, "backup.db")
	if err := backup.StageStandaloneSQLite(ctx, source, image); err != nil {
		return nil, fmt.Errorf("чтение резервной копии: %w", err)
	}
	restored := filepath.Join(dir, "restored.db")
	if err := backup.RestoreSQLite(ctx, restored, image); err != nil {
		return nil, fmt.Errorf("пробное восстановление: %w", err)
	}
	db, err := openCLIStorageWithMode(ctx, "sqlite", restored, "", false, true)
	if err != nil {
		return nil, fmt.Errorf("открытие восстановленной базы: %w", err)
	}
	defer db.Close()
	var count int64
	// The caller validates the logical identifier; TableName also handles
	// Unicode normalization of case and the physical name of long identifiers.
	query := fmt.Sprintf(`SELECT COUNT(*) FROM "%s" WHERE posted = 1`, metadata.TableName(document))
	if err := db.QueryRow(ctx, query).Scan(&count); err != nil {
		return nil, fmt.Errorf("подсчёт проведённых документов %q: %w", document, err)
	}
	return &count, nil
}
