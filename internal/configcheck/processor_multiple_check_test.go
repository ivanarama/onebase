package configcheck

import (
	"strings"
	"testing"

	"github.com/ivantit66/onebase/internal/processor"
	"github.com/ivantit66/onebase/internal/project"
)

// multiple действует только у type: binary — у другого типа check называет ключ.
func TestCheckProcessorMultipleFiles(t *testing.T) {
	proj := &project.Project{Processors: []*processor.Processor{{
		Name: "Сканы",
		Params: []processor.Param{
			{Name: "Пачка", Type: "binary", Multiple: true},
			{Name: "Текст", Type: "file", Multiple: true},
		},
	}}}
	warns := CheckProcessorMultipleFiles(proj)
	if len(warns) != 1 || warns[0].Code != "processor.multiple-type" || !strings.Contains(warns[0].Message, "Текст") {
		t.Fatalf("ожидалось одно предупреждение у параметра Текст: %+v", warns)
	}
}
