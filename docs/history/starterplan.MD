 Here is Claude's plan:                                                                                                                                                                                                    
╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌
 Стартер onebase — 1С-образный лаунчер информационных баз                                                                                                                                                                  
                                                                                                                                                                                                                           
 Context                                                                                                                                                                                                                   

 В onebase сегодня нет аналога «Запуск 1С:Предприятие». Каждый запуск — ручной onebase dev/run --project … --db …, путь и DSN надо помнить. Пользователь хочет небольшой десктопный лаунчер, максимально близкий к
 1С-модели: одна «информационная база» = совокупность (конфигурация + данные + пользователи) в одной PostgreSQL-базе, управляемая из окна-списка.

 Согласованные решения:
 - UI стартера: десктопное окно с embedded webview (WebView2 на Windows 11). Стартер внутри поднимает маленький HTTP-сервер и отдаёт туда HTML. Бинарь остаётся один, без Electron.
 - Хранилище списка баз: глобально в профиле пользователя — ~/.onebase/ibases.yaml (Windows: %USERPROFILE%\.onebase\ibases.yaml).
 - Функции v1: Запустить, Добавить/Изменить/Удалить, Создать новую базу, Выгрузить/Загрузить конфигурацию («Конфигуратор»), Обновить БД (миграция).
 - Порт: настраивается per-база, хранится в записи; флаг --port в dev/run. Дефолт 8080. Можно держать несколько баз одновременно на разных портах.
 - Режимы конфигурации (двойной):
   - file — существующий git-friendly режим, папка на диске, для разработки платформы и типовых решений в репозитории.
   - database — 1С-подобный режим: конфа хранится в служебной таблице самой БД, путь к файлам из UI скрыт. Дефолт при создании базы из стартера.
 - Пользователи: таблица _users в каждой базе. Если пусто — вход без авторизации; если есть записи — требуется логин. Логин-диалог показывается и в стартере (перед открытием webview-вкладки), и в самом веб-UI как
 fallback при прямом заходе по URL. Общий auth-endpoint, один и тот же механизм сессий.
 - «Конфигуратор» (в database-режиме): выгрузка текущей конфы во временную папку ~/.onebase/workspace/<base-id>/, открытие в проводнике ОС; обратная загрузка + миграция.

 Архитектурная схема

                                 ~/.onebase/ibases.yaml
                                       (список баз)
                                           ▲
                                           │
 ┌─────────────────────────────┐    ┌──────┴────────┐    ┌────────────────────────────┐
 │  onebase start              │    │   store.go    │    │  onebase run/dev           │
 │  ├─ HTTP 127.0.0.1:<rand>   │◄──►│   (launcher)  │    │  ├─ --config-source=file   │
 │  ├─ webview-окно            │    └──────┬────────┘    │  │  + --project <dir>      │
 │  ├─ handlers: CRUD / start  │           │             │  └─ --config-source=db     │
 │  │  / stop / migrate /      │           │             │     (читает конфу из       │
 │  │  config-export/import    │           │             │      _onebase_config,      │
 │  └─ форкает → ──────────────┼───────────┴────────────►│      раскладывает в temp)  │
 └─────────────────────────────┘                         └────────────┬───────────────┘
                                                                      │
                                                      ┌───────────────┼───────────────────┐
                                                      ▼               ▼                   ▼
                                               _onebase_config     _users             прикладные
                                               (конфа-в-БД)        (auth)             таблицы

 Критические файлы

 Новые файлы

 Лаунчер

 - internal/launcher/store.go — type Base struct { ID, Name, ConfigSource, Path, DB string; Port int; Created, LastOpened time.Time }; Store с Load/Save/List/Get/Add/Update/Remove. Путь файла через os.UserHomeDir().
 YAML через ту же библиотеку, что уже используется в internal/project/loader.go (проверить — скорее всего gopkg.in/yaml.v3, не добавлять новую зависимость).
 - internal/launcher/runner.go — type Runner struct { procs map[string]*managedProc }:
   - Start(base Base) error — форкает os.Executable() с run --db <DSN> --port <P> и либо --project <path> (для file), либо --config-source=database --base-id=<id> (для database). Логи пишутся в
 ~/.onebase/logs/<base-id>.log. На Windows cmd.SysProcAttr.CreationFlags |= CREATE_NEW_PROCESS_GROUP для корректного kill.
   - Stop(baseID), IsRunning(baseID), Migrate(base) (синхронный onebase migrate …).
   - ExportConfig(base, targetDir), ImportConfig(base, sourceDir) — выгрузка/загрузка, завязанные на режим.
 - internal/launcher/server.go — chi-роутер:
   - GET / — список баз
   - GET /bases/new + POST /bases — форма создания (поля: имя, режим, путь (только для file), DSN, порт, «создать с нуля» checkbox)
   - GET /bases/{id}/edit + POST /bases/{id} (метод-оверрайд PUT)
   - POST /bases/{id}/delete
   - POST /bases/{id}/start → JSON {url, requires_auth} (открывается в новой webview-вкладке через window.open); если requires_auth=true, фронт сначала показывает форму логина → POST /bases/{id}/auth/login → при успехе
  /auth/bootstrap?token=… открывает /ui с уже выставленной session-cookie.
   - POST /bases/{id}/stop
   - POST /bases/{id}/migrate — показывает вывод миграции
   - POST /bases/{id}/config/export — выгрузка в ~/.onebase/workspace/<base-id>/ + explorer / xdg-open / open через internal/launcher/openpath.go
   - POST /bases/{id}/config/import — выбор папки (через стандартный file-picker в webview) → ImportConfig → автоматическая миграция.
 - internal/launcher/handlers.go — реализация выше.
 - internal/launcher/templates.go — html/template в стиле 1С-стартера (серо-голубой, список, кнопки-пиктограммы). Шаблоны: page_index, page_form, page_migrate_output, page_login, page_import_result.
 - internal/launcher/window_webview.go (build-tag !nowebview) — OpenWindow(url, title string) error через github.com/webview/webview_go. Блокирующий, нуждается в runtime.LockOSThread.
 - internal/launcher/window_nowebview.go (build-tag nowebview) — печатает URL, блокируется до SIGINT.
 - internal/launcher/openpath.go — кроссплатформенный «открыть папку в проводнике».

 CLI

 - internal/cli/start.go — команда onebase start (без флагов).
 - internal/cli/ibases.go — ibases list|add|edit|remove (тот же launcher.Store, без webview).

 Конфигурация-в-БД

 - internal/configdb/repo.go — type Repo struct { db *storage.DB }; методы EnsureSchema(ctx) (создаёт _onebase_config), ExportToDir(ctx, dir), ImportFromDir(ctx, dir), List(ctx). Таблица:
 CREATE TABLE IF NOT EXISTS _onebase_config (
     path TEXT PRIMARY KEY,
     content BYTEA NOT NULL,
     updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
 );
 - internal/project/loader.go — в Load(dir) поведение не меняется; добавить LoadFromDB(ctx, db) (*Project, error): выгружает конфу из _onebase_config во временную папку через configdb.Repo, затем вызывает обычный
 Load(tmpDir). Временную папку создаём через os.MkdirTemp(userCacheDir, "onebase-cfg-"); освобождаем на Project.Close() (новый метод).

 Аутентификация

 - internal/auth/users.go — type Repo struct { db *storage.DB }; EnsureSchema(ctx) создаёт _users и _sessions:
 CREATE TABLE IF NOT EXISTS _users (
     id UUID PRIMARY KEY,
     login TEXT UNIQUE NOT NULL,
     password_hash BYTEA NOT NULL,
     full_name TEXT,
     is_admin BOOLEAN NOT NULL DEFAULT FALSE,
     created_at TIMESTAMPTZ NOT NULL DEFAULT now()
 );
 CREATE TABLE IF NOT EXISTS _sessions (
     token TEXT PRIMARY KEY,
     user_id UUID NOT NULL REFERENCES _users(id) ON DELETE CASCADE,
     expires_at TIMESTAMPTZ NOT NULL
 );
 - Методы: Create/Update/Delete/List/Authenticate(login, password), CreateSession(userID), LookupSession(token). Хэши через golang.org/x/crypto/bcrypt (добавить в go.mod).
 - internal/auth/middleware.go — func (r *Repo) Middleware(next http.Handler) http.Handler: если в _users нет записей → пропускает; иначе проверяет cookie onebase_session → если невалидна → для Accept: text/html
 редирект на /login?return=<url>, для остальных — 401 JSON.
 - internal/auth/handlers.go — /login (GET форма, POST проверка → set-cookie → redirect), /logout, /auth/status (GET {requires_auth: bool}), /auth/login (POST JSON — для стартера), /auth/bootstrap?token=… (GET —
 выставляет cookie, редиректит на /ui; используется стартером после успешного /auth/login чтобы передать сессию в webview-вкладку).
 - Админка пользователей в internal/ui/ — новые роуты /ui/admin/users, /ui/admin/users/new, ... — CRUD. Доступно только для is_admin=true (проверяется в хэндлере).

 Модификации существующих файлов

 - internal/api/server.go:21-42:
   - New(reg, store, interp, authRepo *auth.Repo, port int) *Server — добавить authRepo и port.
   - Заменить :8080 на fmt.Sprintf(":%d", port).
   - Подключить auth.Middleware на всех роутах, кроме /login, /logout, /auth/*, /health.
 - internal/cli/dev.go:22-88, internal/cli/run.go:20-70:
   - Новые флаги: --port int (дефолт 8080), --config-source string (дефолт file), --base-id string (нужен только для database).
   - Если config-source=database → вместо project.Load(dir) → project.LoadFromDB(ctx, db); --project становится опциональным. После Load обязательно вызвать project.Close() в defer.
   - Перед api.New вызывать authRepo.EnsureSchema(ctx) и configdb.EnsureSchema(ctx) (в режиме database).
 - internal/cli/migrate.go:13-58:
   - Те же флаги --config-source/--base-id.
   - В режиме database: сначала LoadFromDB (он сам создаст _onebase_config при необходимости), потом обычная миграция прикладных таблиц.
 - internal/cli/init.go — выделить скаффолдинг в project.Scaffold(dir, name string) error (internal/project/scaffold.go). Команда init и launcher-хэндлер «создать новую базу» дергают одну функцию.
 - internal/cli/root.go:22-24 — AddCommand(initCmd, devCmd, runCmd, migrateCmd, buildCmd, startCmd, ibasesCmd).
 - internal/project/loader.go — LoadConfig(dir) (*AppConfig, error) для чтения config/app.yaml (используется для автоподстановки имени).
 - go.mod — добавить:
   - github.com/webview/webview_go (webview-окно)
   - golang.org/x/crypto (bcrypt)
   - Всё остальное (chi, yaml) уже есть.

 Поток данных при создании базы из стартера (режим database)

 1. Пользователь: «+ Создать новую базу» → форма → «Создать с нуля».
 2. Handler: project.Scaffold(tmpDir, name) в os.MkdirTemp.
 3. Handler: storage.Connect(dsn) → auth.Repo.EnsureSchema → configdb.Repo.EnsureSchema → configdb.Repo.ImportFromDir(tmpDir).
 4. Handler: db.Migrate(entities) для прикладных таблиц (читая конфу только что загруженную в _onebase_config через project.LoadFromDB).
 5. store.Add(Base{ConfigSource: "database", DB: dsn, Port: 8080, ...}).
 6. tmpDir удаляем.

 Поток «Конфигуратор»

 1. «Выгрузить» → configdb.Repo.ExportToDir(~/.onebase/workspace/<base-id>/), затем openpath.Open(dir).
 2. Пользователь правит в VS Code / любом редакторе.
 3. «Загрузить» → выбирает ту же папку в file-picker → configdb.Repo.ImportFromDir(dir) → db.Migrate(...) прикладных таблиц по новой конфе.

 Переиспользование существующего кода

 - Запуск сервера базы: launcher-runner форкает os.Executable() с onebase run ... — ничего не дублируем.
 - Миграция: launcher форкает onebase migrate ....
 - Chi + templates: образцом служат internal/api/server.go, internal/ui/{server,handlers,templates}.go.
 - YAML-парсинг: та же библиотека, что в internal/project/loader.go.
 - Cobra-команды: internal/cli/dev.go как образец.
 - Скаффолдинг: один project.Scaffold, используется командой init и launcher'ом.

 Verification

 - go build -o onebase ./cmd/onebase — на Windows 11 требует CGo (MSVC Build Tools) + WebView2 Runtime (уже встроен в Win 11).
 - go build -tags nowebview -o onebase ./cmd/onebase — без CGo, без webview; onebase start печатает URL.
 - go test ./... — существующие юнит-тесты должны проходить.
 - Новые юнит-тесты:
   - internal/launcher/store_test.go — CRUD ibases.yaml, атомарная запись через temp+rename.
   - internal/configdb/repo_test.go — Export/Import round-trip (integration, требует Postgres).
   - internal/auth/users_test.go — хэш/верификация пароля, middleware с пустой/непустой таблицей.
 - go test -tags=integration ./... — существующий integration_test.go не сломать; добавить прогон обоих режимов file и database.
 - Ручной сквозной сценарий (Windows 11):
   a. onebase start → открывается окно стартера.
   b. «+ Создать новую» → имя «Склад», режим database (по умолчанию), DSN postgres://localhost/sklad_test, порт 8080, чекбокс «Создать с нуля» → появляется в списке.
   c. «Конфигуратор» → «Выгрузить» → папка открывается в Explorer; там лежат catalogs/, documents/ и т.д.
   d. Правим catalogs/Номенклатура.yaml, добавляем поле → «Загрузить» → миграция прошла, в UI базы поле появилось.
   e. «Запустить» → откралась webview-вкладка http://localhost:8080/ui, работает существующий UI.
   f. В админке /ui/admin/users создаём пользователя admin/1234. Logout.
   g. Повторно «Запустить» из стартера → стартер видит requires_auth=true, показывает свой login-диалог → ввод admin/1234 → открывается /ui с уже выставленной сессией.
   h. Если открыть /ui напрямую в другом окне браузера без куки → редирект на /login, после ввода — попадаем в UI. Оба входа работают.
   i. «Остановить» в стартере → процесс базы убивается.
   j. Закрытие окна стартера → уже остановленные базы остаются остановленными; работающие продолжают жить (их контролирует ОС).
 - cat ~/.onebase/ibases.yaml — содержит запись с config_source: database, без локального пути.
 - onebase ibases list — видит ту же запись в CLI.
 - Старый пример работает: onebase dev --project ./examples/simple-erp --db … (это режим file, путь --project обязателен) — регрессий нет.

 Этапы реализации (для трассировки прогресса)

 1. Рефакторинг порта: api.New(..., port int), флаги --port в dev/run.
 2. Скаффолдинг: вынести project.Scaffold, обновить init.go.
 3. Launcher-store: internal/launcher/store.go + onebase ibases ... CLI.
 4. Auth: internal/auth/*, middleware в internal/api/server.go, login-страница в internal/ui, admin-роуты users.
 5. Config-in-DB: internal/configdb/repo.go, project.LoadFromDB, флаги --config-source/--base-id в run/dev/migrate.
 6. Launcher-runner: форк onebase run/migrate, kill по baseID, логи.
 7. Launcher-server + templates + handlers: CRUD UI, start/stop, export/import конфы.
 8. Webview-окно: window_webview.go + window_nowebview.go, onebase start.
 9. Тесты: юнит на store/configdb/auth, прогон e2e-сценария руками.

 Сознательно НЕ входит в v1

 - Встроенный редактор конфы в окне стартера (Monaco/CodeMirror) — только выгрузка/загрузка в проводник.
 - Серверный режим подключения (tcp к внешнему onebase-серверу) — только локально.
 - Версионирование конфигурации в БД, откаты, сравнение — одна активная версия.
 - Трей-иконка, автостарт, сворачивание в трей.
 - Детальная ролевая модель: только is_admin vs обычный пользователь.
 - Миграции служебных таблиц между версиями платформы — CREATE TABLE IF NOT EXISTS достаточно для v1, апгрейды схемы _users/_onebase_config — отдельная задача.