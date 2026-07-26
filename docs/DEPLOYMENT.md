# Развёртывание OneBase на Windows Server (в т.ч. offline)

Практическое руководство для администратора: развернуть OneBase на Windows Server
с нуля, настроить резервное копирование и обновлять сервер **без интернета** —
обновления приносятся на флешке.

Руководство описывает два варианта хранилища:

- **Вариант А — PostgreSQL + конфигурация в БД** (рекомендуется для рабочих баз):
  обновление конфигурации через `onebase deploy`, горячая перезагрузка без рестарта.
- **Вариант Б — SQLite + конфигурация файлами** (небольшие/демо-базы): проще в
  переносе, вся база — один файл `.db`.

Обновление **бинаря** (`onebase update`) и стратегия бэкапов работают одинаково в
обоих вариантах.

## Содержание

1. [Требования](#1-требования)
2. [Установка на Windows Server](#2-установка-на-windows-server)
3. [Первый деплой конфигурации](#3-первый-деплой-конфигурации)
4. [Регистрация и запуск как службы Windows](#4-регистрация-и-запуск-как-службы-windows)
5. [Smoke-тест после установки](#5-smoke-тест)
6. [Стратегия резервного копирования](#6-стратегия-резервного-копирования)
7. [Offline-обновление бинаря через флешку](#7-offline-обновление-бинаря-через-флешку)
8. [Обновление конфигурации без рестарта](#8-обновление-конфигурации-без-рестарта)
9. [Откат](#9-откат)
10. [Диагностика](#10-диагностика)

---

## 1. Требования

- **Windows Server 2012 R2 / 2016 / 2019 / 2022** (или Windows 10/11 для стенда).
- **PowerShell 5.1+** (входит в состав ОС).
- Права **администратора** (для регистрации службы через `sc.exe`).
- Один файл **`onebase.exe`** (CGo-free, зависимостей во время выполнения нет).
- Для варианта А — доступный **PostgreSQL 13+** (может быть на том же сервере).
- Для варианта Б — ничего дополнительно: SQLite встроен в бинарь.

Рекомендуемая раскладка каталогов:

```
C:\onebase\
  bin\onebase.exe          # исполняемый файл (его обновляет update)
  project\                 # исходная конфигурация (вариант Б, и источник для deploy в варианте А)
  data\docflow.db          # база SQLite (вариант Б)
  backups\                 # локальные резервные копии
  logs\                    # логи службы (если перенаправляете вывод)
```

---

## 2. Установка на Windows Server

```powershell
# 1. Каталоги
New-Item -ItemType Directory -Force C:\onebase\bin, C:\onebase\backups, C:\onebase\logs

# 2. Скопировать бинарь (например, с флешки D:\)
Copy-Item D:\onebase\onebase.exe C:\onebase\bin\onebase.exe

# 3. Проверить, что бинарь запускается
C:\onebase\bin\onebase.exe --help | Select-Object -First 1
```

> Установленную версию видно в интерфейсе на экране **«О программе»** (номер сборки,
> дата, коммит).

> **Важно.** Запущенный `onebase.exe` держит файл залоченным: Windows не даст
> перезаписать работающий бинарь. Обновление это учитывает (см. §7): старый файл
> **переименовывается**, новый пишется на освободившееся имя.

---

## 3. Первый деплой конфигурации

### Вариант А — PostgreSQL (конфигурация в БД)

`onebase deploy` создаёт БД при необходимости, разворачивает схему платформы,
загружает YAML-конфигурацию в служебную таблицу `_onebase_config`, **прогоняет
DDL-миграции** и фиксирует версию конфигурации.

```powershell
C:\onebase\bin\onebase.exe deploy `
  --project C:\onebase\project `
  --db "postgres://onebase:secret@localhost:5432/docflow?sslmode=disable" `
  --message "релиз 1.0.0"
```

После деплоя конфигурация живёт в БД — файлы проекта на сервере больше не нужны для
запуска.

### Вариант Б — SQLite (конфигурация файлами)

Схема создаётся из файлов проекта при первом запуске (`migrate` — по желанию заранее):

```powershell
C:\onebase\bin\onebase.exe migrate --project C:\onebase\project --sqlite C:\onebase\data\docflow.db
```

---

## 4. Регистрация и запуск как службы Windows

Зарегистрируйте базу в реестре ibases — тогда имя службы, порт и параметры БД
берутся из одного места, а команды `service install` и `update` работают по `--id`.

```powershell
# Вариант А (PostgreSQL, конфигурация в БД)
C:\onebase\bin\onebase.exe ibases add `
  --name docflow `
  --db "postgres://onebase:secret@localhost:5432/docflow?sslmode=disable" `
  --port 8080 --source database

# Вариант Б (SQLite, конфигурация файлами)
C:\onebase\bin\onebase.exe ibases add `
  --name docflow `
  --sqlite C:\onebase\data\docflow.db `
  --path C:\onebase\project `
  --port 8080 --source file

C:\onebase\bin\onebase.exe ibases list      # посмотреть ID базы
```

Установите службу (от имени администратора). Имя службы по умолчанию —
`onebase-<имя-базы>`, например `onebase-docflow`:

```powershell
C:\onebase\bin\onebase.exe service install --id <ID-базы>
```

Служба стартует автоматически при загрузке сервера. Управление:

```powershell
sc.exe query  onebase-docflow      # статус
sc.exe stop   onebase-docflow      # остановить
sc.exe start  onebase-docflow      # запустить
C:\onebase\bin\onebase.exe service uninstall --name onebase-docflow
```

> Если предпочитаете без реестра — можно указать параметры явно:
> `service install --db "postgres://…" --port 8080 --name onebase-docflow`
> или `service install --sqlite C:\onebase\data\docflow.db --project C:\onebase\project --config-source file --port 8080 --name onebase-docflow`.

### Параметры безопасности окружения

Для production-службы задайте переменные на уровне машины до её запуска:

```powershell
# OneBase стоит за настроенным HTTPS reverse proxy.
[Environment]::SetEnvironmentVariable("ONEBASE_SECURE_COOKIES", "true", "Machine")

# Необязательно: изменить минимальную длину новых паролей (по умолчанию 8 символов).
[Environment]::SetEnvironmentVariable("ONEBASE_MIN_PASSWORD_LENGTH", "12", "Machine")
```

`ONEBASE_SECURE_COOKIES=true` нужен при завершении TLS на reverse proxy: OneBase
не доверяет клиентскому `X-Forwarded-Proto` без конфигурации доверенных proxy.
При прямом TLS атрибут `Secure` устанавливается автоматически.

Пустые пароли по умолчанию запрещены во всех интерфейсах и API. Для явно
изолированного kiosk-режима их можно разрешить
`ONEBASE_ALLOW_EMPTY_PASSWORDS=true`; не используйте этот режим для сервера,
доступного из сети. После изменения переменных перезапустите службу.

---

## 5. Smoke-тест

После любой установки/обновления проверьте, что сервер жив и **база доступна**:

```powershell
# readiness: 200 только если БД отвечает (иначе 503)
Invoke-WebRequest http://127.0.0.1:8080/healthz -UseBasicParsing | Select-Object StatusCode

# liveness: 200, «процесс жив» (БД не проверяется)
Invoke-WebRequest http://127.0.0.1:8080/health  -UseBasicParsing | Select-Object StatusCode
```

`/healthz` → `200 ok` означает: сервер поднялся и БД на связи. Именно эту пробу
использует автооткат при обновлении (§7).

Дополнительно откройте в браузере `http://<сервер>:8080/` и войдите под рабочим
пользователем.

---

## 6. Стратегия резервного копирования

**Что бэкапить:** базу данных (бизнес-данные + пользователи) и, для варианта Б,
файлы проекта. Для варианта А конфигурация уже в БД и её версии хранятся в
`_config_versions`.

### Резервная копия вручную

```powershell
# PostgreSQL → backup_<db>_<дата>.sql.gz (нужен pg_dump в PATH)
C:\onebase\bin\onebase.exe backup --db "postgres://onebase:secret@localhost/docflow" --out C:\onebase\backups

# SQLite → backup_<db>_<дата>.db (атомарный VACUUM INTO, pg_dump не нужен)
C:\onebase\bin\onebase.exe backup --sqlite C:\onebase\data\docflow.db --out C:\onebase\backups
```

### Ежедневный автобэкап

Настройте в блоке `backup:` конфигурации (`config/app.yaml`) или через
Конфигуратор → «Бэкапы». Задание регистрируется как регламентное `AutoBackup`,
успехи/ошибки видны в журнале заданий:

```yaml
backup:
  enabled: true
  schedule: "0 2 * * *"   # каждую ночь в 02:00
  keep_last: 7            # хранить 7 последних, старые удалять
  directory: "C:/onebase/backups"
```

### Offsite-копия

Локальный бэкап не спасает от отказа диска или шифровальщика. Раз в сутки
копируйте свежие файлы `backup_*` на отдельный носитель / сетевую шару / в другой
офис. Пример — планировщик Windows, задача раз в сутки:

```powershell
# Скопировать сегодняшние бэкапы на сетевую шару (или на подключённую флешку)
$today = Get-Date -Format "yyyy-MM-dd"
Copy-Item "C:\onebase\backups\backup_*_$today*" "\\backup-nas\onebase\" -Force
```

> Проверяйте восстановимость: раз в месяц восстановите свежий бэкап в тестовую базу
> (`onebase restore …`) и убедитесь, что она открывается.

---

## 7. Offline-обновление бинаря через флешку

Одна команда вместо ручных «останови службу → скопируй exe → запусти → проверь
логи». Команда сама останавливает службу, подменяет бинарь (сохраняя старый
рядом), запускает службу, опрашивает `/healthz` и **откатывается**, если новый
бинарь не поднялся.

```powershell
# С флешки D:\ ; --id берёт имя службы и порт из реестра
C:\onebase\bin\onebase.exe update --from D:\onebase-v1.1.0.zip --sha256 <ожидаемый-hex> --id <ID-базы>

# С проверкой контрольной суммы и увеличенным ожиданием готовности
C:\onebase\bin\onebase.exe update `
  --from D:\onebase-v1.1.0.zip --id <ID-базы> `
  --sha256 <ожидаемый-hex> --timeout 60s
```

Что происходит по шагам:

1. Из `--from` берётся бинарь: `.zip` (внутри ищется `onebase.exe`) или сам `.exe`.
2. Обязательная `--sha256` сверяется до остановки службы.
3. Служба останавливается (ожидание состояния `STOPPED`).
4. Текущий `onebase.exe` атомарно копируется в `onebase.exe.old`, на его место
   пишется новый бинарь.
5. Служба запускается; `/healthz` должен ответить версией именно нового бинаря
   до `--timeout` (30 с по умолчанию).
6. **Успех** → `.old` удаляется, выводится «обновление применено».
   **Неудача** (не поднялся / БД недоступна) → служба останавливается, из `.old`
   восстанавливается прежний бинарь, служба снова запускается; команда сообщает,
   что откат выполнен.

Флаги:

| Флаг | Назначение |
|------|-----------|
| `--from` | путь к `.zip` или `.exe` (обязателен) |
| `--id` / `--service` | база из реестра / явное имя службы |
| `--sha256` | ожидаемая контрольная сумма файла обновления (`.zip` или `.exe`), обязательна |
| `--target` | какой файл заменять (по умолчанию — текущий `onebase.exe`) |
| `--port` / `--healthz-url` | куда стучаться пробой (по умолчанию порт базы) |
| `--timeout` | сколько ждать `200` от `/healthz` (по умолчанию 30с) |

> Release публикует рядом файл `<артефакт>.sha256`; в `--sha256` передайте первый
> столбец из него. Для локальной проверки: `Get-FileHash D:\onebase-v1.1.0.zip -Algorithm SHA256`.

---

## 8. Обновление конфигурации без рестарта

**Только вариант А (PostgreSQL, конфигурация в БД).** Если служба запущена с
`--watch`, сервер сам подхватывает новую конфигурацию после `deploy` — без
перезапуска и разрыва сеансов.

Служба должна быть установлена с флагом `--watch`. Если она уже стоит без него —
переустановите:

```powershell
C:\onebase\bin\onebase.exe service uninstall --name onebase-docflow
C:\onebase\bin\onebase.exe service install --id <ID-базы> --watch
```

Затем принесите новую конфигурацию и задеплойте:

```powershell
C:\onebase\bin\onebase.exe deploy `
  --project D:\project-v2 `
  --db "postgres://onebase:secret@localhost/docflow" `
  --message "релиз 1.1.0"
```

Через несколько секунд сервер применит изменения (в логе — «конфигурация
перезагружена из БД»). Схему БД миграции деплоя приводят в порядок **до** создания
версии, поэтому перезагрузка безопасна.

---

## 9. Откат

### Откат бинаря

Автоматически выполняется при неудачном `update` (§7). Вручную — если проблему
заметили позже:

```powershell
sc.exe stop onebase-docflow
Move-Item C:\onebase\bin\onebase.exe     C:\onebase\bin\onebase.exe.bad -Force
Move-Item C:\onebase\bin\onebase.exe.old C:\onebase\bin\onebase.exe -Force
sc.exe start onebase-docflow
```

### Откат конфигурации (вариант А)

Каждый `deploy`/rollback создаёт версию в `_config_versions`:

```powershell
# Список версий (свежие сверху)
C:\onebase\bin\onebase.exe config versions --db "postgres://onebase:secret@localhost/docflow"

# Разница между версиями
C:\onebase\bin\onebase.exe config diff <версия-до> <версия-после> --db "postgres://…"

# Откатить конфигурацию к нужной версии (создаётся новая версия-откат)
C:\onebase\bin\onebase.exe config rollback <версия> --db "postgres://…" --message "откат к 1.0.0"
```

С `--watch` откат конфигурации подхватится сервером без рестарта.

### Откат данных (восстановление БД)

Полное восстановление требует **остановленной** базы (файл/схема перезаписываются):

```powershell
sc.exe stop onebase-docflow

# PostgreSQL
C:\onebase\bin\onebase.exe restore --db "postgres://onebase:secret@localhost/docflow" --file C:\onebase\backups\backup_docflow_2026-07-09_02-00-00.sql.gz

# SQLite
C:\onebase\bin\onebase.exe restore --sqlite C:\onebase\data\docflow.db --file C:\onebase\backups\backup_docflow_2026-07-09_02-00-00.db --force

sc.exe start onebase-docflow
```

---

## 10. Диагностика

- **Служба не стартует:** `sc.exe query onebase-docflow`; посмотрите системный
  журнал (`Event Viewer → Windows Logs → System`). Частые причины — недоступна БД
  или занят порт.
- **`/healthz` отдаёт 503:** сервер жив, но БД не отвечает — проверьте PostgreSQL /
  путь к файлу SQLite.
- **`update` откатился:** новый бинарь не прошёл `/healthz` за `--timeout`.
  Увеличьте `--timeout`, проверьте `/healthz` вручную, посмотрите вывод команды.
- **Занятый порт:** измените порт базы (`ibases`) и переустановите службу.
- **Проверка бинаря без службы:** запустите вручную `onebase run --id <ID>`
  (порт берётся из реестра) и смотрите вывод в консоли.

Соответствующие возможности перечислены в [docs/features.md](features.md)
(разделы «Бэкап…», «Проба готовности `/healthz`», «Offline-обновление…»,
«Горячая перезагрузка…»).
