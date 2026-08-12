package configcheck

import (
	"strings"
	"testing"

	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/ivantit66/onebase/internal/project"
)

func stagesProject(s *metadata.Stages, enumValues []string) *project.Project {
	return &project.Project{
		Entities: []*metadata.Entity{{
			Name: "Заявка",
			Kind: metadata.KindDocument,
			Fields: []metadata.Field{
				{Name: "Состояние", Type: "enum:СостояниеЗаявки", EnumName: "СостояниеЗаявки"},
			},
			Stages: s,
		}},
		Enums: []*metadata.Enum{{Name: "СостояниеЗаявки", Values: enumValues}},
	}
}

func codes(issues []Issue) []string {
	out := make([]string, 0, len(issues))
	for _, is := range issues {
		out = append(out, is.Code)
	}
	return out
}

func hasCode(issues []Issue, code string) bool {
	for _, is := range issues {
		if is.Code == code {
			return true
		}
	}
	return false
}

// Значение перечисления мимо order: объект в таком состоянии выпадает из
// маршрута молча — ни гейт, ни отчёт про него не знают.
func TestCheckStagesWarnsAboutValueOutsideRoute(t *testing.T) {
	p := stagesProject(&metadata.Stages{
		Field: "Состояние",
		Order: []string{"Черновик", "Утверждена"},
		Transitions: []metadata.StageTransition{
			{From: "Черновик", To: []string{"Утверждена"}},
		},
		Enforce: metadata.StageEnforceStrict,
	}, []string{"Черновик", "Утверждена", "Отменена"})

	warns := CheckStages(p)
	if !hasCode(warns, "stages.value-outside-route") {
		t.Fatalf("предупреждения %v, ожидалось stages.value-outside-route", codes(warns))
	}
	for _, w := range warns {
		if w.Code == "stages.value-outside-route" && !strings.Contains(w.Message, "Отменена") {
			t.Fatalf("в тексте нет пропущенного значения: %s", w.Message)
		}
	}
}

// enforce: warn — маршрут объявлен, но не соблюдается. Это умолчание, поэтому
// предупреждение, а не ошибка: иначе включение блока в работающей конфигурации
// валило бы проверку.
func TestCheckStagesWarnsAboutEnforceWarn(t *testing.T) {
	s := &metadata.Stages{
		Field: "Состояние",
		Order: []string{"Черновик", "Утверждена"},
		Transitions: []metadata.StageTransition{
			{From: "Черновик", To: []string{"Утверждена"}},
		},
		Enforce: metadata.StageEnforceWarn,
	}
	warns := CheckStages(stagesProject(s, []string{"Черновик", "Утверждена"}))
	if !hasCode(warns, "stages.enforce-warn") {
		t.Fatalf("предупреждения %v, ожидалось stages.enforce-warn", codes(warns))
	}

	s.Enforce = metadata.StageEnforceStrict
	warns = CheckStages(stagesProject(s, []string{"Черновик", "Утверждена"}))
	if hasCode(warns, "stages.enforce-warn") {
		t.Fatalf("при strict предупреждения быть не должно: %v", codes(warns))
	}
}

// Сущность без блока `stages` не порождает ни одного предупреждения.
func TestCheckStagesSilentWithoutBlock(t *testing.T) {
	p := stagesProject(nil, []string{"Черновик"})
	if warns := CheckStages(p); len(warns) != 0 {
		t.Fatalf("без stages предупреждений быть не должно: %v", codes(warns))
	}
}
