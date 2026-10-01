package launcher

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"gopkg.in/yaml.v3"
)

// Сопутствующие приложения рабочего места (companion).
//
// Рабочему месту бывает нужен локальный помощник, работающий рядом с
// Предприятием: софтфон оператора контакт-центра, агент ЭЦП, агент сканера или
// печати, терминал оплаты. Раньше такой процесс запускали автозагрузкой
// Windows, и лаунчер о нём не знал ничего: не видел, работает ли он, и не мог
// показать это пользователю.
//
// ВыполнитьКоманду из DSL для этого не подходит и подходить не должно: он
// исполняется НА СЕРВЕРЕ, синхронно, с обязательным таймаутом, и закрыт флагом
// exec.enabled плюс DenyExec в песочнице. Запуск у пользователя — другое место,
// и принадлежит лаунчеру.
//
// Граница безопасности. Лаунчер запускает только то, что лежит в его собственном
// каталоге и объявлено манифестом рядом с его же исполняемым файлом. Ни путь, ни
// аргументы не приходят ни из базы, ни из реестра баз: иначе сервер (или правка
// ibases.yaml, который лежит в профиле пользователя) диктовал бы станции, какой
// файл запустить, и компрометация сервера превращалась бы в запуск произвольного
// кода на всех рабочих местах. Это та же причина, по которой exec.enabled
// выключен по умолчанию.

// companionManifestName — имя манифеста рядом с исполняемым файлом лаунчера.
const companionManifestName = "companions.yaml"

// companionManifest — содержимое companions.yaml.
//
// Пример:
//
//	companions:
//	  callista-operator:
//	    exec: companions/callista-operator/callista-operator.exe
//	    args: ["--minimized"]
//
// Путь относительный — от каталога лаунчера. Подкаталог разрешён сознательно:
// Electron-дистрибутив приезжает каталогом целиком (exe плюс resources и DLL), и
// требовать от поставщика сборку одним файлом не нужно.
type companionManifest struct {
	Companions map[string]companionSpec `yaml:"companions"`
}

// companionSpec — одно объявленное приложение.
type companionSpec struct {
	// Exec — путь относительно каталога лаунчера.
	Exec string `yaml:"exec"`
	// Args — аргументы запуска. Берутся только отсюда: произвольная строка
	// аргументов, пришедшая извне, равносильна произвольной команде даже при
	// фиксированном пути.
	Args []string `yaml:"args"`
}

// ErrCompanionUnknown — логическое имя не объявлено манифестом этого
// дистрибутива. Не причина отказать в работе: рабочее место обязано открыться,
// иначе обновление лаунчера без companion остановило бы контакт-центр.
var ErrCompanionUnknown = errors.New("launcher: companion is not declared by this distribution")

// launcherDir — каталог исполняемого файла лаунчера. Подменяется тестом;
// в рабочем коде всегда вычисляется от os.Executable.
var launcherDir = func() (string, error) {
	exe, err := exePath()
	if err != nil {
		return "", err
	}
	return filepath.Dir(exe), nil
}

// loadCompanionManifest читает манифест дистрибутива. Отсутствие файла — не
// ошибка: дистрибутив без сопутствующих приложений нормален.
func loadCompanionManifest() (map[string]companionSpec, error) {
	dir, err := launcherDir()
	if err != nil {
		return nil, err
	}
	raw, err := os.ReadFile(filepath.Join(dir, companionManifestName)) //nolint:gosec // G304: путь собран из каталога самого лаунчера и константы, извне не приходит
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}
	var m companionManifest
	if err := yaml.Unmarshal(raw, &m); err != nil {
		return nil, fmt.Errorf("%s: %w", companionManifestName, err)
	}
	out := make(map[string]companionSpec, len(m.Companions))
	for name, spec := range m.Companions {
		if err := validateCompanionExec(spec.Exec); err != nil {
			return nil, fmt.Errorf("%s: companion %q: %w", companionManifestName, name, err)
		}
		out[name] = spec
	}
	return out, nil
}

// validateCompanionExec допускает только относительный путь внутри каталога
// лаунчера. Абсолютный путь и выход наверх отвергаются: манифест лежит рядом с
// исполняемым файлом, и если его смогли подменить, то смогли подменить и сам
// лаунчер — но расширять это до «запустить что угодно на диске» всё равно нельзя.
func validateCompanionExec(exec string) error {
	p := strings.TrimSpace(exec)
	if p == "" {
		return errors.New("не задан exec")
	}
	if filepath.IsAbs(p) || strings.HasPrefix(p, `\`) || strings.HasPrefix(p, "/") {
		return errors.New("exec задаётся относительно каталога лаунчера, абсолютный путь не принимается")
	}
	// Диск с двоеточием («C:app.exe») на Windows относителен текущему каталогу
	// диска, а не каталогу лаунчера — такой путь уводит из дистрибутива.
	if strings.Contains(p, ":") {
		return errors.New("exec задаётся относительно каталога лаунчера, путь с указанием диска не принимается")
	}
	clean := filepath.Clean(filepath.FromSlash(p))
	if clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
		return errors.New("exec не может выходить за каталог лаунчера")
	}
	return nil
}

// companionProc — запущенное сопутствующее приложение.
type companionProc struct {
	cmd      *exec.Cmd
	started  time.Time
	exited   bool
	exitErr  error
	exitedAt time.Time
}

// CompanionState — состояние одного companion для показа в лаунчере.
type CompanionState struct {
	Name string
	// Running — процесс, запущенный этим лаунчером, ещё жив.
	Running bool
	// Declared — имя объявлено манифестом дистрибутива.
	Declared bool
	// Failed — процесс завершился сам. Отличать это от «не запускали» важно:
	// молчаливое «не работает» выглядит как будто companion и не был нужен.
	Failed bool
	// Err — текст последней ошибки запуска или завершения, для панели лаунчера.
	Err string
}

// companionRunner запускает и отслеживает сопутствующие приложения.
// Ничего не останавливает: у companion может идти работа (у софтфона —
// разговор), и закрытие окна Предприятия не повод её прерывать.
type companionRunner struct {
	mu    sync.Mutex
	procs map[string]*companionProc
	// specs кэширует манифест: он меняется только вместе с дистрибутивом,
	// то есть не в течение сеанса.
	specs    map[string]companionSpec
	specsErr error
	loaded   bool
	// startCmd подменяется тестом, чтобы не запускать настоящий процесс.
	startCmd func(*exec.Cmd) error
}

func newCompanionRunner() *companionRunner {
	return &companionRunner{procs: map[string]*companionProc{}}
}

func (c *companionRunner) manifest() (map[string]companionSpec, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.loaded {
		c.specs, c.specsErr = loadCompanionManifest()
		c.loaded = true
	}
	return c.specs, c.specsErr
}

// Ensure запускает companion, если он объявлен и ещё не работает.
//
// Повторное открытие рабочего места не плодит второй экземпляр. Отдельно
// учтено, что приложение может реализовать single-instance само: тогда второй
// процесс завершается сразу и успешно, и принять это за падение нельзя —
// поэтому мгновенный выход при уже живом первом экземпляре мы и не создаём,
// просто не запуская второй раз.
func (c *companionRunner) Ensure(name string) error {
	specs, err := c.manifest()
	if err != nil {
		return err
	}
	spec, ok := specs[name]
	if !ok {
		return ErrCompanionUnknown
	}
	c.mu.Lock()
	if p := c.procs[name]; p != nil && !p.exited {
		c.mu.Unlock()
		return nil
	}
	c.mu.Unlock()

	dir, err := launcherDir()
	if err != nil {
		return err
	}
	full := filepath.Join(dir, filepath.FromSlash(spec.Exec))
	cmd := exec.Command(full, spec.Args...) //nolint:gosec // G204: путь собран из каталога лаунчера и манифеста рядом с его exe; аргументы объявлены там же, извне не приходят
	// Рабочий каталог — каталог самого приложения: Electron и подобные ищут
	// рядом resources и DLL.
	cmd.Dir = filepath.Dir(full)

	start := c.startCmd
	if start == nil {
		start = (*exec.Cmd).Start
	}
	if err := start(cmd); err != nil {
		c.mu.Lock()
		c.procs[name] = &companionProc{cmd: cmd, exited: true, exitErr: err, exitedAt: time.Now()}
		c.mu.Unlock()
		return err
	}
	proc := &companionProc{cmd: cmd, started: time.Now()}
	c.mu.Lock()
	c.procs[name] = proc
	c.mu.Unlock()

	// Падение должно быть видно, а не выглядеть как «не запускали». Ждём в
	// отдельной горутине: Wait освобождает дескрипторы процесса.
	if cmd.Process != nil {
		go func() {
			waitErr := cmd.Wait()
			c.mu.Lock()
			proc.exited = true
			proc.exitErr = waitErr
			proc.exitedAt = time.Now()
			c.mu.Unlock()
		}()
	}
	return nil
}

// States возвращает состояние перечисленных companion для панели лаунчера.
func (c *companionRunner) States(names []string) []CompanionState {
	specs, _ := c.manifest()
	out := make([]CompanionState, 0, len(names))
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, name := range names {
		st := CompanionState{Name: name}
		_, st.Declared = specs[name]
		if p := c.procs[name]; p != nil {
			switch {
			case !p.exited:
				st.Running = true
			default:
				st.Failed = true
				if p.exitErr != nil {
					st.Err = p.exitErr.Error()
				}
			}
		}
		out = append(out, st)
	}
	return out
}
