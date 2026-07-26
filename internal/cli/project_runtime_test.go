package cli

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/ivantit66/onebase/internal/dsl/interpreter"
	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/ivantit66/onebase/internal/project"
	"github.com/ivantit66/onebase/internal/runtime"
	"github.com/ivantit66/onebase/internal/scheduler"
	"github.com/ivantit66/onebase/internal/storage"
)

func newReloadTestScheduler(t *testing.T, reg *runtime.Registry) *scheduler.Scheduler {
	t.Helper()
	ctx := context.Background()
	db, err := storage.ConnectSQLite(ctx, filepath.Join(t.TempDir(), "reload.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(db.Close)
	if err := db.EnsureScheduledRunsTable(ctx); err != nil {
		t.Fatal(err)
	}
	return scheduler.New(db, reg, interpreter.New())
}

func TestReloadProjectRuntimeRejectsInvalidScheduleBeforeRegistrySwap(t *testing.T) {
	reg := runtime.NewRegistry()
	reg.Load(runtime.LoadOptions{
		Entities: []*metadata.Entity{{Name: "Before", Kind: metadata.KindCatalog}},
	})
	sched := newReloadTestScheduler(t, reg)
	if err := sched.LoadJobs([]*metadata.ScheduledJob{{
		Name: "BeforeJob", Schedule: "@every 1h", Enabled: true,
	}}); err != nil {
		t.Fatal(err)
	}

	err := reloadProjectRuntime(reg, sched, nil, &project.Project{
		Entities: []*metadata.Entity{{Name: "After", Kind: metadata.KindDocument}},
		ScheduledJobs: []*metadata.ScheduledJob{{
			Name: "Broken", Schedule: "not-a-cron", Enabled: true,
		}},
	})
	if err == nil {
		t.Fatal("expected invalid schedule error")
	}
	if reg.GetEntity("Before") == nil || reg.GetEntity("After") != nil {
		t.Fatal("registry changed after rejected generation")
	}
	if sched.GetJob("BeforeJob") == nil || sched.GetJob("Broken") != nil {
		t.Fatal("scheduler changed after rejected generation")
	}
}

func TestReloadProjectRuntimePublishesCompleteGenerationAndKeepsNativeJobs(t *testing.T) {
	reg := runtime.NewRegistry()
	reg.Load(runtime.LoadOptions{
		Entities: []*metadata.Entity{{Name: "Before", Kind: metadata.KindCatalog}},
	})
	sched := newReloadTestScheduler(t, reg)
	if err := sched.LoadJobs([]*metadata.ScheduledJob{{
		Name: "BeforeJob", Schedule: "@every 1h", Enabled: true,
	}}); err != nil {
		t.Fatal(err)
	}
	if err := sched.RegisterGoJob("AutoBackup", "Backup", "@every 2h", func(context.Context) error {
		return nil
	}); err != nil {
		t.Fatal(err)
	}

	err := reloadProjectRuntime(reg, sched, nil, &project.Project{
		Entities: []*metadata.Entity{{Name: "After", Kind: metadata.KindDocument}},
		ScheduledJobs: []*metadata.ScheduledJob{{
			Name: "AfterJob", Schedule: "@every 3h", Enabled: true,
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if reg.GetEntity("Before") != nil || reg.GetEntity("After") == nil {
		t.Fatal("registry generation was not replaced")
	}
	if sched.GetJob("BeforeJob") != nil || sched.GetJob("AfterJob") == nil || sched.GetJob("AutoBackup") == nil {
		t.Fatal("scheduled job generation was not replaced correctly")
	}
}
