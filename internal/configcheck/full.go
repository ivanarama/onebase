package configcheck

import (
	"context"
	"os"
	"path/filepath"

	"github.com/ivantit66/onebase/internal/auth"
	"github.com/ivantit66/onebase/internal/project"
	"github.com/ivantit66/onebase/internal/storage"
)

// Options tunes the complete configuration validation.
type Options struct {
	// Lint enables advisory checks that report warnings only. These checks are
	// intentionally separate from the default validation path so `onebase check`
	// remains a strict correctness gate while `onebase check --lint` can surface
	// maintainability smells.
	Lint bool
}

// RunFull performs the complete configuration validation used by both
// `onebase check` and the web configurator "check all" endpoint.
func RunFull(dir string) Result {
	return RunFullWithOptions(dir, Options{})
}

// RunFullWithOptions is RunFull plus opt-in advisory lint warnings.
func RunFullWithOptions(dir string, opts Options) Result {
	dirIssues, dirWarnings := CheckDir(dir)
	issues := dirIssues
	warnings := dirWarnings
	if opts.Lint {
		warnings = append(warnings, CheckLintYAML(dir)...)
	}
	appCfg, appCfgErr := project.LoadConfig(dir)
	if appCfgErr != nil && !AlreadyReported(issues, appCfgErr.Error()) {
		issues = append(issues, Issue{Message: "config/app.yaml: " + appCfgErr.Error()})
	}

	if proj, err := project.Load(dir); err == nil {
		strictLexicalScope := appCfgErr == nil && appCfg != nil && appCfg.DSL != nil && appCfg.DSL.StrictLexicalScope
		issues = append(issues, CheckQueries(proj)...)
		issues = append(issues, CheckReportComposition(proj)...)
		issues = append(issues, CheckJournalConditional(proj)...)
		issues = append(issues, CheckFormConditional(proj)...)
		issues = append(issues, CheckReportOutputFormat(proj)...)
		roles, rolesErr := auth.LoadRolesYAML(filepath.Join(dir, "roles"))
		if rolesErr != nil && !AlreadyReported(issues, rolesErr.Error()) {
			issues = append(issues, Issue{Message: "roles: " + rolesErr.Error()})
		}
		issues = append(issues, CheckCrossRefs(proj, roles)...)
		warnings = append(warnings, CheckLayoutWarnings(proj)...)
		warnings = append(warnings, CheckFormFieldFormat(proj)...)
		issues = append(issues, CheckHTTPServices(proj)...)
		issues = append(issues, CheckExchangePlans(proj)...)
		issues = append(issues, CheckPages(proj)...)
		issues = append(issues, CheckNameCollisions(proj)...)
		if strictLexicalScope {
			issues = append(issues, CheckStrictLexicalScope(dir, proj)...)
		}
		if opts.Lint {
			lintWarnings := CheckLintProject(dir, proj, roles)
			if strictLexicalScope {
				lintWarnings = excludeIssueCode(lintWarnings, "dsl.cross-scope-read")
			}
			warnings = append(warnings, lintWarnings...)
		}
		if db, closeDB, derr := BuildSchemaDB(proj); derr == nil {
			validate := func(sql string) error { return db.ValidateQuery(context.Background(), sql) }
			issues = append(issues, CheckQueriesExecutable(proj, validate)...)
			issues = append(issues, CheckModuleQueries(proj, validate)...)
			closeDB()
		} else {
			issues = append(issues, CheckModuleQueries(proj, nil)...)
		}
		proj.Close()
	} else if !AlreadyReported(issues, err.Error()) {
		issues = append(issues, Issue{Message: "Project.Load: " + err.Error()})
	}

	return NewResult(issues, warnings)
}
func excludeIssueCode(in []Issue, code string) []Issue {
	if len(in) == 0 {
		return in
	}
	out := in[:0]
	for _, is := range in {
		if is.Code != code {
			out = append(out, is)
		}
	}
	return out
}

// BuildSchemaDB creates a temporary SQLite database with the schema described by
// project metadata so executable query validation can PREPARE generated SQL.
func BuildSchemaDB(proj *project.Project) (*storage.DB, func(), error) {
	ctx := context.Background()
	f, err := os.CreateTemp("", "onebase_check_*.db")
	if err != nil {
		return nil, nil, err
	}
	path := f.Name()
	f.Close()
	db, err := storage.ConnectSQLite(ctx, path)
	if err != nil {
		os.Remove(path)
		return nil, nil, err
	}
	closer := func() { db.Close(); os.Remove(path) }
	steps := []func() error{
		func() error { return db.Migrate(ctx, proj.Entities) },
		func() error { return db.MigrateRegisters(ctx, proj.Registers) },
		func() error { return db.MigrateInfoRegisters(ctx, proj.InfoRegisters) },
		func() error { return db.MigrateConstants(ctx, proj.Constants) },
		func() error { return db.MigrateAccountRegisters(ctx, proj.AccountRegisters) },
		func() error { return db.EnsureAccountsTable(ctx) },
		func() error { return db.SyncAccounts(ctx, proj.ChartsOfAccounts) },
	}
	for _, step := range steps {
		if err := step(); err != nil {
			closer()
			return nil, nil, err
		}
	}
	return db, closer, nil
}
