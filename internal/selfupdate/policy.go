package selfupdate

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	oblog "github.com/ivantit66/onebase/internal/logging"
	"gopkg.in/yaml.v3"
)

// PolicyFileName — файл политики обновлений. Лежит РЯДОМ С БИНАРЁМ, а не в
// профиле пользователя, и это принципиально: в свой профиль пользователь пишет
// сам, поэтому политика оттуда не была бы политикой. На общей установке
// (терминальный сервер, C:\Program Files) каталог бинаря принадлежит
// администратору — там его слово и оказывается последним.
const PolicyFileName = "onebase.policy.yaml"

// EnvUpdates — аварийный выключатель для развёртывания скриптом:
// ONEBASE_UPDATES=off запрещает и проверки, и кнопку.
const EnvUpdates = "ONEBASE_UPDATES"

// Policy — ограничения администратора на обновление платформы.
type Policy struct {
	// UI — показывать ли кнопку обновления в лаунчере и конфигураторе.
	UI Flag `yaml:"ui"`
	// Check — можно ли ходить в сеть за информацией о новых версиях.
	// Выключается в офлайн-контурах (issue #299): проверка не должна ни
	// подвешивать старт, ни оставлять следов исходящих соединений.
	Check Flag `yaml:"check"`
	// Channel фиксирует канал: пользователь не сможет переключиться.
	Channel string `yaml:"channel"`
	// Repo — источник сборок для закрытых контуров (зеркало).
	Repo string `yaml:"repo"`
}

type policyFile struct {
	Updates Policy `yaml:"updates"`
}

// Flag — необязательный булев параметр: отличает «не задано» от «задано false».
//
// Разбирается вручную, потому что yaml.v3 следует YAML 1.2, где булевы — только
// true/false, а `off`/`no`/`yes` становятся строками. Администратор, написавший
// привычное `check: off`, должен получить выключенную проверку, а не молча
// проигнорированную настройку.
type Flag struct {
	Set   bool
	Value bool
}

func (f *Flag) UnmarshalYAML(node *yaml.Node) error {
	switch strings.ToLower(strings.TrimSpace(node.Value)) {
	case "true", "yes", "on", "1":
		f.Set, f.Value = true, true
		return nil
	case "false", "no", "off", "0":
		f.Set, f.Value = true, false
		return nil
	default:
		return fmt.Errorf("ожидалось да/нет (true/false/on/off), получено %q", node.Value)
	}
}

// Or возвращает значение флага либо def, если он не задан.
func (f Flag) Or(def bool) bool {
	if f.Set {
		return f.Value
	}
	return def
}

// UIAllowed сообщает, показывать ли средства обновления в интерфейсе.
func (p Policy) UIAllowed() bool { return p.UI.Or(true) }

// CheckAllowed сообщает, разрешены ли сетевые проверки обновлений.
func (p Policy) CheckAllowed() bool { return p.Check.Or(true) }

// ChannelOr возвращает канал, зафиксированный политикой, либо def.
func (p Policy) ChannelOr(def Channel) Channel {
	switch Channel(strings.ToLower(strings.TrimSpace(p.Channel))) {
	case ChannelStable:
		return ChannelStable
	case ChannelBuild:
		return ChannelBuild
	default:
		return def
	}
}

// ChannelLocked сообщает, что канал задан политикой и менять его нельзя.
func (p Policy) ChannelLocked() bool {
	return p.ChannelOr("") != ""
}

// RepoOr возвращает источник сборок: политика, иначе def.
func (p Policy) RepoOr(def string) string {
	if r := strings.TrimSpace(p.Repo); r != "" {
		return r
	}
	if strings.TrimSpace(def) == "" {
		return DefaultRepo
	}
	return def
}

// LoadPolicy читает политику из каталога бинаря и накладывает переменную
// окружения.
//
// Битый файл политики трактуется как полный запрет, а не как её отсутствие:
// администратор, у которого опечатка в YAML, должен увидеть пропавшую кнопку,
// а не разрешённое обновление. Причина уходит в журнал.
func LoadPolicy(binDir string) Policy {
	var p Policy
	path := filepath.Join(binDir, PolicyFileName)
	data, err := os.ReadFile(path) //nolint:gosec // G304: путь фиксирован — файл политики рядом с бинарём
	switch {
	case os.IsNotExist(err):
		// Политики нет — обычный случай личной установки.
	case err != nil:
		oblog.Component("selfupdate").Warn("не удалось прочитать политику обновлений — обновление запрещено", "path", path, "err", err)
		p = denyAll()
	default:
		var f policyFile
		if err := yaml.Unmarshal(data, &f); err != nil {
			oblog.Component("selfupdate").Warn("политика обновлений не разобрана — обновление запрещено", "path", path, "err", err)
			p = denyAll()
		} else {
			p = f.Updates
		}
	}

	if updatesDisabledByEnv() {
		p.UI = Flag{Set: true, Value: false}
		p.Check = Flag{Set: true, Value: false}
	}
	return p
}

func denyAll() Policy {
	return Policy{UI: Flag{Set: true, Value: false}, Check: Flag{Set: true, Value: false}}
}

func updatesDisabledByEnv() bool {
	switch strings.ToLower(strings.TrimSpace(os.Getenv(EnvUpdates))) {
	case "off", "0", "false", "no", "disabled":
		return true
	default:
		return false
	}
}

// BinaryDir возвращает каталог текущего исполняемого файла — место, где лежит
// политика и куда придётся писать при обновлении.
func BinaryDir() (string, error) {
	exe, err := os.Executable()
	if err != nil {
		return "", fmt.Errorf("selfupdate: определить текущий бинарь: %w", err)
	}
	// Симлинк (типичная установка в /usr/local/bin) ведёт в реальный каталог —
	// проверять права нужно именно там.
	if resolved, err := filepath.EvalSymlinks(exe); err == nil {
		exe = resolved
	}
	return filepath.Dir(exe), nil
}

// BinaryPath — путь ТЕКУЩЕГО исполняемого файла с разрешёнными симлинками.
//
// Отличается от filepath.Join(BinaryDir(), BinaryName()) именем: жёсткое имя
// «onebase[.exe]» осмысленно только когда речь о каталоге УСТАНОВКИ (туда
// обновление кладёт файл именно так). Для вопроса «не подменили ли бинарь, из
// которого я загружен» имя должно быть настоящим — иначе бинарь под другим
// именем проверяет чужой (и обычно несуществующий) файл (#831).
func BinaryPath() (string, error) {
	exe, err := os.Executable()
	if err != nil {
		return "", fmt.Errorf("selfupdate: определить текущий бинарь: %w", err)
	}
	if resolved, err := filepath.EvalSymlinks(exe); err == nil {
		exe = resolved
	}
	return exe, nil
}

// CanWriteBinaryDir сообщает, может ли текущий пользователь заменить бинарь.
//
// Это и есть граница «кому можно обновлять платформу»: на личной установке
// (~/.onebase/bin) каталог принадлежит пользователю, на общей (Program Files,
// сетевая папка терминального сервера) — администратору. Проверяем пробой, а
// не разбором ACL: на Windows разбор прав не даёт надёжного ответа, а попытка
// создать файл даёт.
func CanWriteBinaryDir(dir string) bool {
	f, err := os.CreateTemp(dir, ".onebase-write-test-*")
	if err != nil {
		return false
	}
	name := f.Name()
	_ = f.Close()
	_ = os.Remove(name)
	return true
}

// CanSafelyUpdateBinaryDir additionally requires installation-scoped consumer
// coordination to be enforceable. Shared/read-only system installations may
// be executable by users who cannot join the lock protocol, so self-update is
// deliberately limited to private per-user installations.
func CanSafelyUpdateBinaryDir(dir string) bool {
	return ValidateBinaryUpdateTarget(dir) == nil
}

// ValidateBinaryUpdateTarget explains why dir cannot participate in the full
// consumer/writer lifecycle protocol. A writable directory alone is not
// sufficient when other users can execute the same package without access to
// its coordination files.
func ValidateBinaryUpdateTarget(dir string) error {
	if !CanWriteBinaryDir(dir) {
		return errors.New("selfupdate: installation directory is not writable by the current user")
	}
	_, err := targetCoordinationPermissions(dir)
	return err
}
