package launcher

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

// Манифест companions.yaml — «что запускать» поставляет комплект рабочего
// места: файл лежит рядом с исполняемым файлом лаунчера и задаёт «ключ →
// программа внутри каталога комплекта» (формат и правила пути — как в #1844).
// «Кому запускать» по-прежнему решает сервер ключами пользователя, а запись
// базы в ibases.yaml может задать свой путь для этой станции — он главнее
// манифеста (companions.go).
//
//	companions:
//	  callista:
//	    exec: companions/callista-operator/callista-operator.exe
//	    args: ["--minimized"]
//	    env:
//	      CALLISTA_OPERATOR_API_URL: https://cc.icecorp.ru:8445
//
// Программа, которую ставит собственный установщик в профиль пользователя
// (Callista 0.7.1+ — NSIS для текущего пользователя, дальше обновляется сама),
// задаётся путём от папки профиля: exec начинается с %LOCALAPPDATA%,
// %APPDATA%, %USERPROFILE%, %ProgramFiles% или %ProgramFiles(x86)%:
//
//	companions:
//	  callista:
//	    exec: '%LOCALAPPDATA%\Programs\@callistaoperator-desktop\callista-operator.exe'

const companionManifestName = "companions.yaml"

type companionManifestFile struct {
	Companions map[string]companionManifestSpec `yaml:"companions"`
}

type companionManifestSpec struct {
	// Exec — путь относительно каталога лаунчера; подкаталог разрешён:
	// Electron-дистрибутив приезжает каталогом (exe, resources, DLL).
	Exec string `yaml:"exec"`
	// Args — аргументы запуска, только отсюда.
	Args []string `yaml:"args"`
	// Env — переменные окружения программы поверх окружения лаунчера.
	Env map[string]string `yaml:"env"`
}

// launcherDir — каталог исполняемого файла лаунчера; тест подменяет.
var launcherDir = func() (string, error) {
	exe, err := os.Executable()
	if err != nil {
		return "", err
	}
	return filepath.Dir(exe), nil
}

// loadCompanionManifest читает companions.yaml из каталога dir. Нет файла —
// не ошибка: комплект без сопутствующих программ нормален. Хотя бы одна
// небезопасная или ошибочная запись отвергает манифест ЦЕЛИКОМ: частично
// применённое объявление хуже отказа.
func loadCompanionManifest(dir string) ([]Companion, error) {
	raw, err := os.ReadFile(filepath.Join(dir, companionManifestName)) //nolint:gosec // G304: путь собран из каталога самого лаунчера и константы
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}
	var m companionManifestFile
	if err := yaml.Unmarshal(raw, &m); err != nil {
		return nil, fmt.Errorf("%s: %w", companionManifestName, err)
	}
	out := make([]Companion, 0, len(m.Companions))
	for name, spec := range m.Companions {
		key, ok := NormalizeCompanionKey(name)
		if !ok {
			return nil, fmt.Errorf("%s: ключ «%s» — латиница в нижнем регистре, цифры, «_», «.», «-», до 32 символов", companionManifestName, name)
		}
		if err := validateCompanionExec(spec.Exec); err != nil {
			return nil, fmt.Errorf("%s: «%s»: %w", companionManifestName, name, err)
		}
		if err := validateCompanionEnv(spec.Env); err != nil {
			return nil, fmt.Errorf("%s: «%s»: %w", companionManifestName, name, err)
		}
		path, err := resolveCompanionExec(dir, spec.Exec)
		if err != nil {
			return nil, fmt.Errorf("%s: «%s»: %w", companionManifestName, name, err)
		}
		out = append(out, Companion{Key: key, Path: path, Args: spec.Args, Dir: filepath.Dir(path), Env: spec.Env})
	}
	return out, nil
}

// validateCompanionExec допускает только относительный путь внутри каталога
// лаунчера или внутри папки профиля (%LOCALAPPDATA%\…, см. companionExecRoots).
// Абсолютный путь, выход наверх и путь с диском («C:app.exe» относителен
// текущему каталогу диска, а не лаунчера) отвергаются.
func validateCompanionExec(exec string) error {
	p := strings.TrimSpace(exec)
	if p == "" {
		return errors.New("не задан exec")
	}
	where := "каталога лаунчера"
	if strings.HasPrefix(p, "%") {
		root, rest, err := splitCompanionExecRoot(p)
		if err != nil {
			return err
		}
		p, where = rest, "%"+root+"%"
	}
	if filepath.IsAbs(p) || strings.HasPrefix(p, `\`) || strings.HasPrefix(p, "/") {
		return fmt.Errorf("exec задаётся относительно %s, абсолютный путь не принимается", where)
	}
	if strings.Contains(p, ":") || strings.Contains(p, "%") {
		return fmt.Errorf("exec задаётся относительно %s, путь с указанием диска или другой переменной не принимается", where)
	}
	clean := filepath.Clean(filepath.FromSlash(p))
	if clean == "." || clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
		return fmt.Errorf("exec не может выходить за пределы %s", where)
	}
	return nil
}

// companionExecRoots — переменные окружения, с которых может начинаться exec:
// папки профиля и программ, куда ставят свои установщики. Произвольная
// переменная (%TEMP%, %PATH%, своя) не принимается: её значение задаёт кто
// угодно на станции, и путь ушёл бы куда угодно.
var companionExecRoots = []string{"LOCALAPPDATA", "APPDATA", "USERPROFILE", "ProgramFiles", "ProgramFiles(x86)"}

// companionGetenv — значение переменной окружения; тест подменяет.
var companionGetenv = os.Getenv

// splitCompanionExecRoot: «%LOCALAPPDATA%\Programs\x.exe» → «LOCALAPPDATA»,
// «Programs\x.exe». Имя переменной — из companionExecRoots без учёта регистра
// (как в Windows), после неё — разделитель и непустой путь.
func splitCompanionExecRoot(exec string) (root, rest string, err error) {
	end := strings.Index(exec[1:], "%")
	if end < 0 {
		return "", "", errors.New("exec: незакрытая переменная окружения")
	}
	name := exec[1 : end+1]
	rest = exec[end+2:]
	for _, r := range companionExecRoots {
		if strings.EqualFold(r, name) {
			root = r
		}
	}
	if root == "" {
		return "", "", fmt.Errorf("exec: в начале пути допускаются только %%%s%%", strings.Join(companionExecRoots, "%, %"))
	}
	if !strings.HasPrefix(rest, `\`) && !strings.HasPrefix(rest, "/") {
		return "", "", fmt.Errorf("exec: после %%%s%% нужен разделитель пути", root)
	}
	rest = strings.TrimLeft(rest, `\/`)
	if rest == "" {
		return "", "", fmt.Errorf("exec: после %%%s%% не указана программа", root)
	}
	return root, rest, nil
}

// resolveCompanionExec — полный путь программы манифеста: от каталога
// лаунчера или от папки профиля. Переменная не задана или задана не полным
// путём — ошибка, а не запуск из текущего каталога.
func resolveCompanionExec(dir, exec string) (string, error) {
	p := strings.TrimSpace(exec)
	if !strings.HasPrefix(p, "%") {
		return filepath.Join(dir, filepath.Clean(filepath.FromSlash(p))), nil
	}
	root, rest, err := splitCompanionExecRoot(p)
	if err != nil {
		return "", err
	}
	base := strings.TrimSpace(companionGetenv(root))
	if base == "" || !filepath.IsAbs(base) {
		return "", fmt.Errorf("exec: переменная %%%s%% на этом компьютере не задана полным путём", root)
	}
	return filepath.Join(base, filepath.Clean(filepath.FromSlash(rest))), nil
}

// mergeCompanions — программы для записи базы: манифест комплекта, поверх
// которого записи станции (ibases.yaml) с тем же ключом задают свой путь.
func mergeCompanions(manifest, record []Companion) []Companion {
	out := make([]Companion, 0, len(manifest)+len(record))
	byKey := map[string]int{}
	for _, c := range manifest {
		byKey[c.Key] = len(out)
		out = append(out, c)
	}
	for _, c := range record {
		if i, ok := byKey[c.Key]; ok {
			// Станция задаёт свой путь, но переменные окружения комплекта
			// (адрес сервера телефонии) остаются: форма записи их не задаёт.
			if len(c.Env) == 0 {
				c.Env = out[i].Env
			}
			out[i] = c
			continue
		}
		byKey[c.Key] = len(out)
		out = append(out, c)
	}
	return out
}

// companionEnvName — имя переменной окружения: латиница, цифры и «_», не с цифры.
var companionEnvName = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]{0,127}$`)

// validateCompanionEnv проверяет имена и значения переменных манифеста.
func validateCompanionEnv(env map[string]string) error {
	for name, value := range env {
		if !companionEnvName.MatchString(name) {
			return fmt.Errorf("env: имя «%s» — латиница, цифры и «_», не с цифры", name)
		}
		if strings.ContainsAny(value, "\x00\r\n") {
			return fmt.Errorf("env: значение %s содержит перевод строки или нулевой символ", name)
		}
	}
	return nil
}

// companionEnv — окружение лаунчера, поверх которого переменные программы
// (одноимённые заменяются без учёта регистра, как в Windows).
func companionEnv(base []string, extra map[string]string) []string {
	out := make([]string, 0, len(base)+len(extra))
	for _, kv := range base {
		name, _, _ := strings.Cut(kv, "=")
		overridden := false
		for k := range extra {
			if strings.EqualFold(k, name) {
				overridden = true
				break
			}
		}
		if !overridden {
			out = append(out, kv)
		}
	}
	names := make([]string, 0, len(extra))
	for k := range extra {
		names = append(names, k)
	}
	sort.Strings(names)
	for _, k := range names {
		out = append(out, k+"="+extra[k])
	}
	return out
}
