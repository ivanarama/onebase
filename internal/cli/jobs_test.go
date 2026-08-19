package cli

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/spf13/cobra"

	"github.com/ivantit66/onebase/internal/storage"
)

// Регламентные задания переключаются из командной строки (#991).
//
// Тесты идут через RunE-функции команды (то, что вызывает cobra), а состояние
// проверяется отдельным подключением к базе — как у settings: иначе тест
// доказывал бы только то, что команда что-то подержала в памяти.

func jobsCLIFixture(t *testing.T) (*cobra.Command, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "jobs.db")
	cmd := &cobra.Command{}
	cmd.Flags().String("db", "", "")
	cmd.Flags().String("sqlite", "", "")
	if err := cmd.Flags().Set("sqlite", path); err != nil {
		t.Fatal(err)
	}
	return cmd, path
}

// jobDecisionOnDisk перечитывает решение отдельным подключением.
func jobDecisionOnDisk(t *testing.T, path, name string) (on, ok bool) {
	t.Helper()
	ctx := context.Background()
	db, err := storage.ConnectSQLite(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	on, ok, err = db.GetScheduledEnabled(ctx, name)
	if err != nil {
		t.Fatal(err)
	}
	return on, ok
}

func TestJobsEnable_ПишетРешениеВБазу(t *testing.T) {
	cmd, path := jobsCLIFixture(t)

	if err := jobsEnableCmd.RunE(cmd, []string{"ТелеграмПоллинг"}); err != nil {
		t.Fatalf("jobs enable: %v", err)
	}

	on, ok := jobDecisionOnDisk(t, path, "ТелеграмПоллинг")
	if !ok || !on {
		t.Fatalf("после enable: on=%v ok=%v, ожидалось true/true", on, ok)
	}
}

func TestJobsDisable_ВыключаетЗадание(t *testing.T) {
	cmd, path := jobsCLIFixture(t)

	if err := jobsEnableCmd.RunE(cmd, []string{"ОбменДанными"}); err != nil {
		t.Fatal(err)
	}
	if err := jobsDisableCmd.RunE(cmd, []string{"ОбменДанными"}); err != nil {
		t.Fatalf("jobs disable: %v", err)
	}

	if on, ok := jobDecisionOnDisk(t, path, "ОбменДанными"); !ok || on {
		t.Fatalf("после disable: on=%v ok=%v, ожидалось false/true", on, ok)
	}
}

func TestJobsReset_УбираетРешение(t *testing.T) {
	cmd, path := jobsCLIFixture(t)

	if err := jobsDisableCmd.RunE(cmd, []string{"Автобэкап"}); err != nil {
		t.Fatal(err)
	}
	if err := jobsResetCmd.RunE(cmd, []string{"Автобэкап"}); err != nil {
		t.Fatalf("jobs reset: %v", err)
	}

	if _, ok := jobDecisionOnDisk(t, path, "Автобэкап"); ok {
		t.Fatal("после reset решение осталось в базе")
	}
}

// Reset для задания без решения — не ошибка: «вернуть как в конфигурации»
// и без того выполнено.
func TestJobsReset_БезРешенияНеОшибка(t *testing.T) {
	cmd, _ := jobsCLIFixture(t)
	if err := jobsResetCmd.RunE(cmd, []string{"ТакогоНет"}); err != nil {
		t.Fatalf("reset без решения: %v", err)
	}
}

func TestJobs_ПустоеИмяОтклоняется(t *testing.T) {
	cmd, _ := jobsCLIFixture(t)
	for _, run := range []func(*cobra.Command, []string) error{
		jobsEnableCmd.RunE, jobsDisableCmd.RunE, jobsResetCmd.RunE, jobsStatusCmd.RunE,
	} {
		if err := run(cmd, []string{"   "}); err == nil {
			t.Error("пустое имя принято")
		}
	}
}
