package launcher

import (
	"errors"
	"fmt"
	"net/url"
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
//	    update:
//	      manifest: https://cc01.icecorp.ru/updates/callista/operator-desktop.json
//
// update.manifest — список обновлений программы на её сервере (формат Callista:
// version + downloads.win32{url, size, sha256}): перед запуском лаунчер ставит
// версию новее установленной (companions_update.go).

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
	// Update — откуда брать обновления программы.
	Update companionUpdateSpec `yaml:"update"`
}

type companionUpdateSpec struct {
	// Manifest — https-адрес списка обновлений; пусто — программа не обновляется.
	Manifest string `yaml:"manifest"`
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
		if err := validateCompanionUpdateManifest(spec.Update.Manifest); err != nil {
			return nil, fmt.Errorf("%s: «%s»: %w", companionManifestName, name, err)
		}
		path := filepath.Join(dir, filepath.Clean(filepath.FromSlash(strings.TrimSpace(spec.Exec))))
		out = append(out, Companion{Key: key, Path: path, Args: spec.Args, Dir: filepath.Dir(path), Env: spec.Env,
			UpdateManifest: strings.TrimSpace(spec.Update.Manifest)})
	}
	return out, nil
}

// validateCompanionExec допускает только относительный путь внутри каталога
// лаунчера. Абсолютный путь, выход наверх и путь с диском («C:app.exe»
// относителен текущему каталогу диска, а не лаунчера) отвергаются.
func validateCompanionExec(exec string) error {
	p := strings.TrimSpace(exec)
	if p == "" {
		return errors.New("не задан exec")
	}
	if filepath.IsAbs(p) || strings.HasPrefix(p, `\`) || strings.HasPrefix(p, "/") {
		return errors.New("exec задаётся относительно каталога лаунчера, абсолютный путь не принимается")
	}
	if strings.Contains(p, ":") {
		return errors.New("exec задаётся относительно каталога лаунчера, путь с указанием диска не принимается")
	}
	clean := filepath.Clean(filepath.FromSlash(p))
	if clean == "." || clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
		return errors.New("exec не может выходить за каталог лаунчера")
	}
	return nil
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
			// Обновления комплект ставит только в свою папку: программу по
			// пути станции лаунчер не трогает.
			out[i] = c
			continue
		}
		byKey[c.Key] = len(out)
		out = append(out, c)
	}
	return out
}

// validateCompanionUpdateManifest — адрес списка обновлений: только https с
// хостом. Скачанный архив проверяется по SHA-256 из этого списка, поэтому сам
// список обязан приходить по защищённому каналу.
func validateCompanionUpdateManifest(raw string) error {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil
	}
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "https" || u.Host == "" || u.User != nil {
		return errors.New("update.manifest — полный адрес https://… списка обновлений")
	}
	return nil
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
