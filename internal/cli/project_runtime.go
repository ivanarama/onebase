package cli

import (
	"fmt"

	"github.com/ivantit66/onebase/internal/api"
	"github.com/ivantit66/onebase/internal/project"
	"github.com/ivantit66/onebase/internal/runtime"
	"github.com/ivantit66/onebase/internal/scheduler"
)

// registryFromProject assembles a complete, unpublished registry generation.
// Building away from the live registry prevents readers from observing a mix
// of old and new metadata while the individual categories are loaded.
func registryFromProject(proj *project.Project) *runtime.Registry {
	next := runtime.NewRegistry()
	next.Load(runtime.LoadOptions{
		Entities:        proj.Entities,
		Programs:        proj.Programs,
		ManagerPrograms: proj.ManagerPrograms,
		ServicePrograms: proj.ServicePrograms,
		PagePrograms:    proj.PagePrograms,
		Registers:       proj.Registers,
		InfoRegs:        proj.InfoRegisters,
		Enums:           proj.Enums,
		Constants:       proj.Constants,
		Reports:         proj.Reports,
		PrintForms:      proj.PrintForms,
	})
	next.LoadDSLPrintForms(proj.DSLPrintForms)
	next.LoadLayoutForms(proj.LayoutForms)
	next.LoadModules(proj.Modules)
	next.LoadProcessors(proj.Processors)
	next.LoadHTTPServices(proj.HTTPServices)
	next.LoadPages(proj.Pages)
	next.LoadExchangePlans(proj.ExchangePlans)
	next.LoadIntakes(proj.Intakes)
	next.LoadSubsystems(proj.Subsystems)
	next.LoadJournals(proj.Journals)
	next.LoadAccountRegisters(proj.AccountRegisters, proj.ChartsOfAccounts)
	next.LoadWidgets(proj.Widgets)
	next.LoadHomePage(proj.HomePage)
	return next
}

// reloadProjectRuntime validates and replaces the reloadable runtime surface:
// project metadata/DSL plus project scheduled jobs. Static app.yaml settings,
// locales, roles and database migrations are deliberately handled by callers.
func reloadProjectRuntime(reg *runtime.Registry, sched *scheduler.Scheduler, srv *api.Server, proj *project.Project) error {
	next := registryFromProject(proj)
	if sched != nil {
		if err := sched.ValidateProjectJobs(proj.ScheduledJobs); err != nil {
			return fmt.Errorf("validate scheduled jobs: %w", err)
		}
		// Reload first: after successful validation its only expected runtime
		// failure is shutdown in progress, in which case the registry remains
		// on the previous generation.
		if err := sched.ReloadProjectJobs(proj.ScheduledJobs); err != nil {
			return fmt.Errorf("reload scheduled jobs: %w", err)
		}
	}
	reg.ReplaceProjectFrom(next)
	if srv != nil {
		srv.InvalidateWidgetCache()
		// WS-шлюзы приёмки: изменённые соединения пересоздаются, нетронутые
		// живут дальше (правка обработчика переподключения не требует).
		srv.ResyncWSIntakes()
	}
	return nil
}
