package launcher

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/ivantit66/onebase/internal/companions"
)

// Сопутствующие программы — то, что рабочее место запускает вместе с базой
// (у оператора колл-центра — программа телефонии). Запись базы хранит список
// «ключ → программа на ЭТОМ компьютере»; сервер после входа называет только
// КЛЮЧИ, нужные пользователю (GET /ui/launcher/companions), и окно Предприятия
// запускает соответствующие программы, если они ещё не работают.
//
// Разделение принципиальное: путь, аргументы и каталог задаются только здесь,
// в локальной настройке лаунчера. Если бы их сообщал сервер, любой, кто может
// менять конфигурацию, запускал бы произвольные программы на всех станциях.
// Неизвестный ключ ничего не запускает.

// Companion — одна сопутствующая программа записи базы.
type Companion struct {
	// Key — ключ, которым сервер называет программу пользователю: латиница в
	// нижнем регистре, цифры, «_», «.», «-»; до 32 символов.
	Key string `yaml:"key"`
	// Path — полный путь к исполняемому файлу на этом компьютере.
	Path string `yaml:"path"`
	// Args — аргументы запуска.
	Args []string `yaml:"args,omitempty"`
	// Dir — рабочий каталог; пусто — каталог программы.
	Dir string `yaml:"dir,omitempty"`
	// Env — переменные окружения поверх окружения лаунчера (только из
	// манифеста комплекта): например CALLISTA_OPERATOR_API_URL закрепляет
	// адрес сервера телефонии в клиенте.
	Env map[string]string `yaml:"env,omitempty"`
	// UpdateManifest — адрес списка обновлений (только из манифеста комплекта,
	// в ibases.yaml не хранится): перед запуском лаунчер ставит новую версию.
	UpdateManifest string `yaml:"-"`
}

// NormalizeCompanionKey приводит ключ к нижнему регистру и проверяет его.
func NormalizeCompanionKey(raw string) (string, bool) { return companions.NormalizeKey(raw) }

// ParseCompanions разбирает список из формы записи базы: строка на программу,
// поля через «|» — «ключ | путь | аргументы | рабочий каталог»; аргументы и
// каталог необязательны. Пустые строки и строки с «#» в начале пропускаются.
func ParseCompanions(text string) ([]Companion, error) {
	var out []Companion
	seen := map[string]bool{}
	for i, line := range strings.Split(strings.ReplaceAll(text, "\r\n", "\n"), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		parts := strings.Split(line, "|")
		if len(parts) < 2 || len(parts) > 4 {
			return nil, fmt.Errorf("строка %d: нужно «ключ | путь к программе | аргументы | рабочий каталог» (аргументы и каталог — по желанию)", i+1)
		}
		key, ok := NormalizeCompanionKey(parts[0])
		if !ok {
			return nil, fmt.Errorf("строка %d: ключ «%s» — латиница в нижнем регистре, цифры, «_», «.», «-», до 32 символов", i+1, strings.TrimSpace(parts[0]))
		}
		if seen[key] {
			return nil, fmt.Errorf("строка %d: ключ «%s» уже есть выше", i+1, key)
		}
		seen[key] = true
		c := Companion{Key: key, Path: strings.TrimSpace(parts[1])}
		if c.Path == "" {
			return nil, fmt.Errorf("строка %d: не указан путь к программе", i+1)
		}
		if !filepath.IsAbs(c.Path) {
			return nil, fmt.Errorf("строка %d: путь к программе должен быть полным, например C:\\Callista\\callista-operator.exe", i+1)
		}
		if len(parts) > 2 {
			args, err := splitCompanionArgs(parts[2])
			if err != nil {
				return nil, fmt.Errorf("строка %d: %v", i+1, err)
			}
			c.Args = args
		}
		if len(parts) > 3 {
			c.Dir = strings.TrimSpace(parts[3])
		}
		out = append(out, c)
	}
	return out, nil
}

// FormatCompanions — обратное ParseCompanions, для поля формы.
func FormatCompanions(list []Companion) string {
	var b strings.Builder
	for _, c := range list {
		b.WriteString(c.Key + " | " + c.Path)
		if len(c.Args) > 0 || c.Dir != "" {
			quoted := make([]string, 0, len(c.Args))
			for _, a := range c.Args {
				if a == "" || strings.ContainsAny(a, " \t\"") {
					a = `"` + strings.ReplaceAll(a, `"`, `\"`) + `"`
				}
				quoted = append(quoted, a)
			}
			b.WriteString(" | " + strings.Join(quoted, " "))
		}
		if c.Dir != "" {
			b.WriteString(" | " + c.Dir)
		}
		b.WriteString("\n")
	}
	return b.String()
}

// splitCompanionArgs делит строку аргументов по пробелам; часть в двойных
// кавычках — один аргумент (\" внутри — кавычка).
func splitCompanionArgs(s string) ([]string, error) {
	var args []string
	var cur strings.Builder
	inQuote, has := false, false
	for i := 0; i < len(s); i++ {
		ch := s[i]
		switch {
		case ch == '\\' && inQuote && i+1 < len(s) && s[i+1] == '"':
			cur.WriteByte('"')
			i++
		case ch == '"':
			inQuote, has = !inQuote, true
		case (ch == ' ' || ch == '\t') && !inQuote:
			if has {
				args = append(args, cur.String())
				cur.Reset()
				has = false
			}
		default:
			cur.WriteByte(ch)
			has = true
		}
	}
	if inQuote {
		return nil, errors.New("в аргументах не закрыта кавычка")
	}
	if has {
		args = append(args, cur.String())
	}
	return args, nil
}

// companionRuntime — то, чем запуск трогает систему; в тестах подменяется.
type companionRuntime struct {
	stat    func(path string) error
	running func(path string) bool
	start   func(c Companion) error
}

var systemCompanionRuntime = companionRuntime{
	stat: func(path string) error {
		info, err := os.Stat(path)
		if err != nil {
			return err
		}
		if info.IsDir() {
			return fmt.Errorf("%s — каталог, а не программа", path)
		}
		return nil
	},
	running: companionProcessRunning,
	start: func(c Companion) error {
		cmd := exec.Command(c.Path, c.Args...) //nolint:gosec // G204: путь и аргументы — локальная настройка рабочего места в ibases.yaml; сервер передаёт только ключ
		cmd.Dir = c.Dir
		if cmd.Dir == "" {
			cmd.Dir = filepath.Dir(c.Path)
		}
		if len(c.Env) > 0 {
			cmd.Env = companionEnv(os.Environ(), c.Env)
		}
		detachCompanion(cmd)
		if err := cmd.Start(); err != nil {
			return err
		}
		// Программа живёт сама по себе: окно Предприятия её не ждёт и не
		// завершает вместе с собой.
		return cmd.Process.Release()
	},
}

// companionLauncher запускает сопутствующие программы одного окна
// Предприятия. Ключ, уже обработанный этим окном, повторно не запускается:
// оболочка перезагружается при навигации, и программа, которую пользователь
// закрыл сам, не должна открываться снова на каждой странице.
type companionLauncher struct {
	rt      companionRuntime
	mu      sync.Mutex
	handled map[string]bool

	// update — обновление программы перед запуском (companions_update.go);
	// nil — без обновлений. Идёт в фоне: окно Предприятия не ждёт скачивания.
	update   func(ctx context.Context, c Companion) (string, error)
	rollback func(c Companion) error
	// notify показывает оператору итог фонового обновления; nil — не показывать.
	notify func(msg string)
	bg     sync.WaitGroup
}

func newCompanionLauncher(rt companionRuntime) *companionLauncher {
	return &companionLauncher{rt: rt, handled: map[string]bool{}}
}

// companionUpdateTimeout — сколько ждать скачивания и установки, прежде чем
// запустить установленную версию.
const companionUpdateTimeout = 15 * time.Minute

// Start запускает программы записи для ключей, которые назвал сервер, и
// возвращает сообщения об ошибках для показа пользователю. Ключ без программы
// в записи — молча пропускается: программа нужна не на каждом рабочем месте.
func (l *companionLauncher) Start(list []Companion, keys []string) []string {
	l.mu.Lock()
	defer l.mu.Unlock()
	var msgs []string
	for _, raw := range keys {
		key, ok := NormalizeCompanionKey(raw)
		if !ok || l.handled[key] {
			continue
		}
		l.handled[key] = true
		var c *Companion
		for i := range list {
			if list[i].Key == key {
				c = &list[i]
				break
			}
		}
		if c == nil {
			continue
		}
		if err := l.rt.stat(c.Path); err != nil {
			msgs = append(msgs, fmt.Sprintf("Не удалось запустить «%s»: программа не найдена (%s). Проверьте путь в записи базы в лаунчере.", c.Key, c.Path))
			continue
		}
		if l.rt.running(c.Path) {
			// Уже работает — возможно, идёт разговор: ни обновлять, ни
			// запускать второй раз. Обновится при следующем запуске.
			continue
		}
		if c.UpdateManifest != "" && l.update != nil {
			l.bg.Add(1)
			go l.updateAndStart(*c)
			continue
		}
		if err := l.rt.start(*c); err != nil {
			msgs = append(msgs, fmt.Sprintf("Не удалось запустить «%s» (%s): %v", c.Key, c.Path, err))
		}
	}
	return msgs
}

// updateAndStart — фоновая часть запуска программы со списком обновлений:
// проверить список и поставить новую версию, затем запустить. Сбой обновления
// не мешает запуску установленной версии — он только пишется в журнал.
func (l *companionLauncher) updateAndStart(c Companion) {
	defer l.bg.Done()
	ctx, cancel := context.WithTimeout(context.Background(), companionUpdateTimeout)
	version, err := l.update(ctx, c)
	cancel()
	if err != nil {
		respondLog().Warn("обновление сопутствующей программы не выполнено, запускается установленная", "key", c.Key, "err", err)
		version = ""
	}
	if startErr := l.rt.start(c); startErr != nil {
		if version != "" && l.rollback != nil {
			respondLog().Warn("новая версия сопутствующей программы не запустилась, возвращается прежняя", "key", c.Key, "version", version, "err", startErr)
			if rbErr := l.rollback(c); rbErr == nil {
				if again := l.rt.start(c); again == nil {
					l.say(fmt.Sprintf("%s %s не запустилась — оставлена прежняя версия.", companionTitle(c.Key), version))
					return
				}
			} else {
				respondLog().Warn("прежняя версия сопутствующей программы не вернулась", "key", c.Key, "err", rbErr)
			}
		}
		l.say(fmt.Sprintf("Не удалось запустить «%s» (%s): %v", c.Key, c.Path, startErr))
		return
	}
	if version != "" {
		respondLog().Info("сопутствующая программа обновлена", "key", c.Key, "version", version)
		l.say(fmt.Sprintf("%s обновлена до версии %s.", companionTitle(c.Key), version))
	}
}

func (l *companionLauncher) say(msg string) {
	if l.notify != nil {
		l.notify(msg)
	}
}

// companionTitle — имя программы для сообщения оператору.
func companionTitle(key string) string {
	if strings.EqualFold(key, "callista") {
		return "Callista"
	}
	return "«" + key + "»"
}

// startBaseCompanions — запуск для записи базы baseID: программы манифеста
// комплекта (companions.yaml рядом с лаунчером), поверх них — пути станции из
// записи в ibases.yaml. Отвергнутый манифест — сообщение, записи станции при
// этом работают.
func startBaseCompanions(l *companionLauncher, store *Store, baseID string, keys []string) []string {
	if len(keys) == 0 {
		return nil
	}
	var msgs []string
	var manifest []Companion
	if dir, err := launcherDir(); err == nil {
		var mErr error
		manifest, mErr = loadCompanionManifest(dir)
		if mErr != nil {
			msgs = append(msgs, "Сопутствующие программы комплекта не запущены: "+mErr.Error())
			manifest = nil
		}
	}
	var record []Companion
	if store != nil && baseID != "" {
		if b, err := store.Get(baseID); err == nil {
			record = b.Companions
		}
	}
	return append(msgs, l.Start(mergeCompanions(manifest, record), keys)...)
}
