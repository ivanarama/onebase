package backup

import (
	"context"

	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/ivantit66/onebase/internal/project"
	"github.com/ivantit66/onebase/internal/storage"
)

// Длина 62–63 байта не доказывает старую обрезку: современный SQLIdent
// имеет ту же длину. Отказ допускается только по точной прежней обрезке
// длинного имени из конфигурации архива; оставленные колонки сохраняются.
type legacyImportIdents struct {
	tables  map[string]bool
	columns map[string]map[string]bool
}

func loadLegacyImportIdents(ctx context.Context, db *storage.DB, source, dir string) (*legacyImportIdents, error) {
	proj, err := loadProjectForImport(ctx, db, source, dir)
	if err != nil {
		return nil, err
	}
	defer proj.Close()
	return projectLegacyImportIdents(proj), nil
}

func projectLegacyImportIdents(proj *project.Project) *legacyImportIdents {
	ids := &legacyImportIdents{tables: map[string]bool{}, columns: map[string]map[string]bool{}}
	add := func(table string, logicalCols []string) {
		if old := metadata.LegacySQLIdents(table); len(old) > 1 {
			ids.tables[old[1]] = true
		}
		cols := map[string]bool{}
		for _, col := range logicalCols {
			if old := metadata.LegacySQLIdents(col); len(old) > 1 {
				cols[old[1]] = true
			}
		}
		ids.columns[metadata.SQLIdent(table)] = cols
	}
	fields := func(fs []metadata.Field) []string {
		cols := make([]string, 0, len(fs))
		for _, f := range fs {
			cols = append(cols, metadata.LogicalColumnName(f))
		}
		return cols
	}
	for _, e := range proj.Entities {
		add(metadata.TableLogical(e.Name), fields(e.Fields))
		for _, tp := range e.TableParts {
			add(metadata.TablePartTableLogical(e.Name, tp.Name), fields(tp.Fields))
		}
	}
	for _, r := range proj.Registers {
		cols := append(append(fields(r.Dimensions), fields(r.Resources)...), fields(r.Attributes)...)
		add(metadata.RegisterTableLogical(r.Name), cols)
		if r.TotalsUsable() {
			add(metadata.RegisterTotalsTableLogical(r.Name), append(fields(r.Dimensions), fields(r.Resources)...))
		}
	}
	for _, r := range proj.InfoRegisters {
		add(metadata.InfoRegTableLogical(r.Name), append(fields(r.Dimensions), fields(r.Resources)...))
	}
	for _, r := range proj.AccountRegisters {
		add(metadata.AccountRegTableLogical(r.Name), fields(r.Resources))
		if r.TotalsUsable() {
			var cols []string
			for _, c := range fields(r.Resources) {
				cols = append(cols, c+"_дт", c+"_кт")
			}
			add(metadata.AccountRegTotalsTableLogical(r.Name), cols)
		}
	}
	return ids
}
