package metadata

import (
	"strings"
	"testing"
)

func TestValidateStagesRejectsDisconnectedCycle(t *testing.T) {
	entity := &Entity{
		Name: "Заявка",
		Kind: KindDocument,
		Fields: []Field{{
			Name: "Состояние", Type: FieldType("enum:СостояниеЗаявки"), EnumName: "СостояниеЗаявки",
		}},
		Stages: &Stages{
			Field:   "Состояние",
			Order:   []string{"Черновик", "ВРаботе", "Изолирован"},
			Enforce: StageEnforceStrict,
			Transitions: []StageTransition{
				{From: "Черновик", To: []string{"ВРаботе"}},
				{From: "Изолирован", To: []string{"Изолирован"}},
			},
		},
	}
	enums := []*Enum{{
		Name: "СостояниеЗаявки", Values: []string{"Черновик", "ВРаботе", "Изолирован"},
	}}

	err := Validate([]*Entity{entity}, enums)
	if err == nil || !strings.Contains(err.Error(), "недостижим") {
		t.Fatalf("изолированный цикл этапов должен быть отклонён как недостижимый, получено: %v", err)
	}
}
