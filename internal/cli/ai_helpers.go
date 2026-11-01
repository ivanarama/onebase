package cli

import (
	"github.com/ivantit66/onebase/internal/aicontext"
	"github.com/ivantit66/onebase/internal/project"
	"github.com/ivantit66/onebase/internal/runtime"
)

func projectAIContext(proj *project.Project) string {
	reports := make([]aicontext.NamedTitle, 0, len(proj.Reports))
	for _, rp := range proj.Reports {
		reports = append(reports, aicontext.NamedTitle{Name: rp.Name, Title: rp.Title})
	}
	processors := make([]aicontext.NamedTitle, 0, len(proj.Processors))
	for _, p := range proj.Processors {
		processors = append(processors, aicontext.NamedTitle{Name: p.Name, Title: p.Title})
	}
	return aicontext.SchemaText(aicontext.Input{
		Entities:         proj.Entities,
		Registers:        proj.Registers,
		InfoRegisters:    proj.InfoRegisters,
		AccountRegisters: proj.AccountRegisters,
		ChartsOfAccounts: proj.ChartsOfAccounts,
		Enums:            proj.Enums,
		Constants:        proj.Constants,
		Reports:          reports,
		Processors:       processors,
		Journals:         proj.Journals,
		Subsystems:       proj.Subsystems,
	})
}

func buildRuntimeRegistry(proj *project.Project) *runtime.Registry {
	reg := runtime.NewRegistry()
	reg.LoadHelp(proj.Help)
	reg.Load(runtime.LoadOptions{
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
	reg.LoadModules(proj.Modules)
	reg.LoadProcessors(proj.Processors)
	reg.LoadJournals(proj.Journals)
	reg.LoadSubsystems(proj.Subsystems)
	reg.LoadAccountRegisters(proj.AccountRegisters, proj.ChartsOfAccounts)
	return reg
}
