package launcher

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// fakeCompanionRuntime записывает запуски вместо настоящих процессов.
type fakeCompanionRuntime struct {
	existing map[string]bool
	running  map[string]bool
	startErr error
	started  []string
}

func (f *fakeCompanionRuntime) rt() companionRuntime {
	return companionRuntime{
		stat: func(path string) error {
			if f.existing[path] {
				return nil
			}
			return os.ErrNotExist
		},
		running: func(path string) bool { return f.running[path] },
		start: func(c Companion) error {
			if f.startErr != nil {
				return f.startErr
			}
			f.started = append(f.started, c.Key+":"+c.Path+":"+strings.Join(c.Args, ","))
			f.running[c.Path] = true
			return nil
		},
	}
}

const callistaPath = `C:\Callista\callista-operator.exe`

// Сервер назвал ключ — программа записи запускается, и только один раз за
// окно; ключ без записи и ключ, которого сервер не назвал, ничего не
// запускают; уже работающая программа не дублируется; неверный путь —
// сообщение, остальные программы своё получают.
func TestCompanionLauncherStart(t *testing.T) {
	list := []Companion{
		{Key: "callista", Path: callistaPath, Args: []string{"--minimized"}},
		{Key: "notes", Path: `C:\Notes\notes.exe`},
		{Key: "broken", Path: `C:\Нет\такой.exe`},
	}
	t.Run("ключ назван — запуск один раз", func(t *testing.T) {
		f := &fakeCompanionRuntime{existing: map[string]bool{callistaPath: true}, running: map[string]bool{}}
		l := newCompanionLauncher(f.rt())
		if msgs := l.Start(list, []string{"CallISTA"}); len(msgs) != 0 {
			t.Fatalf("сообщения: %v", msgs)
		}
		// Перезагрузка оболочки в том же окне снова присылает ключ.
		l.Start(list, []string{"callista"})
		if len(f.started) != 1 || f.started[0] != "callista:"+callistaPath+":--minimized" {
			t.Fatalf("запуски: %v", f.started)
		}
	})
	t.Run("ключа нет — ничего", func(t *testing.T) {
		f := &fakeCompanionRuntime{existing: map[string]bool{callistaPath: true}, running: map[string]bool{}}
		l := newCompanionLauncher(f.rt())
		if msgs := l.Start(list, nil); len(msgs) != 0 || len(f.started) != 0 {
			t.Fatalf("без ключей: запуски %v, сообщения %v", f.started, msgs)
		}
		// Ключ, которого нет в записи этого рабочего места, и ключ с
		// посторонними символами — тоже ничего.
		if msgs := l.Start(list, []string{"teams", "../../cmd"}); len(msgs) != 0 || len(f.started) != 0 {
			t.Fatalf("чужой ключ: запуски %v, сообщения %v", f.started, msgs)
		}
	})
	t.Run("уже запущена — не дублирует", func(t *testing.T) {
		f := &fakeCompanionRuntime{existing: map[string]bool{callistaPath: true}, running: map[string]bool{callistaPath: true}}
		l := newCompanionLauncher(f.rt())
		if msgs := l.Start(list, []string{"callista"}); len(msgs) != 0 || len(f.started) != 0 {
			t.Fatalf("работающая программа: запуски %v, сообщения %v", f.started, msgs)
		}
	})
	t.Run("неверный путь — сообщение, остальные запускаются", func(t *testing.T) {
		f := &fakeCompanionRuntime{existing: map[string]bool{callistaPath: true}, running: map[string]bool{}}
		l := newCompanionLauncher(f.rt())
		msgs := l.Start(list, []string{"broken", "callista"})
		if len(msgs) != 1 || !strings.Contains(msgs[0], "broken") || !strings.Contains(msgs[0], "не найдена") {
			t.Fatalf("сообщения: %v", msgs)
		}
		if len(f.started) != 1 || !strings.HasPrefix(f.started[0], "callista:") {
			t.Fatalf("запуски: %v", f.started)
		}
	})
	t.Run("ошибка запуска — сообщение", func(t *testing.T) {
		f := &fakeCompanionRuntime{existing: map[string]bool{callistaPath: true}, running: map[string]bool{}, startErr: errors.New("отказано в доступе")}
		l := newCompanionLauncher(f.rt())
		msgs := l.Start(list, []string{"callista"})
		if len(msgs) != 1 || !strings.Contains(msgs[0], "отказано в доступе") {
			t.Fatalf("сообщения: %v", msgs)
		}
	})
}

// Список из формы записи базы: «ключ | путь | аргументы | каталог»,
// аргументы в кавычках, обратное преобразование для поля формы.
func TestParseCompanions(t *testing.T) {
	text := "# телефония\n callista | C:\\Callista\\callista-operator.exe | --mode \"оператор КЦ\" | C:\\Callista\n\nnotes|C:\\Notes\\n.exe\n"
	list, err := ParseCompanions(text)
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 2 || list[0].Key != "callista" || list[0].Path != `C:\Callista\callista-operator.exe` ||
		strings.Join(list[0].Args, "|") != "--mode|оператор КЦ" || list[0].Dir != `C:\Callista` || list[1].Key != "notes" {
		t.Fatalf("разбор: %+v", list)
	}
	again, err := ParseCompanions(FormatCompanions(list))
	if err != nil || len(again) != 2 || strings.Join(again[0].Args, "|") != "--mode|оператор КЦ" || again[0].Dir != list[0].Dir {
		t.Fatalf("круг через форму: %+v, %v", again, err)
	}
	for _, bad := range []string{
		"callista",                                   // нет пути
		"Callista Operator | C:\\c.exe",              // ключ с пробелом
		"callista | callista-operator.exe",           // путь не полный
		"callista | C:\\a.exe\ncallista | C:\\b.exe", // повтор ключа
		"callista | C:\\a.exe | \"незакрыта",         // кавычка
	} {
		if _, err := ParseCompanions(bad); err == nil {
			t.Errorf("ожидалась ошибка для %q", bad)
		}
	}
}

// Сопутствующие программы хранятся в ibases.yaml вместе с записью базы, и
// окно Предприятия находит их по id записи.
func TestCompanionsPersistInStore(t *testing.T) {
	store := &Store{path: filepath.Join(t.TempDir(), "ibases.yaml")}
	b, err := NewClientBase("КЦ — рабочая", "https://10.2.5.62:8443")
	if err != nil {
		t.Fatal(err)
	}
	b.Companions = []Companion{{Key: "callista", Path: callistaPath}}
	if err := store.Add(b); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(store.path)
	if err != nil || !strings.Contains(string(raw), "companions:") || !strings.Contains(string(raw), "key: callista") {
		t.Fatalf("ibases.yaml без сопутствующих программ: %s %v", raw, err)
	}
	f := &fakeCompanionRuntime{existing: map[string]bool{callistaPath: true}, running: map[string]bool{}}
	if msgs := startBaseCompanions(newCompanionLauncher(f.rt()), store, b.ID, []string{"callista"}); len(msgs) != 0 || len(f.started) != 1 {
		t.Fatalf("запуск по записи базы: %v, %v", f.started, msgs)
	}
	// Записи нет (список баз правили) — работает только манифест комплекта;
	// без него запускать нечего, и это не ошибка.
	setLauncherDir(t, t.TempDir())
	f2 := &fakeCompanionRuntime{existing: map[string]bool{callistaPath: true}, running: map[string]bool{}}
	if msgs := startBaseCompanions(newCompanionLauncher(f2.rt()), store, "нет-такой", []string{"callista"}); len(msgs) != 0 || len(f2.started) != 0 {
		t.Fatalf("неизвестная запись без манифеста: %v, %v", f2.started, msgs)
	}
}
