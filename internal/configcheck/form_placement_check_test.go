package configcheck_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ivantit66/onebase/internal/configcheck"
	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/ivantit66/onebase/internal/processor"
	"github.com/ivantit66/onebase/internal/project"
)

// placementProject собирает временный проект с одной сущностью и заданными
// файлами форм (пути относительно forms/).
func placementProject(t *testing.T, formFiles ...string) (string, *project.Project) {
	t.Helper()
	dir := t.TempDir()
	for _, rel := range formFiles {
		full := filepath.Join(dir, "forms", filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte("schema: onebase.form/v1\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	proj := &project.Project{Entities: []*metadata.Entity{
		{Name: "РеализацияТоваров", Kind: metadata.KindDocument},
	}}
	return dir, proj
}

func codesOf(warns []configcheck.Issue) []string {
	out := []string{}
	for _, w := range warns {
		out = append(out, w.File)
	}
	return out
}

// Файл прямо в forms/ не загружается — предупреждение.
func TestCheckFormPlacement_FlatFileWarns(t *testing.T) {
	dir, proj := placementProject(t, "РеализацияТоваров.form.yaml")
	warns := configcheck.CheckFormPlacement(dir, proj)
	if len(warns) != 1 {
		t.Fatalf("ожидалось 1 предупреждение, получено %d: %v", len(warns), codesOf(warns))
	}
	if warns[0].Code != "form.not-loaded" {
		t.Errorf("код = %q", warns[0].Code)
	}
	if !strings.Contains(warns[0].Message, "НЕ загружается") {
		t.Errorf("сообщение не объясняет суть: %q", warns[0].Message)
	}
}

// Файл в каталоге, совпадающем с именем сущности в нижнем регистре, — тишина.
func TestCheckFormPlacement_CorrectDirSilent(t *testing.T) {
	dir, proj := placementProject(t, "реализациятоваров/объекта.form.yaml")
	if warns := configcheck.CheckFormPlacement(dir, proj); len(warns) != 0 {
		t.Fatalf("ложное срабатывание: %v", codesOf(warns))
	}
}

// Каталог назван не как сущность — предупреждение с подсказкой.
func TestCheckFormPlacement_UnknownDirWarns(t *testing.T) {
	dir, proj := placementProject(t, "накладная/объекта.form.yaml")
	warns := configcheck.CheckFormPlacement(dir, proj)
	if len(warns) != 1 {
		t.Fatalf("ожидалось 1 предупреждение, получено %d", len(warns))
	}
	if !strings.Contains(warns[0].Message, "не соответствует ни одной сущности") {
		t.Errorf("сообщение: %q", warns[0].Message)
	}
}

// Регистр каталога важен: загрузчик ищет строго нижний регистр.
func TestCheckFormPlacement_WrongCaseWarnsWithHint(t *testing.T) {
	dir, proj := placementProject(t, "РеализацияТоваров/объекта.form.yaml")
	warns := configcheck.CheckFormPlacement(dir, proj)
	if len(warns) != 1 {
		t.Fatalf("ожидалось 1 предупреждение, получено %d", len(warns))
	}
	if !strings.Contains(warns[0].SuggestedFix, "реализациятоваров") {
		t.Errorf("подсказка не называет верный каталог: %q", warns[0].SuggestedFix)
	}
}

// Вложенность глубже одного уровня тоже не читается.
func TestCheckFormPlacement_NestedWarns(t *testing.T) {
	dir, proj := placementProject(t, "реализациятоваров/вложено/объекта.form.yaml")
	warns := configcheck.CheckFormPlacement(dir, proj)
	if len(warns) != 1 {
		t.Fatalf("ожидалось 1 предупреждение, получено %d", len(warns))
	}
	if !strings.Contains(warns[0].Message, "вложенном каталоге") {
		t.Errorf("сообщение: %q", warns[0].Message)
	}
}

// Нет каталога forms/ — проверка молчит и не падает.
func TestCheckFormPlacement_NoFormsDir(t *testing.T) {
	dir := t.TempDir()
	proj := &project.Project{Entities: []*metadata.Entity{{Name: "X", Kind: metadata.KindCatalog}}}
	if warns := configcheck.CheckFormPlacement(dir, proj); len(warns) != 0 {
		t.Fatalf("ожидалась тишина, получено %v", codesOf(warns))
	}
}

// Поставляемые конфигурации не должны содержать мёртвых форм: именно так
// 15 из 24 файлов годами не загружались.
func TestCheckFormPlacement_ShippedConfigsClean(t *testing.T) {
	for _, dir := range []string{
		"../../examples/trade", "../../examples/finance",
		"../../examples/crm", "../../examples/tasks", "../../examples/minimal",
	} {
		proj, err := project.Load(dir)
		if err != nil {
			t.Fatalf("%s: %v", dir, err)
		}
		warns := configcheck.CheckFormPlacement(dir, proj)
		proj.Close()
		if len(warns) != 0 {
			t.Errorf("%s: мёртвые формы %v", dir, codesOf(warns))
		}
	}
}

// У обработок тоже бывают управляемые формы: платформа их грузит и рендерит
// (см. ui.handleProcessorFormEvent), поэтому forms/<имя обработки>/ — валидное
// размещение. До исправления проверка знала только про сущности и объявляла
// каждую такую форму «НЕ загружается», хотя в браузере форма открывалась.
func TestCheckFormPlacement_ProcessorDirSilent(t *testing.T) {
	dir, proj := placementProject(t, "консользаданий/форма.form.yaml")
	proj.Processors = []*processor.Processor{{Name: "КонсольЗаданий"}}
	if warns := configcheck.CheckFormPlacement(dir, proj); len(warns) != 0 {
		t.Fatalf("ложное срабатывание на форме обработки: %v", codesOf(warns))
	}
}

// А каталог, не совпадающий ни с сущностью, ни с обработкой, по-прежнему ловится.
func TestCheckFormPlacement_UnknownDirWithProcessorsWarns(t *testing.T) {
	dir, proj := placementProject(t, "чужаяпапка/форма.form.yaml")
	proj.Processors = []*processor.Processor{{Name: "КонсольЗаданий"}}
	warns := configcheck.CheckFormPlacement(dir, proj)
	if len(warns) != 1 {
		t.Fatalf("ожидалось 1 предупреждение, получено %d: %v", len(warns), codesOf(warns))
	}
}
