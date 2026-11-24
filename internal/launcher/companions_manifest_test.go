package launcher

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func setLauncherDir(t *testing.T, dir string) {
	t.Helper()
	saved := launcherDir
	launcherDir = func() (string, error) { return dir, nil }
	t.Cleanup(func() { launcherDir = saved })
}

func writeManifest(t *testing.T, dir, body string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, companionManifestName), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

// Манифест комплекта (companions.yaml рядом с лаунчером) задаёт «что
// запускать», сервер ключом — «кому»: ключ от сервера + программа в
// манифесте → запуск из каталога комплекта с аргументами манифеста; ключ есть,
// но ни манифест, ни запись станции программу не знают → ничего, без
// сообщения; путь станции из ibases.yaml главнее манифеста.
func TestCompanionsFromManifest(t *testing.T) {
	dir := t.TempDir()
	setLauncherDir(t, dir)
	writeManifest(t, dir, "companions:\n  callista:\n    exec: companions/callista-operator/callista-operator.exe\n    args: [\"--minimized\"]\n")
	exe := filepath.Join(dir, "companions", "callista-operator", "callista-operator.exe")
	store := &Store{path: filepath.Join(t.TempDir(), "ibases.yaml")}
	b, err := NewClientBase("КЦ — рабочая", "https://10.2.5.62:8443")
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Add(b); err != nil {
		t.Fatal(err)
	}

	t.Run("ключ от сервера и программа в манифесте — запуск", func(t *testing.T) {
		f := &fakeCompanionRuntime{existing: map[string]bool{exe: true}, running: map[string]bool{}}
		msgs := startBaseCompanions(newCompanionLauncher(f.rt()), store, b.ID, []string{"callista"})
		if len(msgs) != 0 || len(f.started) != 1 || f.started[0] != "callista:"+exe+":--minimized" {
			t.Fatalf("запуски %v, сообщения %v", f.started, msgs)
		}
	})
	t.Run("ключа нет ни в манифесте, ни в записи — ничего", func(t *testing.T) {
		f := &fakeCompanionRuntime{existing: map[string]bool{exe: true}, running: map[string]bool{}}
		msgs := startBaseCompanions(newCompanionLauncher(f.rt()), store, b.ID, []string{"notes"})
		if len(msgs) != 0 || len(f.started) != 0 {
			t.Fatalf("запуски %v, сообщения %v", f.started, msgs)
		}
	})
	t.Run("путь станции главнее манифеста", func(t *testing.T) {
		own := `D:\Телефония\callista.exe`
		b.Companions = []Companion{{Key: "callista", Path: own}}
		if err := store.Update(b); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { b.Companions = nil; _ = store.Update(b) })
		f := &fakeCompanionRuntime{existing: map[string]bool{exe: true, own: true}, running: map[string]bool{}}
		msgs := startBaseCompanions(newCompanionLauncher(f.rt()), store, b.ID, []string{"callista"})
		if len(msgs) != 0 || len(f.started) != 1 || !strings.HasPrefix(f.started[0], "callista:"+own+":") {
			t.Fatalf("запуски %v, сообщения %v", f.started, msgs)
		}
	})
}

// Небезопасный путь в манифесте отвергает манифест ЦЕЛИКОМ — и соседняя
// безопасная запись не запускается: частично применённое объявление хуже
// отказа. Отказ виден сообщением; запись станции при этом работает.
func TestCompanionsManifestRejectsUnsafePaths(t *testing.T) {
	for _, bad := range []string{
		`C:\Windows\System32\calc.exe`,
		`/usr/bin/xterm`,
		`\server\share\x.exe`,
		`C:calc.exe`,
		`../outside.exe`,
		`companions/../../outside.exe`,
		``,
	} {
		t.Run(bad, func(t *testing.T) {
			dir := t.TempDir()
			setLauncherDir(t, dir)
			writeManifest(t, dir, "companions:\n  good:\n    exec: companions/good.exe\n  callista:\n    exec: '"+bad+"'\n")
			good := filepath.Join(dir, "companions", "good.exe")
			f := &fakeCompanionRuntime{existing: map[string]bool{good: true}, running: map[string]bool{}}
			msgs := startBaseCompanions(newCompanionLauncher(f.rt()), nil, "", []string{"good", "callista"})
			if len(f.started) != 0 {
				t.Fatalf("манифест с небезопасным путём применён: %v", f.started)
			}
			if len(msgs) != 1 || !strings.Contains(msgs[0], companionManifestName) {
				t.Fatalf("отказ манифеста не виден: %v", msgs)
			}
		})
	}
	// Ключ, не годный по правилу, — тоже отказ манифеста.
	dir := t.TempDir()
	if _, err := os.Stat(dir); err != nil {
		t.Fatal(err)
	}
	writeManifest(t, dir, "companions:\n  \"Callista Operator\":\n    exec: companions/c.exe\n")
	if _, err := loadCompanionManifest(dir); err == nil {
		t.Fatal("ключ с пробелом принят")
	}
	// Нет манифеста — нормальное состояние.
	if list, err := loadCompanionManifest(t.TempDir()); err != nil || list != nil {
		t.Fatalf("без манифеста: %v %v", list, err)
	}
}

// setCompanionEnv подменяет переменные окружения, которые читает манифест.
func setCompanionEnv(t *testing.T, env map[string]string) {
	t.Helper()
	saved := companionGetenv
	companionGetenv = func(name string) string { return env[name] }
	t.Cleanup(func() { companionGetenv = saved })
}

// Callista 0.7.1+ ставится своим установщиком в профиль пользователя и дальше
// обновляется сама: комплект указывает путь от %LOCALAPPDATA%, окно остаётся
// владельцем запуска (тот же ключ от сервера, те же args и env манифеста).
func TestCompanionsManifestInstalledPath(t *testing.T) {
	dir := t.TempDir()
	local := t.TempDir()
	setLauncherDir(t, dir)
	setCompanionEnv(t, map[string]string{"LOCALAPPDATA": local})
	exe := filepath.Join(local, "Programs", "@callistaoperator-desktop", "callista-operator.exe")
	for _, exec := range []string{
		`%LOCALAPPDATA%\Programs\@callistaoperator-desktop\callista-operator.exe`,
		`%localappdata%/Programs/@callistaoperator-desktop/callista-operator.exe`,
	} {
		t.Run(exec, func(t *testing.T) {
			writeManifest(t, dir, "companions:\n  callista:\n    exec: '"+exec+"'\n    args: [\"--minimized\"]\n    env:\n      CALLISTA_OPERATOR_API_URL: https://cc.example:8445\n")
			list, err := loadCompanionManifest(dir)
			if err != nil || len(list) != 1 {
				t.Fatalf("манифест: %v %v", list, err)
			}
			if list[0].Path != exe || list[0].Dir != filepath.Dir(exe) || list[0].Env["CALLISTA_OPERATOR_API_URL"] == "" {
				t.Fatalf("программа: %+v, ждали %s", list[0], exe)
			}
			f := &fakeCompanionRuntime{existing: map[string]bool{exe: true}, running: map[string]bool{}}
			msgs := startBaseCompanions(newCompanionLauncher(f.rt()), nil, "", []string{"callista"})
			if len(msgs) != 0 || len(f.started) != 1 || f.started[0] != "callista:"+exe+":--minimized" {
				t.Fatalf("запуски %v, сообщения %v", f.started, msgs)
			}
		})
	}
}

// Переменная — только из короткого списка папок профиля и программ; после неё
// — путь внутри этой папки. Иначе манифест отвергается целиком.
func TestCompanionsManifestRejectsUnsafeInstalledPaths(t *testing.T) {
	local := t.TempDir()
	setCompanionEnv(t, map[string]string{"LOCALAPPDATA": local, "TEMP": local, "PATH": local})
	for _, bad := range []string{
		`%TEMP%\callista.exe`,
		`%PATH%\callista.exe`,
		`%LOCALAPPDATA%\..\callista.exe`,
		`%LOCALAPPDATA%\Programs\..\..\callista.exe`,
		`%LOCALAPPDATA%`,
		`%LOCALAPPDATA%\`,
		`%LOCALAPPDATA%callista.exe`,
		`%LOCALAPPDATA%\C:\callista.exe`,
		`%LOCALAPPDATA%\%TEMP%\callista.exe`,
		`%LOCALAPPDATA\callista.exe`,
	} {
		t.Run(bad, func(t *testing.T) {
			dir := t.TempDir()
			writeManifest(t, dir, "companions:\n  callista:\n    exec: '"+bad+"'\n")
			if list, err := loadCompanionManifest(dir); err == nil {
				t.Fatalf("принят небезопасный путь: %+v", list)
			}
		})
	}
	// Переменная не задана на станции — отказ, а не путь от текущего каталога.
	setCompanionEnv(t, map[string]string{})
	dir := t.TempDir()
	writeManifest(t, dir, "companions:\n  callista:\n    exec: '%LOCALAPPDATA%/Programs/c.exe'\n")
	if _, err := loadCompanionManifest(dir); err == nil || !strings.Contains(err.Error(), "LOCALAPPDATA") {
		t.Fatalf("без переменной: %v", err)
	}
}
