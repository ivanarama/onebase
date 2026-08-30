package selfupdate

// Жёсткий режим подписи (#1195) живёт не в коде, а в ldflags релизного
// workflow: сам по себе `RequireSignature` — пустая переменная, и все тесты
// поведения в signature_test.go зелены при любом её значении. Ровно так дефект
// и получился: механизм написан, покрыт тестами и не включён, а мягкость
// означала, что подпись обходится её отсутствием.
//
// Поэтому здесь проверяется НАСТОЯЩИЙ файл workflow, а не копия его строк: если
// флаг однажды уедет из сборки, отказ должен быть виден на CI, а не через
// полгода на подменённом релизе. Тот же повод, что у #611, только точка
// подмены — не приватная функция, а конвейер выпуска.

import (
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"strings"
	"testing"
)

const releaseWorkflowPath = "../../.github/workflows/release.yml"

// pkgProbe нужен ровно затем, чтобы спросить у компилятора настоящий путь
// пакета. Линковщик про опечатку в `-X` молчит: символ не найден — флаг просто
// не применён, сборка зелёная, режим мягкий. А путь тут посторонний для
// репозитория (модуль `ivantit66/onebase` при репозитории `ivanarama/onebase`),
// и переименование модуля выключило бы подпись беззвучно.
type pkgProbe struct{}

func selfupdatePkgPath() string { return reflect.TypeOf(pkgProbe{}).PkgPath() }

var (
	// Строка сборки, вшивающая открытый ключ, — признак «здесь собирается
	// бинарь, который поедет пользователю».
	rePublicKeyFlag = regexp.MustCompile(`selfupdate\.PublicKey=`)
	// Значение забирается вместе с флагом: проверять надо не наличие строки, а
	// то, что подставленное значение движок считает жёстким режимом.
	reRequireFlag = regexp.MustCompile(`selfupdate\.RequireSignature=([^\s"']*)`)
)

// workflowStep — шаг workflow: заголовок `- name:` и его строки до следующего.
type workflowStep struct {
	name  string
	lines []string
}

var reStepName = regexp.MustCompile(`^\s*-\s+name:\s*(.+?)\s*$`)

// releaseBuildSteps возвращает шаги релизного workflow, которые собирают бинарь
// с вшитым открытым ключом. YAML здесь не разбирается: нужен не смысл файла, а
// соседство двух ldflags в одном шаге, и деление по `- name:` для этого точнее
// — оно ловит и случай «флаг есть, но в другом шаге».
func releaseBuildSteps(t *testing.T) []workflowStep {
	t.Helper()
	raw, err := os.ReadFile(filepath.FromSlash(releaseWorkflowPath))
	if err != nil {
		t.Fatalf("релизный workflow не читается (%s): %v", releaseWorkflowPath, err)
	}

	var steps []workflowStep
	cur := workflowStep{name: "(до первого шага)"}
	for _, line := range strings.Split(string(raw), "\n") {
		if m := reStepName.FindStringSubmatch(line); m != nil {
			steps = append(steps, cur)
			cur = workflowStep{name: m[1]}
			continue
		}
		// Комментарии выброшены намеренно: в шаге сборки разобрано, почему
		// пустой ключ выключает проверку, и упоминание флага в тексте не
		// должно засчитываться за его подстановку.
		if strings.HasPrefix(strings.TrimSpace(line), "#") {
			continue
		}
		cur.lines = append(cur.lines, line)
	}
	steps = append(steps, cur)

	var build []workflowStep
	for _, s := range steps {
		if rePublicKeyFlag.MatchString(strings.Join(s.lines, "\n")) {
			build = append(build, s)
		}
	}
	return build
}

// Каждая выпускаемая сборка обязана нести жёсткий режим. Проверка идёт через
// SignatureEnforced — тот же предикат, что читает боевой путь: подставь workflow
// «yes» или «0», и тест скажет то же, что скажет обновление у пользователя.
func TestРелизныйWorkflow_ЖёсткийРежимВшитВКаждуюСборку(t *testing.T) {
	steps := releaseBuildSteps(t)

	// Сборок две — CLI-бинарь по матрице и GUI-бинарь Windows. Меньше значит,
	// что шаг переименован или уехал, и тест перестал смотреть на сборку
	// вместо того, чтобы упасть.
	if len(steps) < 2 {
		t.Fatalf("в %s найдено %d шагов сборки с вшитым ключом, ожидалось не меньше 2 "+
			"(CLI и GUI) — проверьте, не переехали ли шаги", releaseWorkflowPath, len(steps))
	}

	wantSymbol := selfupdatePkgPath() + ".RequireSignature="
	for _, s := range steps {
		body := strings.Join(s.lines, "\n")
		m := reRequireFlag.FindStringSubmatch(body)
		if m == nil {
			t.Errorf("шаг %q вшивает открытый ключ, но не вшивает RequireSignature: "+
				"такая сборка примет релиз без подписи (#1195)", s.name)
			continue
		}
		// Путь символа целиком, а не хвост `selfupdate.RequireSignature`:
		// линковщик ненайденный символ пропускает молча, и сборка с опечаткой
		// в пути выйдет мягкой, ничем этого не показав.
		if !strings.Contains(body, wantSymbol) {
			t.Errorf("шаг %q подставляет флаг мимо настоящего символа: ожидалось -X %s…, "+
				"иначе линковщик молча его проигнорирует", s.name, wantSymbol)
		}
		withKeys(t, "", m[1])
		if !SignatureEnforced() {
			t.Errorf("шаг %q подставляет RequireSignature=%q, а движок считает это мягким режимом",
				s.name, m[1])
		}
	}
}

// Открытый ключ приходит из переменной репозитория, и пустой её значение
// выключает проверку целиком (#967) — тогда жёсткий режим не сработает, потому
// что до него дело не дойдёт. Флаг ключа обязан остаться на месте: без него
// жёсткий режим превращается в украшение.
func TestРелизныйWorkflow_ОткрытыйКлючПриходитИзПеременной(t *testing.T) {
	for _, s := range releaseBuildSteps(t) {
		body := strings.Join(s.lines, "\n")
		if !strings.Contains(body, "RELEASE_PUBLIC_KEY") {
			t.Errorf("шаг %q вшивает ключ мимо переменной RELEASE_PUBLIC_KEY", s.name)
		}
	}
}
