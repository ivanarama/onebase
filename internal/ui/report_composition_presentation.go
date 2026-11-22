package ui

import (
	"fmt"
	"strings"

	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/ivantit66/onebase/internal/query"
	"github.com/ivantit66/onebase/internal/report"
	"github.com/ivantit66/onebase/internal/storage"
)

// Presentation belongs to one request, never to persisted report metadata or
// a user's saved grouping names. SQL and grouping identity keep their names.
type reportCompositionPresentation struct {
	groupLabels []string
	grandLabel  string
}

func (p reportCompositionPresentation) groupHeader(spec *report.Composition) string {
	if len(p.groupLabels) == len(spec.Groupings) {
		return strings.Join(p.groupLabels, " / ")
	}
	return strings.Join(spec.Groupings, " / ")
}

func (p reportCompositionPresentation) grandTitle() string {
	if p.grandLabel != "" {
		return p.grandLabel
	}
	return "Итого"
}

func (s *Server) compositionPresentation(rep *report.Report, spec *report.Composition, compiled *query.Result, lang string) reportCompositionPresentation {
	p := reportCompositionPresentation{grandLabel: s.tr(lang, "Итого")}
	for _, name := range spec.Groupings {
		label := name
		if field := s.reportProjectionMetadata(compiled, name); field != nil {
			label = field.DisplayName(lang)
		} else {
			for _, param := range rep.Params {
				if strings.EqualFold(param.Name, name) {
					label = param.DisplayLabel(lang)
					break
				}
			}
		}
		p.groupLabels = append(p.groupLabels, label)
	}
	return p
}

// Resolve only a proven direct projection. Expressions, unions, ambiguous
// joins and unknown provenance fall back to the result field name.
func (s *Server) reportProjectionMetadata(compiled *query.Result, output string) *metadata.Field {
	if compiled == nil || !compiled.Projection.Simple {
		return nil
	}
	var projection *query.ProjectionColumn
	for i := range compiled.Projection.Columns {
		column := &compiled.Projection.Columns[i]
		if strings.EqualFold(column.Output, output) && len(column.Path) > 0 && !column.Star {
			if projection != nil {
				return nil
			}
			projection = column
		}
	}
	if projection == nil {
		return nil
	}
	qualified := false
	for _, source := range compiled.SourceOccurrences {
		if len(projection.Path) > 1 && (strings.EqualFold(projection.Path[0], source.Qualifier) || strings.EqualFold(projection.Path[0], source.Name)) {
			qualified = true
		}
	}
	var result *metadata.Field
	for _, source := range compiled.SourceOccurrences {
		path := projection.Path
		if len(path) > 1 && (strings.EqualFold(path[0], source.Qualifier) || strings.EqualFold(path[0], source.Name)) {
			path = path[1:]
		} else if qualified {
			continue
		} else if len(path) > 2 && strings.EqualFold(path[1], source.Name) {
			path = path[2:]
		}
		entity := s.reportPresentationSourceMeta(source)
		if entity == nil {
			return nil
		}
		var found *metadata.Field
		for index, part := range path {
			found = nil
			if entity == nil {
				break
			}
			for i := range entity.Fields {
				if strings.EqualFold(entity.Fields[i].Name, part) {
					copy := entity.Fields[i]
					found = &copy
					break
				}
			}
			if found == nil {
				break
			}
			if index+1 == len(path) {
				break
			}
			if index+2 == len(path) && strings.EqualFold(path[index+1], "Ссылка") && found.RefEntity != "" {
				break
			}
			entity = s.reg.GetEntity(found.RefEntity)
		}
		if found != nil {
			if result != nil {
				return nil
			}
			result = found
		}
	}
	return result
}

// Display metadata obeys the compiler's source kind. This request-local lookup
// does not change shared access policy; unresolved provenance keeps the alias.
func (s *Server) reportPresentationSourceMeta(source query.SourceOccurrence) *metadata.Entity {
	if source.Virtual && source.TablePart == "" {
		return nil // Output names alone do not prove a derived column's field.
	}
	var entity *metadata.Entity
	switch strings.ToLower(source.Kind) {
	case "catalog", "document":
		if e := s.reg.GetEntity(source.Name); e != nil && strings.EqualFold(string(e.Kind), source.Kind) {
			entity = e
		}
	case "inforeg":
		entity = storage.InfoRegisterPredicateEntity(s.reg.GetInfoRegister(source.Name))
	case "register":
		reg, account := s.reg.GetRegister(source.Name), s.reg.GetAccountRegister(source.Name)
		if reg != nil && account != nil {
			return nil // The permission kind does not distinguish these namespaces.
		}
		if reg != nil {
			entity = storage.RegisterPredicateEntity(reg)
		} else if account != nil {
			entity = storage.AccountRegisterPredicateEntity(account)
		}
	}
	if entity == nil || source.TablePart == "" {
		return entity
	}
	for i := range entity.TableParts {
		if strings.EqualFold(entity.TableParts[i].Name, source.TablePart) {
			return metadata.TablePartQueryEntity(entity, &entity.TableParts[i], entity.Name)
		}
	}
	return nil
}

// Groups fade from the same pale shade for screen and exports; details have
// no automatic fill. Explicit safe cell styles win.
func composedGroupFill(level, count int) string {
	if level < 0 || count < 1 || level >= count {
		return ""
	}
	numerator, denominator := count-level, count
	return fmt.Sprintf("#%02x%02x%02x", 255-15*numerator/denominator, 255-16*numerator/denominator, 255-43*numerator/denominator)
}

func composedGroupDarkFill(level, count int) string {
	if level < 0 || count < 1 || level >= count {
		return ""
	}
	numerator, denominator := count-level, count
	return fmt.Sprintf("#%02x%02x%02x", 15+36*numerator/denominator, 23+42*numerator/denominator, 42+43*numerator/denominator)
}
