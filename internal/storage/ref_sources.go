package storage

import "github.com/ivantit66/onebase/internal/metadata"

// referenceSource is the shared physical inventory for deletion and navigation.
// It never contains identifiers supplied by a request.
type referenceSource struct {
	kind, name, part, table, column, label string
	field                                  metadata.Field
	entity, predicateEntity                *metadata.Entity
	recorder, recorderType                 string
	navigable                              bool
}

func referenceSources(target string, src RefSources) []referenceSource {
	if src == nil {
		return nil
	}
	var out []referenceSource
	add := func(s referenceSource, fields []metadata.Field, numbered bool) {
		for i, f := range fields {
			if f.RefEntity != target {
				continue
			}
			s.field = f
			s.column = metadata.ColumnName(f)
			if numbered {
				s.column = metadata.SubcontoColumn(i + 1)
			}
			out = append(out, s)
		}
	}
	for _, e := range src.Entities() {
		s := referenceSource{
			kind: string(e.Kind), name: e.Name, table: metadata.TableName(e.Name), label: e.Name,
			entity: e, predicateEntity: e,
			navigable: e.Kind == metadata.KindCatalog || e.Kind == metadata.KindDocument,
		}
		add(s, e.Fields, false)
		for _, tp := range e.TableParts {
			s.part = tp.Name
			s.table = metadata.TablePartTableName(e.Name, tp.Name)
			s.label = e.Name + "." + tp.Name
			add(s, tp.Fields, false)
		}
	}
	for _, r := range src.Registers() {
		s := referenceSource{
			kind: "register", name: r.Name, table: metadata.RegisterTableName(r.Name), label: "РегистрНакопления." + r.Name,
			predicateEntity: RegisterPredicateEntity(r),
			recorder:        "recorder", recorderType: "recorder_type", navigable: true,
		}
		add(s, r.Dimensions, false)
		add(s, r.Resources, false)
		add(s, r.Attributes, false)
	}
	for _, r := range src.InfoRegisters() {
		s := referenceSource{
			kind: "inforeg", name: r.Name, table: metadata.InfoRegTableName(r.Name), label: "РегистрСведений." + r.Name,
			predicateEntity: InfoRegisterPredicateEntity(r),
			recorder:        "recorder", recorderType: "recorder_type", navigable: r.Recorder,
		}
		add(s, r.Dimensions, false)
		add(s, r.Resources, false)
	}
	for _, r := range src.AccountRegisters() {
		s := referenceSource{
			kind: "accountreg", name: r.Name, table: metadata.AccountRegTableName(r.Name), label: "РегистрБухгалтерии." + r.Name,
			predicateEntity: AccountRegisterPredicateEntity(r),
			recorder:        "регистратор", recorderType: "регистратор_тип", navigable: true,
		}
		add(s, r.Resources, false)
		add(s, r.Subconto, true)
	}
	return out
}
