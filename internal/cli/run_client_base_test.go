package cli

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ivantit66/onebase/internal/launcher"
)

// `run --id` для записи клиентского подключения обязан отказать ДО любых
// локальных операций.
//
// У такой записи DBType и DB пусты, и legacy-ветка resolveServerLaunchConfig
// принимала их за старую SQLite-запись: создавала базу в os.TempDir() и
// поднимала listener. Пользователь при этом работал бы в отдельной пустой базе
// вместо своего сервера и заметил бы это не сразу, поэтому отказ обязан стоять
// раньше, чем появится файл базы или сокет.
func TestRunRejectsClientBaseBeforeTouchingDatabase(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)

	store, err := launcher.NewStore()
	if err != nil {
		t.Fatal(err)
	}
	base, err := launcher.NewClientBase("Remote", "https://example.invalid:8443")
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Add(base); err != nil {
		t.Fatal(err)
	}
	// Путь, который выдумывала legacy-ветка для записи без DBType и DB.
	legacyDB := filepath.Join(os.TempDir(), "onebase_"+base.ID+".db")
	if err := os.Remove(legacyDB); err != nil && !os.IsNotExist(err) {
		t.Fatalf("подготовка: %v", err)
	}

	// runCmd — глобальная команда с «липкими» флагами: берём настоящее
	// определение, прежние значения возвращаем соседним тестам пакета.
	flag := runCmd.Flags().Lookup("id")
	if flag == nil {
		t.Fatal("у команды run нет флага --id")
	}
	prev := flag.Value.String()
	t.Cleanup(func() { _ = runCmd.Flags().Set("id", prev) })
	if err := runCmd.Flags().Set("id", base.ID); err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	runCmd.SetContext(ctx)

	// Отказ обязан быть мгновенным. Без правки команда доходит до запуска
	// сервера и блокируется до конца контекста — тест должен падать на этом
	// быстро, а не висеть до таймаута пакета и не оставлять за собой listener.
	done := make(chan error, 1)
	go func() { done <- runServer(runCmd, nil) }()
	select {
	case err = <-done:
	case <-time.After(20 * time.Second):
		cancel()
		<-done
		t.Fatal("run --id клиентской записи не отказал, а продолжил запуск: отказ стоит позже создания базы и listener")
	}
	if err == nil {
		t.Fatal("run --id клиентской записи завершился без ошибки — локальный сервер запущен")
	}
	for _, want := range []string{"подключение к работающему серверу", base.ServerURL} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("в отказе нет %q: %v", want, err)
		}
	}

	// Ни файла базы, ни занятого порта быть не должно: отказ стоит раньше них.
	if _, statErr := os.Stat(legacyDB); statErr == nil {
		t.Errorf("создана локальная база %s — отказ произошёл слишком поздно", legacyDB)
		_ = os.Remove(legacyDB)
	}
	if _, statErr := os.Stat(filepath.Join(home, ".onebase")); statErr != nil {
		t.Errorf("реестр баз пропал: %v", statErr)
	}
}

// Обычная запись реестра тем же путём по-прежнему разбирается: отказ адресный, а
// не «любая база из реестра больше не запускается».
func TestRunResolvesLocalBaseFromRegistry(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)

	store, err := launcher.NewStore()
	if err != nil {
		t.Fatal(err)
	}
	base := &launcher.Base{
		Name: "Локальная", DBType: "sqlite", DBPath: filepath.Join(home, "base.db"),
		ConfigSource: "database", Port: 18099,
	}
	if err := store.Add(base); err != nil {
		t.Fatal(err)
	}

	flag := runCmd.Flags().Lookup("id")
	prev := flag.Value.String()
	t.Cleanup(func() { _ = runCmd.Flags().Set("id", prev) })
	if err := runCmd.Flags().Set("id", base.ID); err != nil {
		t.Fatal(err)
	}

	cfg, err := resolveServerLaunchConfig(runCmd)
	if err != nil {
		t.Fatalf("обычная запись реестра перестала разбираться: %v", err)
	}
	if cfg.dbType != "sqlite" || cfg.sqlitePath != base.DBPath || cfg.port != base.Port {
		t.Errorf("параметры запуска прочитаны неверно: %+v", cfg)
	}
}
