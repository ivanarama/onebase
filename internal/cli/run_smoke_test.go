package cli

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"path/filepath"
	"testing"
	"time"

	"github.com/ivantit66/onebase/internal/storage"
)

// Smoke-тест сборки приложения: поднимает сервер ТЕМ ЖЕ путём, что и команда
// `onebase run` — через runServer, а не собирая обвязку заново, — и проверяет,
// что подсистемы действительно поднялись.
//
// Зачем именно так. runServer поднимает больше двух десятков подсистем подряд, и
// до этого теста его не вызывал НИ ОДИН тест: единственная сквозная проверка
// сборки (integration_test.go) собирает сервер вручную, живёт за тегом
// integration и требует PostgreSQL, то есть в обычном `go test ./...` не идёт.
// Поэтому тест намеренно без тега и на SQLite — он обязан работать в джобе build
// на каждом PR.
//
// Что он ловит. При переносе кода между пакетами (вынос инициализации в
// internal/app, шаг 1 ARCH-01) git мержит построчно и не понимает, что функция
// переехала: чужая правка, добавляющая подъём подсистемы, может остаться в
// старом файле и не доехать до нового. Результат компилируется, go vet молчит,
// остальные тесты зелёные — подсистема просто не поднята. Ровно этот отказ здесь
// и детектируется: у каждой подсистемы есть внешний признак (ответ HTTP или
// созданная таблица), и потеря вызова инициализации гасит признак.
//
// Проверка идёт через публичную точку входа (cobra-команду run), поэтому
// переживёт шаг 1: после выноса в internal/app runServer станет тонкой обёрткой,
// а тест продолжит проверять ровно то же самое.
func TestRunServerBootsFullApplication(t *testing.T) {
	base, dbPath, stop := bootSmokeServer(t)

	// 1. Внешне наблюдаемые признаки подсистем HTTP-уровня.
	for _, probe := range []struct {
		path      string
		subsystem string
	}{
		{"/health", "HTTP-сервер поднят (api.New + ListenAndServe)"},
		{"/healthz", "соединение с БД живо (healthz пингует базу)"},
		{"/", "UI смонтирован (uiSrv.Mount)"},
		{"/login", "страница входа смонтирована (auth)"},
		{"/catalogs/Контрагент", "REST смонтирован и реестр конфигурации загружен"},
	} {
		resp, err := http.Get(base + probe.path)
		if err != nil {
			t.Errorf("GET %s: %v\nне поднялось: %s", probe.path, err, probe.subsystem)
			continue
		}
		_ = resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			t.Errorf("GET %s вернул %d, ожидался 200\nне поднялось: %s",
				probe.path, resp.StatusCode, probe.subsystem)
		}
	}

	// Останавливаем до чтения схемы: так проверяется ещё и штатное завершение
	// (BeginQuiesce -> schedCancel -> srv.Shutdown), а база освобождается.
	stop(t)

	// 2. Состав схемы. Каждая таблица — след конкретного вызова инициализации в
	// runServer. Если вызов потерялся при переносе кода, таблицы не будет, и
	// сообщение назовёт подсистему, а не просто «нет таблицы».
	//
	// ДОБАВЛЯЕШЬ ПОДСИСТЕМУ, СОЗДАЮЩУЮ ТАБЛИЦУ, — ДОПИШИ СЮДА СТРОКУ.
	// Иначе её потерю при следующем переносе кода не заметит никто.
	wantTables := map[string]string{
		"_users":             "аутентификация (authRepo.EnsureSchema)",
		"_roles":             "роли (authRepo.EnsureSchema + auth.SyncRoles)",
		"_audit":             "аудит (db.EnsureAuditSchema)",
		"_exchange_changes":  "планы обмена (db.EnsureExchangeSchema)",
		"_intake_log":        "приём сообщений (db.EnsureIntakeSchema)",
		"_accounts":          "план счетов (db.EnsureAccountsTable)",
		"_ext_printforms":    "внешние печатные формы (extform.New)",
		"_ext_reports":       "внешние отчёты (extform.NewReports)",
		"_ext_processors":    "внешние обработки (extform.NewProcessors)",
		"_scheduled_runs":    "планировщик (db.EnsureScheduledRunsTable)",
		"_attachments":       "вложения (db.EnsureAttachmentTable)",
		"_blobs":             "хранилище блобов (db.EnsureBlobTable)",
		"_constants":         "константы (db.MigrateConstants)",
		"контрагент":         "сущности конфигурации (db.Migrate)",
		"поступление_товары": "табличные части (db.Migrate)",
		"рег_товарноедвижение": "регистры накопления (db.MigrateRegisters)",
	}

	got := smokeTableSet(t, dbPath)
	for table, subsystem := range wantTables {
		if !got[table] {
			t.Errorf("после старта нет таблицы %q — не поднялась подсистема: %s", table, subsystem)
		}
	}
}

// bootSmokeServer поднимает сервер через runServer и возвращает базовый URL,
// путь к файлу БД и функцию остановки.
func bootSmokeServer(t *testing.T) (base, dbPath string, stop func(*testing.T)) {
	t.Helper()

	dbPath = filepath.Join(t.TempDir(), "smoke.db")

	// Порт выбираем сами: runServer биндит 0, но фактический адрес наружу не
	// отдаёт — узнать его после старта неоткуда.
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := l.Addr().(*net.TCPAddr).Port
	if err := l.Close(); err != nil {
		t.Fatal(err)
	}

	// runCmd — глобальная команда, её флаги «липкие». Берём именно её, а не
	// свежий cobra.Command с переписанным набором флагов: так тест использует
	// настоящее определение команды и не разъедется с ним, когда флаг добавят.
	// Прежние значения возвращаем, чтобы не влиять на соседние тесты пакета.
	for _, kv := range [][2]string{
		{"project", filepath.Join("..", "..", "examples", "minimal")},
		{"sqlite", dbPath},
		{"port", fmt.Sprint(port)},
		{"host", "127.0.0.1"},
	} {
		name, value := kv[0], kv[1]
		flag := runCmd.Flags().Lookup(name)
		if flag == nil {
			t.Fatalf("у команды run нет флага --%s", name)
		}
		prev := flag.Value.String()
		t.Cleanup(func() { _ = runCmd.Flags().Set(name, prev) })
		if err := runCmd.Flags().Set(name, value); err != nil {
			t.Fatalf("флаг --%s: %v", name, err)
		}
	}

	ctx, cancel := context.WithCancel(context.Background())
	// cobra подставляет контекст только в Execute; без SetContext runServer
	// уронит signal.NotifyContext на nil-родителе.
	runCmd.SetContext(ctx)

	done := make(chan error, 1)
	go func() { done <- runServer(runCmd, nil) }()

	stopped := false
	stop = func(t *testing.T) {
		t.Helper()
		if stopped {
			return
		}
		stopped = true
		cancel()
		select {
		case err := <-done:
			// Отмена контекста — штатный путь остановки, ошибки быть не должно.
			if err != nil {
				t.Errorf("runServer завершился с ошибкой: %v", err)
			}
		case <-time.After(60 * time.Second):
			t.Error("runServer не завершился за 60с после отмены контекста")
		}
	}
	t.Cleanup(func() { cancel() })

	base = fmt.Sprintf("http://127.0.0.1:%d", port)

	// Готовность — только по полученному HTTP-ответу: listener открывается
	// раньше, чем начинается обслуживание, поэтому успешный TCP-connect ещё
	// ничего не значит.
	deadline := time.Now().Add(60 * time.Second)
	for {
		if time.Now().After(deadline) {
			stop(t)
			t.Fatal("сервер не ответил на /health за 60с")
		}
		resp, err := http.Get(base + "/health")
		if err == nil {
			_ = resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				return base, dbPath, stop
			}
		}
		select {
		case err := <-done:
			t.Fatalf("сервер завершился до готовности: %v", err)
		case <-time.After(50 * time.Millisecond):
		}
	}
}

// smokeTableSet читает имена таблиц из файла БД, оставшегося после старта.
func smokeTableSet(t *testing.T, dbPath string) map[string]bool {
	t.Helper()

	ctx := context.Background()
	db, err := storage.ConnectSQLite(ctx, dbPath)
	if err != nil {
		t.Fatalf("открыть БД после старта: %v", err)
	}
	defer db.Close()

	rows, err := db.Query(ctx, "SELECT name FROM sqlite_master WHERE type='table'")
	if err != nil {
		t.Fatalf("список таблиц: %v", err)
	}
	defer rows.Close()

	set := make(map[string]bool)
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			t.Fatalf("scan: %v", err)
		}
		set[name] = true
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("обход таблиц: %v", err)
	}
	return set
}
