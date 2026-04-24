# onebase

Открытая бизнес-платформа с 1С-подобным DSL, написанная на Go.

Одна «информационная база» = конфигурация (справочники, документы, регистры, отчёты) + данные в PostgreSQL + пользователи. Управляется через лаунчер — аналог «Запуск 1С:Предприятие».

---

## Установка (Windows)

### Вариант 1 — скачать и запустить

1. Скачать архив со страницы [Releases](https://github.com/ivanarama/onebase/releases/latest):  
   `onebase-windows-amd64.zip`
2. Распаковать в любую папку, например `C:\onebase\`
3. Двойной клик на **`onebase-gui.exe`** — откроется лаунчер в браузере

Что в архиве:
```
onebase-gui.exe   ← двойной клик — запускает лаунчер в браузере
onebase.exe       ← CLI-инструмент (для терминала и сервера)
examples/         ← пример проекта simple-erp
```

### Вариант 2 — установщик PowerShell

```powershell
irm https://raw.githubusercontent.com/ivanarama/onebase/main/install.ps1 | iex
```

Кладёт `onebase.exe` в `%USERPROFILE%\.onebase\bin\` и добавляет в PATH.

### Linux / macOS

```bash
tar xzf onebase-linux-amd64.tar.gz
sudo mv onebase-linux-amd64/onebase /usr/local/bin/
onebase start
```

### Требования

- PostgreSQL 14+ (локально или сетевой)
- Windows 10/11 с Edge (для нативного окна) — или любой браузер

---

## Быстрый старт

### 1. Запустить лаунчер

```
Запустить.vbs   ← двойной клик в проводнике
```

или из терминала:

```
onebase start
```

### 2. Создать базу

В лаунчере нажать **«+ Добавить»**:

| Поле | Пример |
|---|---|
| Наименование | Склад |
| Тип конфигурации | В базе данных *(рекомендуется)* |
| Строка подключения | `postgres://localhost/sklad?sslmode=disable` |
| Порт | 8080 |
| Создать с нуля | ✓ |

Нажать **«Добавить»** — база зарегистрирована, конфигурация создана автоматически.

### 3. Запустить базу

Выбрать базу в списке → **«1С:Предприятие»** — откроется веб-интерфейс базы.

### 4. Создать пользователей (опционально)

В веб-интерфейсе базы: **Администрирование → Пользователи → + Добавить**.  
Пока пользователей нет — вход без пароля. После создания первого пользователя — вход только авторизовавшись.

---

## Команды CLI

| Команда | Описание |
|---|---|
| `onebase start` | Открыть лаунчер информационных баз |
| `onebase ibases list` | Список зарегистрированных баз |
| `onebase ibases add --name ... --db ... --port ...` | Зарегистрировать базу |
| `onebase ibases remove --id ...` | Удалить регистрацию |
| `onebase dev --project ./my-app --db ...` | Dev-сервер с hot reload (файловый режим) |
| `onebase run --project ./my-app --db ...` | Production-сервер (файловый режим) |
| `onebase migrate --project ./my-app --db ...` | Применить схему БД |
| `onebase init [dir]` | Создать заготовку нового проекта |

---

## Архитектура: платформа / конфигурация / база

Аналогия с 1С:

| 1С | onebase | Физически |
|---|---|---|
| Платформа | `onebase.exe` | Go-бинарник: рантайм, DSL, REST API, лаунчер |
| Конфигурация | Папка (`catalogs/`, `documents/`, ...) или строки в БД | YAML + `.os` скрипты |
| Информационная база | PostgreSQL | Прикладные таблицы + `_users` + `_onebase_config` |

### Два режима конфигурации

**Файловый** (`config_source: file`) — для разработки:
- Конфигурация в папке на диске под git
- `onebase dev --project ./my-app --db ...` с hot reload

**В базе данных** (`config_source: database`) — для пользователей:
- Конфигурация хранится в таблице `_onebase_config` в PostgreSQL
- Редактирование через «Конфигуратор» в лаунчере (Выгрузка → правка → Загрузка)
- Путь к папке скрыт, база — самодостаточная сущность

---

## Сборка из исходников

Требуется Go 1.23+.

```bash
# Консольная версия (без CGo)
go build -o onebase ./cmd/onebase

# GUI-версия с нативным окном (требует CGo и WebView2 на Windows)
go build -tags webview -ldflags="-H windowsgui" -o onebase-gui ./cmd/onebase
```

```bash
# Тесты
go test ./...

# Интеграционные тесты (требуется PostgreSQL)
export TEST_DATABASE_URL=postgres://localhost/onebase_test
go test -tags=integration ./...
```

---

## Пример проекта

`examples/simple-erp/` — складской учёт: поступление и списание товаров.

```bash
onebase dev --project ./examples/simple-erp --db "postgres://localhost/onebase_dev?sslmode=disable"
# → http://localhost:8080/ui
```
