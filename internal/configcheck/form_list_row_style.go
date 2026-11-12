package configcheck

import (
	"fmt"
	"strings"

	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/ivantit66/onebase/internal/project"
	"github.com/ivantit66/onebase/internal/widget"
)

// CheckFormListRowStyle проверяет row_style формы списка: только kind: list;
// у правила есть field и что задать (background и/или bold); цвета правил —
// #RGB/#RRGGBB; каждый путь проходит по реквизитам сущности и ссылкам до
// конечного реквизита. В рантайме ошибочный путь просто не даёт оформления —
// опечатка осталась бы незамеченной.
func CheckFormListRowStyle(proj *project.Project) []Issue {
	var issues []Issue
	byName := map[string]*metadata.Entity{}
	for _, e := range proj.Entities {
		if e != nil {
			byName[strings.ToLower(e.Name)] = e
		}
	}
	for _, ent := range proj.Entities {
		if ent == nil {
			continue
		}
		for _, form := range ent.Forms {
			if form == nil || form.ListRowStyle == nil {
				continue
			}
			add := func(msg string) {
				issues = append(issues, Issue{
					File: formFileLabel(ent, form), Object: form.Name,
					Kind: "Управляемая форма (row_style)", Code: "form.row-style", Message: msg,
				})
			}
			if !strings.EqualFold(form.Kind, "list") {
				add("row_style поддерживается только у формы списка (kind: list)")
				continue
			}
			rs := form.ListRowStyle
			if len(rs.Rules) == 0 && rs.BackgroundFrom == "" && rs.BoldFrom == "" {
				add("row_style пуст: задайте rules, background_from или bold_from")
			}
			checkPath := func(where, path string) {
				if err := listRowStylePath(ent, byName, path); err != nil {
					add(fmt.Sprintf("%s: %v", where, err))
				}
			}
			for i, rule := range rs.Rules {
				where := fmt.Sprintf("rules[%d]", i)
				if strings.TrimSpace(rule.Field) == "" {
					add(where + ": не задан field")
				} else {
					checkPath(where+".field", rule.Field)
				}
				if rule.Background == "" && !rule.Bold {
					add(where + ": правило ничего не оформляет — задайте background или bold")
				}
				if rule.Background != "" && widget.NormalizeRowColor(rule.Background) == "" {
					add(fmt.Sprintf("%s.background: %q — не цвет #RGB или #RRGGBB", where, rule.Background))
				}
			}
			if rs.BackgroundFrom != "" {
				checkPath("background_from", rs.BackgroundFrom)
			}
			if rs.BoldFrom != "" {
				checkPath("bold_from", rs.BoldFrom)
			}
		}
	}
	return issues
}

// listRowStylePath проверяет путь «Реквизит[.Реквизит…]»: каждый промежуточный
// реквизит — ссылка на сущность конфигурации, конечный — реквизит её шапки.
func listRowStylePath(ent *metadata.Entity, byName map[string]*metadata.Entity, path string) error {
	parts := strings.Split(strings.TrimSpace(path), ".")
	cur := ent
	for i, part := range parts {
		var field *metadata.Field
		for j := range cur.Fields {
			if strings.EqualFold(cur.Fields[j].Name, part) {
				field = &cur.Fields[j]
				break
			}
		}
		if field == nil {
			return fmt.Errorf("в %q нет реквизита %q (%s)", cur.Name, part, path)
		}
		if i == len(parts)-1 {
			return nil
		}
		next := byName[strings.ToLower(field.RefEntity)]
		if field.RefEntity == "" || next == nil {
			return fmt.Errorf("%q в %q — не ссылка на справочник или документ (%s)", part, cur.Name, path)
		}
		cur = next
	}
	return nil
}
