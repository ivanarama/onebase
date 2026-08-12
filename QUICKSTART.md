# Быстрый старт onebase

## Что такое onebase

Открытая бизнес-платформа для учётных задач.  
Одна **информационная база** = метаданные (справочники, документы, регистры) + данные в SQLite или PostgreSQL.  
Метаданные описываются в YAML-файлах, бизнес-логика — на простом DSL.

> 1С, 1С:Предприятие — товарные знаки ООО «1С». Проект разрабатывается независимо и не аффилирован с ООО «1С».

---

## Путь 1 — Через лаунчер (без командной строки)

Подходит для быстрого старта и разработки конфигурации через редактор файлов.

### Шаг 1. Скачать и запустить

1. Скачайте архив со страницы [Releases](https://github.com/ivanarama/onebase/releases/latest):  
   `onebase-windows-amd64.zip`
2. Распакуйте в любую папку, например `C:\onebase\`
3. Двойной клик на **`onebase-gui.exe`**

Откроется лаунчер информационных баз.

### Шаг 2. Убедиться, что PostgreSQL запущен

Лаунчер создаст базу данных автоматически, но PostgreSQL должен быть установлен и запущен.  
Строка подключения по умолчанию: `postgres://postgres:пароль@localhost/имя_бд?sslmode=disable`

### Шаг 3. Создать новую базу

Нажмите **«+ Добавить»** и заполните форму:

| Поле | Пример |
|---|---|
| Наименование | Мой склад |
| Тип конфигурации | **Файловый (разработка)** |
| Путь к папке | `C:\мои-проекты\склад` |
| Строка подключения | `postgres://postgres:1234@localhost/склад_dev?sslmode=disable` |
| Порт | 8080 |
| ☑ Создать структуру папок | включить |

Нажмите **«Добавить»**.

**Что произойдёт автоматически:**
- Создастся база данных `склад_dev` в PostgreSQL
- Создастся папка `C:\мои-проекты\склад\` со структурой:

```
склад/
├── config/
│   └── app.yaml          ← имя приложения
├── catalogs/
│   └── контрагент.yaml   ← пример справочника
├── documents/
│   └── счёт.yaml         ← пример документа
├── registers/            ← регистры накопления
├── reports/              ← отчёты
└── src/
    └── счёт.os           ← пример бизнес-логики
```

### Шаг 4. Запустить базу

Выберите базу в списке → нажмите **«Предприятие»**.  
Откроется веб-интерфейс в браузере: **http://localhost:8080/ui**

Нужно второе окно под другим пользователем — нажмите **«Новое окно»**: база откроется
во внешнем Chromium-браузере с изолированным профилем, и вход в нём не заденет
остальные окна. Работает и из обычного лаунчера, и из `onebase-gui.exe`.

### Шаг 5. Редактировать конфигурацию

Откройте папку проекта в любом редакторе (VS Code, Notepad++).  
Добавляйте и изменяйте YAML-файлы — база перезагружает конфигурацию **автоматически** при каждом сохранении файла.

---

## Путь 2 — Через командную строку

Подходит для CI/CD, автоматизации и разработки без GUI.

### Требования

```
Go 1.26+       → https://go.dev/dl/                     (только для сборки из исходников)
PostgreSQL 14+ → https://www.postgresql.org/download/  (только для многопользовательского режима; для SQLite не нужен)
```

### Установка

```bash
git clone https://github.com/ivanarama/onebase
cd onebase
go build -o onebase.exe ./cmd/onebase
```

### Создать проект

```bash
# Пустой проект
onebase init ./склад

# Из готового шаблона (рекомендуется)
onebase init --list-templates          # посмотреть доступные шаблоны
onebase init --template warehouse ./склад   # склад с проведением и отчётами
onebase init --template tasks     ./задачи  # таск-трекер
onebase init --template crm       ./crm     # мини-CRM
onebase init --template finance   ./finance # домашние финансы
```

### Запустить в режиме разработки

```bash
onebase dev \
  --project ./склад \
  --db "postgres://postgres:1234@localhost/склад_dev?sslmode=disable" \
  --open
```

`--open` откроет **http://localhost:8080/ui** сам, когда сервер будет готов
отвечать. Дальше правьте конфигурацию и сохраняйте файлы — открытая страница
перечитает себя без F5. Вместо `--db` можно взять SQLite: `--sqlite склад_dev.db`.

---

## Структура конфигурации

### Справочник — `catalogs/товар.yaml`

```yaml
name: Товар
fields:
  - name: Наименование
    type: string
  - name: Единица
    type: string
  - name: Цена
    type: number
```

### Документ — `documents/приходная.yaml`

```yaml
name: Приходная
posting: true                    # включает кнопки «Провести» / «Отменить»
numerator:
  prefix: "ПРИ-"
  length: 5
  period: year                   # автонумерация: ПРИ-00001
fields:
  - name: Дата
    type: date
  - name: Поставщик
    type: reference:Контрагент   # ссылка на справочник
tableparts:
  - name: Товары
    fields:
      - name: Товар
        type: reference:Товар
      - name: Количество
        type: number
      - name: Цена
        type: number
      - name: Сумма
        type: number
```

Секция `numerator` автоматически добавляет документу системный строковый
реквизит `Номер`, если он не объявлен в `fields`. Его можно использовать в
формах, `onebase describe` и запросах
(`ВЫБРАТЬ Д.Номер ИЗ Документ.Приходная КАК Д`). Явное поле `Номер` допустимо,
когда нужно задать собственный синоним или другие метаданные.

### Регистр накопления — `registers/остатки.yaml`

```yaml
name: Остатки
dimensions:
  - name: Товар
    type: string
resources:
  - name: Количество
    type: number
```

### Бизнес-логика — `src/приходная.posting.os`

```
Процедура ОбработкаПроведения() Экспорт
  Для Каждого Стр Из this.Товары Цикл
    Дв = Движения("Остатки");
    Дв.Товар       = Стр.Товар;
    Дв.Количество  = Стр.Количество;
    Дв.ВидДвижения = "Приход";
    Дв.Записать();
  КонецЦикла;
КонецПроцедуры
```

---

## Типы полей

| Тип | Описание | Пример |
|---|---|---|
| `string` | Текст | `"ООО Ромашка"` |
| `number` | Число | `42.5` |
| `date` | Дата и время | `"2026-04-24"` |
| `bool` | Да / Нет | `true` |
| `reference:Имя` | Ссылка на справочник или документ | — |
| `enum:Имя` | Значение перечисления | — |

---

## DSL — встроенные объекты и функции

| Объект / функция | Описание |
|---|---|
| `this` | Текущий объект (поля документа или справочника) |
| `this.ТабЧасть` | Коллекция строк табличной части |
| `Движения("Регистр")` | Создать движение в регистре накопления |
| `Error("текст")` | Прервать запись, показать ошибку пользователю |
| `Сообщить("текст")` | Вывод в блок результата (обработки) |
| `НачатьТранзакцию()` | Начать транзакцию PostgreSQL |
| `ЗафиксироватьТранзакцию()` | COMMIT |
| `ОтменитьТранзакцию()` | ROLLBACK |
| `ПрочитатьJSON(строка)` | Распарсить JSON → Соответствие/Массив |
| `ЗаписатьJSON(объект)` | Сериализовать → строка JSON |
| `ВыгрузитьВExcel(данные)` | Получить массив строк → Base64 xlsx |

**HTTP-клиент:**
```
Соединение = Новый HTTPСоединение("api.example.com");
Ответ = Соединение.Получить("/endpoint");
данные = ПрочитатьJSON(Ответ.Тело);
```

**Email:**
```
Письмо = Новый ПочтовоеСообщение;
Письмо.Кому = "user@example.com";
Письмо.Тема = "Уведомление";
Письмо.Тело = "Документ проведён";
Письмо.Отправить();
```

---

## Поиск и пагинация в списках

Каждый список справочника или документа поддерживает:

- **Поиск** — поле над таблицей, мгновенный (без Enter, дебаунс 320 мс)  
  URL: `?q=стол` → ILIKE по всем текстовым полям
- **Фильтры** — `?filter[Статус]=Новая`
- **Пагинация** — `?page=2&limit=50` (по умолчанию 100 строк)
- **Экспорт в Excel** — кнопка «Excel ↓» в заголовке списка

---

## Вложения к документам

На карточке любого документа или справочника есть секция **«Вложения»**:

- Загрузка файлов (drag-and-drop или file picker)
- Скачивание и удаление
- Максимальный размер и допустимые типы настраиваются в `config/app.yaml`:

```yaml
attachments:
  max_file_size_mb: 50
  allowed_types: [pdf, png, jpg, docx, xlsx]
```

Файлы хранятся на диске: `~/.onebase/files/<база>/<объект>/<uuid>`

Конфигурации времён v0.9.3 могут содержать `attachments.storage_type`,
`attachments.storage_location`, `attachments.office_allowed_types` и проектный
блок `russian_post`. Эти ключи принимаются для обратной совместимости, но
платформой не применяются; `onebase check` выводит предупреждения о миграции.
Расширения из `office_allowed_types` перенесите в `allowed_types`, режим
хранения задайте в настройках информационной базы, а настройки интеграции —
в собственных метаданных или константах проекта.

---

## Печатные формы и PDF

Привязываются к документу в `printforms/<имя>.yaml`. Кнопка «Печать» появляется автоматически.

В интерфейсе рядом с «Печать HTML» есть кнопка **«Скачать PDF»** — чистый PDF без браузера.

---

## DSL-печатные формы с макетами

Более мощный вариант печатных форм — `.os`-скрипт с макетом (аналог 1С:ТабличныйДокумент).

### 1. Создать файлы

```
printforms/
├── Накладная.os              # DSL-логика
└── Накладная.layout.yaml     # визуальный макет
```

Первая строка `.os`:
```
// Документ: РеализацияТоваров
```

### 2. Описать макет (layout.yaml)

Макет состоит из **именованных областей** — таблиц с ячейками.  
Ячейка содержит `text` (статичный текст) или `parameter` (заполняется из скрипта):

```yaml
areas:
  Заголовок:
    rows:
      - cells:
          - text: "Накладная №"
            bold: true
          - parameter: Номер
  Строки:
    rows:
      - cells:
          - parameter: Товар
          - parameter: Количество
            align: right
          - parameter: Сумма
            align: right
  Итог:
    rows:
      - cells:
          - text: "Итого:"
            bold: true
            colspan: 2
          - parameter: Итого
            bold: true
            align: right
```

### 3. Написать DSL-скрипт

```
// Документ: РеализацияТоваров
Функция Сформировать()
    ТабДок = Новый ТабличныйДокумент

    Заг = ТабДок.ПолучитьОбласть("Заголовок")
    Заг.Параметры.Номер = Номер
    ТабДок.Вывести(Заг)

    Для Каждого Стр Из Товары Цикл
        Обл = ТабДок.ПолучитьОбласть("Строки")
        Обл.Параметры.Товар      = Стр.Номенклатура.Наименование
        Обл.Параметры.Количество = Стр.Количество
        Обл.Параметры.Сумма      = Стр.Сумма
        ТабДок.Вывести(Обл)
    КонецЦикла

    ИтогОбл = ТабДок.ПолучитьОбласть("Итог")
    ИтогОбл.Параметры.Итого = Товары.Итог("Сумма")
    ТабДок.Вывести(ИтогОбл)

    Возврат ТабДок
КонецФункции
```

### 4. Редактировать макет в конфигураторе

В **Конфигураторе → дерево метаданных → Печатные формы DSL → ваша форма → Макет**
открывается встроенный визуальный редактор:

- **Слева** — YAML-текст макета (редактируется напрямую)
- **Справа** — визуальный конструктор с кликабельными ячейками

Изменения в YAML сразу отражаются в конструкторе. Кликнув ячейку,
внизу появится панель свойств (шрифт, цвет, выравнивание, colspan и т.д.).

Кнопка **«Сохранить»** записывает изменения в `Накладная.layout.yaml`.

> Подробнее — раздел «DSL-печатные формы с макетами» в **DEVELOPER.md**.

---

## Деплой на сервер

Пользователи подключаются через **браузер** — никакого отдельного клиентского приложения нет.

```
[Браузер пользователя] ──HTTP──> [onebase :8080] ──> [PostgreSQL]
```

### Шаг 1. Подготовить сервер

```bash
# Установить PostgreSQL (Ubuntu/Debian)
apt install postgresql postgresql-client

# Создать пользователя и базу данных
sudo -u postgres psql -c "CREATE USER onebase WITH PASSWORD 'securepass';"
sudo -u postgres psql -c "CREATE DATABASE mydb OWNER onebase;"

# Скопировать бинарник на сервер
scp onebase user@server:/usr/local/bin/
chmod +x /usr/local/bin/onebase
```

### Шаг 2. Задеплоить конфигурацию

Конфигурация (YAML-файлы) разрабатывается локально, а на сервер загружается в PostgreSQL одной командой:

```bash
# Запустить на локальной машине (или в CI/CD):
onebase deploy \
  --project ./myapp \
  --db "postgres://onebase:securepass@server-ip/mydb?sslmode=disable"
```

Команда выполняет сразу всё:
- Создаёт базу данных если не существует
- Инициализирует схему платформы (`_users`, `_sessions`, `_onebase_config`, ...)
- Загружает YAML-конфигурацию в PostgreSQL
- Применяет DDL-миграции (`CREATE TABLE IF NOT EXISTS` для всех сущностей)

После этого папка с YAML-файлами на сервере **не нужна** — конфиг хранится в PostgreSQL.

### Шаг 3. Запустить сервер

```bash
# На сервере — одноразовый запуск для проверки
onebase run \
  --config-source database \
  --db "postgres://onebase:securepass@localhost/mydb?sslmode=disable" \
  --port 8080
```

Открыть в браузере: `http://server-ip:8080/ui`

### Шаг 4. Настроить автозапуск (systemd)

```bash
# Зарегистрировать базу в реестре (один раз)
onebase ibases add \
  --name "Мой склад" \
  --db "postgres://onebase:securepass@localhost/mydb?sslmode=disable" \
  --source database \
  --port 8080

onebase ibases list   # скопировать ID

# Установить как systemd-сервис (нужен sudo)
sudo onebase service install --id <uuid> --user onebase
```

Или без реестра:

```bash
sudo onebase service install \
  --db "postgres://onebase:securepass@localhost/mydb?sslmode=disable" \
  --port 8080 \
  --name onebase-myapp \
  --user onebase
```

Команды управления:
```bash
systemctl status onebase-myapp   # статус
journalctl -u onebase-myapp -f   # логи в реальном времени
systemctl stop onebase-myapp     # остановить
systemctl start onebase-myapp    # запустить
```

Удалить сервис:
```bash
sudo onebase service uninstall --name onebase-myapp
```

### Nginx (HTTPS, обратный прокси)

```nginx
server {
    listen 80;
    server_name myapp.example.com;
    return 301 https://$host$request_uri;
}

server {
    listen 443 ssl;
    server_name myapp.example.com;

    ssl_certificate     /etc/letsencrypt/live/myapp.example.com/fullchain.pem;
    ssl_certificate_key /etc/letsencrypt/live/myapp.example.com/privkey.pem;

    location / {
        proxy_pass         http://127.0.0.1:8080;
        proxy_set_header   Host $host;
        proxy_set_header   X-Real-IP $remote_addr;
        proxy_set_header   X-Forwarded-For $proxy_add_x_forwarded_for;
        proxy_set_header   X-Forwarded-Proto $scheme;
        proxy_read_timeout 120s;
        client_max_body_size 100m;   # для загрузки вложений
    }
}
```

### Обновление конфигурации на сервере

После изменения YAML-файлов локально:

```bash
# Задеплоить обновлённую конфигурацию (перезаписывает _onebase_config)
onebase deploy \
  --project ./myapp \
  --db "postgres://onebase:securepass@server-ip/mydb?sslmode=disable"

# Перезапустить сервис (применит новые метаданные)
ssh user@server "systemctl restart onebase-myapp"
```

### Windows Server

```powershell
# Установить как Windows-сервис (от имени администратора)
onebase service install `
  --db "postgres://onebase:securepass@localhost/mydb?sslmode=disable" `
  --port 8080 `
  --name onebase-myapp

# Управление
sc.exe start onebase-myapp
sc.exe stop  onebase-myapp
```

Просмотр логов: **Просмотр событий → Журналы Windows → Приложение** (источник: `onebase-myapp`).

Или добавить вывод в файл — передать флаг при генерации unit-файла:
```bash
onebase service install --print --db ... > onebase-myapp.bat
# Отредактировать .bat для перенаправления stdout в файл
```

---

## Бэкап и восстановление

```bash
# Создать бэкап (требуется pg_dump)
onebase backup --db "postgres://localhost/sklad" --out ./backups/

# Восстановить (требуется psql)
onebase restore --db "postgres://localhost/sklad" \
                --file ./backups/backup_sklad_2026-05-07_10-30.sql.gz
```

---

## Команды CLI

```bash
# Разработка
onebase start                                    # лаунчер (GUI)
onebase dev   --project . --db <dsn> --open      # разработка: hot reload + браузер
onebase dev   --project . --sqlite dev.db --reload-binary  # + пересборка платформы

# Сервер
onebase deploy --project . --db <dsn>            # задеплоить конфиг в PostgreSQL
onebase run   --config-source database --db <dsn> --port 8080   # запуск production
onebase run   --id <uuid>                        # запуск базы из реестра по ID

# Автозапуск
onebase service install --id <uuid>              # установить как systemd / Windows service
onebase service install --db <dsn> --port 8080   # без реестра
onebase service install --print --db <dsn>       # показать unit-файл без установки
onebase service uninstall --name <svcName>       # удалить сервис

# Проект
onebase init  ./my-project                       # создать пустой проект
onebase init  --template warehouse ./my-wh       # из готового шаблона
onebase init  --list-templates                   # список шаблонов
onebase migrate --project . --db <dsn>           # применить миграцию вручную

# Бэкап
onebase backup  --db <dsn> --out ./backups/      # создать бэкап
onebase restore --db <dsn> --file <path>         # восстановить из бэкапа

# Реестр баз
onebase ibases list                              # список зарегистрированных баз
onebase ibases add --name X --db <dsn>           # добавить базу в реестр
onebase ibases remove --id <uuid>                # удалить из реестра

# Прочее
onebase convert --dir ./export --out .           # импорт конфигурации из XML-выгрузки
```

---

## Конфигуратор

Кнопка **«Конфигуратор»** в лаунчере открывает инструмент разработчика:

- **Дерево метаданных** — все объекты конфигурации с полями и исходными кодами
- **Импорт конфигурации** — загрузка из файлов XML-выгрузки 1С:Предприятие 8.3

---

## Полная документация

`DEVELOPER.md` в репозитории — полный справочник: все типы объектов, DSL, язык запросов, виртуальные таблицы, регистр бухгалтерии, подсистемы, регламентные задания.
