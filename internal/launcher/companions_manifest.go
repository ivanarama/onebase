package launcher

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
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
		path := filepath.Join(dir, filepath.Clean(filepath.FromSlash(strings.TrimSpace(spec.Exec))))
		out = append(out, Companion{Key: key, Path: path, Args: spec.Args, Dir: filepath.Dir(path)})
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
			out[i] = c
			continue
		}
		byKey[c.Key] = len(out)
		out = append(out, c)
	}
	return out
}
