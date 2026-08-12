# Аудит проекта OneBase и план улучшений

**Дата:** 2026-08-11

**Статус:** черновик для независимого технического ревью

**Базовый коммит:** `4c2cf3701fb1e574ca2dfd1b81d59e6b0b2b46e9`

**Ветка:** `newfich`
**Область аудита:** архитектура, безопасность, целостность данных, тестирование,
CI/CD, релизы, эксплуатация, документация и продуктовое направление.

Документ описывает результаты статического аудита репозитория. Это не план,
утверждённый к реализации: каждый вывод должен быть независимо подтверждён по
коду, тестам и требованиям продукта.

## 1. Краткий вывод

OneBase уже выглядит как зрелый продукт: у проекта широкая функциональность,
сильная тестовая база, два поддерживаемых движка БД, развитые механизмы
безопасности, наблюдаемости и эксплуатации.

Главный риск следующего этапа — не отсутствие функций, а рост поверхности
системы быстрее, чем консолидация её инвариантов. Одинаковые изменения данных
частично реализуются независимо в UI, REST API и DSL. Это повышает вероятность,
что один из путей забудет выполнить проверку ссылок, сохранить движения,
отправить событие, записать аудит или применить ограничение доступа.

Рекомендуемое решение на ближайший цикл:

1. Выделить отдельный стабилизационный milestone без расширения продуктовой
   поверхности; срок оценить после независимой верификации находок и разбиения
   на change sets.
2. Централизовать команды изменения данных.
3. Закрыть перечисленные ниже security/release/backup риски.
4. Добавить сквозные контрактные и браузерные тесты.
5. После этого возвращаться к marketplace, кластеризации и другим крупным
   направлениям.

Предлагаемое позиционирование v1:

> Одноузловая self-hosted платформа для небольших и средних команд: SQLite для
> быстрого старта, PostgreSQL для многопользовательской эксплуатации.

## 2. Методика и ограничения

Аудит включал:

- инвентаризацию пакетов, файлов и зависимостей;
- просмотр composition root и основных HTTP/UI/DSL путей;
- сравнение реализаций сохранения, проведения и удаления;
- статический security review аутентификации, авторизации, вложений, HTTP-сервисов,
  обновлений, архивов и reverse-proxy поведения;
- анализ GitHub Actions, установщика, backup/restore и документации;
- проверку структуры тестов и истории изменений.

Выполненные проверки:

- синтаксис всех 14 first-party JavaScript-файлов — успешно;
- `managed_file_reader_behavior_test.js` — 10 из 10 тестов успешно;
- `git diff --check` — успешно;
- рабочее дерево до создания этого документа было чистым.

Ограничение: Go toolchain отсутствовал в окружении аудита. Поэтому локально не
запускались `go test ./...`, race detector, линтеры, fuzzing, PostgreSQL
integration tests и фактическое измерение покрытия. Наличие соответствующих
шагов в CI не означает, что они зелёные на базовом коммите.

## 3. Снимок проекта

Приблизительные показатели на базовом коммите:

| Показатель | Значение |
|---|---:|
| Production-файлы Go в `cmd` и `internal` | около 620 |
| Test-файлы Go в `cmd` и `internal` | около 730 |
| Функции `Test*` | около 3 229 |
| Benchmark-функции | 8 |
| Fuzz-тесты | 0 |
| Использования `t.Parallel()` | 0 |
| Внутренние пакеты | около 78 |
| Production Go LOC | около 178 700 |

Крупнейшие точки концентрации логики:

- `internal/query/query.go` — более 4 300 строк;
- `internal/ui/templates.go` — более 3 000 строк;
- `internal/ui/handlers_entity.go` — более 2 100 строк;
- `internal/launcher/configurator_tmpl_tree.go` — более 2 000 строк;
- `internal/launcher/static/configurator.js` — более 4 300 строк;
- `internal/ui/static/ui.js` — около 2 900 строк;
- `internal/ui/static/managed.js` — около 2 100 строк.

Высокий размер файла сам по себе не является дефектом. Важен тот факт, что в
части этих файлов одновременно сосредоточены разные стадии обработки и разные
инварианты продукта.

## 4. Сильные стороны

Во время ревью обнаружены следующие положительные свойства:

- CI предусматривает форматирование, `go vet`, race detector, coverage,
  PostgreSQL integration tests, `golangci-lint`, `gosec`, `govulncheck`,
  benchmark regression gate и Windows cross-build.
- Сессии и API-токены генерируются случайно и хранятся в виде хешей.
- OIDC реализует state, nonce, PKCE, JWKS и проверки основных claims.
- Есть CSRF/Fetch Metadata защита, security headers, RLS и field masking.
- SQL-значения параметризуются, а идентификаторы метаданных валидируются.
- Для загрузок, архивов и restore предусмотрены ограничения размера и защиты от
  traversal/symlink.
- DSL network/exec/file возможности ограничиваются guards; присутствуют sandbox,
  таймауты и лимиты вывода.
- Webhook dispatcher имеет ограниченную очередь, workers, таймауты, retry caps и
  graceful shutdown.
- Проект поддерживает SQLite и PostgreSQL, содержит метрики, pprof,
  Prometheus/Grafana и нагрузочные сценарии.
- Набор security regression tests заметно выше среднего для проекта такого типа.

Эти механизмы следует сохранять. Предлагаемые изменения направлены прежде всего
на уменьшение расхождения между разными путями выполнения.

## 5. Шкала приоритетов и уверенности

| Приоритет | Значение |
|---|---|
| P0 | После подтверждения рекомендуется закрыть до следующего stable-релиза |
| P1 | Следующий инженерный цикл; существенно снижает риск сопровождения и эксплуатации |
| P2 | Полезное улучшение после стабилизации; допустимо планировать по фактическому спросу |

Приоритет поставки не равен severity. Например, отсутствие подписи релиза —
подтверждённый факт, но проект уже сознательно отложил эту работу, поэтому ниже
она имеет высокую уверенность и P2 delivery priority. И наоборот, возможное
нарушение права доступа требует срочного продуктового решения, но пока имеет
среднюю уверенность как дефект, поскольку текущий тест может закреплять
сознательный compatibility contract.

| Уверенность | Значение |
|---|---|
| Высокая | Факт непосредственно подтверждён кодом, workflow или тестом |
| Средняя | Код указывает на риск, но нужны runtime-проверка или продуктовое решение |
| Низкая | Гипотеза для исследования, без достаточного доказательства в текущем аудите |

## 6. Сводка находок

| ID | Delivery | Уверенность | Область | Краткий вывод |
|---|---|---|---|---|
| DATA-01 | P0 verification | Средняя | Целостность данных | Нужно проверить достижимый обход `CheckRefs` через API/DSL delete |
| CORE-01 | P1 | Высокая | Архитектура ядра | Команды изменения данных дублируются в UI/API/DSL |
| SEC-01 | P0 | Высокая | Доступность | JSON-login не ограничивает body и допускает high-cardinality limiter keys |
| SEC-02 | P0 decision | Средняя | Авторизация | UI трактует `write` как достаточное для чтения вложения; нормативная модель не подтверждена |
| REL-01 | P0 | Высокая | Installer | Установщик не проверяет checksum и обновляет без staging |
| REL-02 | P0 | Высокая | Release gate | Публикация не зависит от зелёного CI для exact SHA |
| REL-03 | P2 | Высокая | Supply chain | У release/self-update нет независимой криптографической подписи |
| DR-01 | P0 | Высокая | Восстановление | Нет PostgreSQL backup/restore round-trip gate |
| SEC-03 | P0/P1 | Средняя | Bootstrap | Внешний fresh deployment стартует после предупреждения |
| SEC-04 | P1 | Высокая | TOTP | Без master key TOTP seed сохраняется открыто |
| SEC-05 | P1 | Средняя | Reverse proxy | Forwarded headers используются без trusted-proxy модели |
| INT-01 | P1 | Средняя | HMAC | Generic HTTP service signature не имеет freshness/replay protection |
| INT-02 | P1 decision | Высокая | HTTP config | Пустой `auth` нормализуется к `none` |
| ARCH-01 | P1 | Высокая | Архитектура | `runServer` слишком велик, API конструктивно зависит от UI |
| ARCH-02 | P1 | Высокая | DSL | UI/API report evaluators имеют разные resource limits |
| ARCH-03 | P1 | Высокая | Query Builder | Две копии схемы уже расходятся по поддерживаемым sources |
| QUAL-01 | P1 | Высокая | Config tests | Реализованные тесты examples не являются общим CI gate |
| QUAL-02 | P1 | Высокая | Browser E2E | Frontend gate проверяет синтаксис, но не критические пользовательские пути |
| QUAL-03 | P2 | Высокая | Fuzzing | Parser-heavy проект не содержит fuzz targets |
| QUAL-04 | P1 | Высокая | CI hygiene | Coverage/dependency процессы не имеют достаточного regression/schedule gate |
| CLI-01 | P1 | Высокая | CLI | Реализованная команда `device-agent` не зарегистрирована |
| DOC-01 | P1 | Высокая | Документация | Статусы планов и документированные команды расходятся с кодом |
| PRODUCT-01 | P2 | Средняя | Стратегия | Marketplace и enterprise-расширения лучше начинать после стабилизации ядра |

## 7. Подробные находки

### DATA-01 — возможное расхождение удаления и `CheckRefs`

**Статус.** Гипотеза с P0-приоритетом проверки, а не подтверждённый P0-дефект.

**Наблюдение.** Удаление реализовано в нескольких адаптерах, а явно видимая
fail-closed проверка ссылок присутствует в UI-пути.

**Доказательства:**

- удаления реализованы отдельно в `internal/api/handlers.go:449`,
  `internal/api/v2.go:267`, `internal/ui/handlers_entity.go:1522` и
  `internal/ui/dsl_documents.go:388`;
- UI вызывает `CheckRefs` в `internal/ui/handlers_entity.go:1570`;
- `storage.DB.Delete` в `internal/storage/crud.go:764` сам эту проверку не
  выполняет, хотя `internal/storage/deletion.go:82` описывает `CheckRefs` как
  защитную precondition.

**Что пока не доказано.** API/DSL могут иметь эквивалентный upstream guard,
ограничение схемы либо тест, который не был замечен статическим аудитом.
Runtime-проверка на базовом коммите не выполнялась.

**Проверка:**

1. Создать запись A, на которую ссылается запись B.
2. Под одной и той же ролью попытаться удалить A через UI, REST v1, REST v2 и
   DSL.
3. Сопоставить HTTP/DSL результат, состояние A/B, аудит и движения.
4. Проследить полный call graph каждого пути до транзакции и storage.

**Действие при подтверждении.** Сделать защиту обязательной внутри общего
delete use-case или другой единой границы, которую невозможно обойти адаптером.

**Критерии готовности:** все точки входа одинаково отклоняют удаление при
существующей защищаемой ссылке; regression test сначала воспроизводит расхождение
на старом коде, если оно действительно существует.

### CORE-01 — консолидация pipeline изменения данных

**Наблюдение.** Сохранение, проведение и удаление частично реализованы отдельно
для разных транспортов.

**Доказательства:**

- центральный `entityservice.Save` — `internal/entityservice/service.go:209`;
- DSL-путь сохранения документа — `internal/ui/dsl_documents.go:733`;
- ручное проведение из UI — `internal/ui/handlers_entity.go:1291`;
- комментарии в `internal/ui/dsl_documents.go:804` указывают, что DSL обходит
  `entityservice.Save` и вручную регистрирует exchange/change/webhook эффекты;
- отдельные delete-пути перечислены в DATA-01.

**Риск.** Чем больше независимых реализаций, тем проще пропустить движение,
аудит, webhook, live event, обмен или проверку доступа. Недавняя история
исправлений silent movement loss согласуется с этим классом риска, но не является
доказательством нового активного дефекта.

**Рекомендация.** Сначала создать contract matrix и явно описать обязательные
инварианты. Затем мигрировать одну операцию — предпочтительно `Delete` — на
общий use-case. `CommandService` с `Save`, `Post`, `Unpost`, `Delete`, `Mark` и
`Fill` является одним из вариантов реализации, а не единственно допустимой
архитектурой.

**Критерии готовности:**

- для каждой операции существует одна спецификация обязательных эффектов;
- contract tests выполняют операцию через UI, REST v1, REST v2 и DSL и
  сравнивают итог;
- fault-injection tests отдельно проверяют отказ обязательных транзакционных и
  post-commit эффектов;
- адаптер не может пропустить RLS, ссылки, движения или аудит;
- documented compatibility behavior для старых API сохранено либо явно
  версионировано.

### SEC-01 — ограничение публичного JSON-login

**Наблюдение.** Form login ограничивает размер request body, JSON login — нет.
Произвольный login также участвует в ключе rate limiter.

**Доказательства:**

- публичный маршрут — `internal/api/server.go:102`;
- form body ограничен в `internal/auth/handlers.go:195`;
- JSON decoder читает `r.Body` без `MaxBytesReader` в
  `internal/auth/handlers.go:269`;
- login входит в limiter key в `internal/auth/ratelimit.go:90`;
- после достижения 10 000 элементов cleanup сканирует map в
  `internal/auth/ratelimit.go:75`.

**Риск.** Неаутентифицированный клиент может создавать лишнюю нагрузку на
память/CPU большими телами и большим числом уникальных логинов.

**Рекомендация.** Ввести общий bounded JSON decoder, лимит тела 16–64 КиБ,
ограничения длины login/password, отдельные лимиты per-IP и per-account и
жёстко ограниченный TTL/LRU cache. Тем же helper следует закрыть остальные
JSON create/update endpoints.

**Критерии готовности:**

- oversized body получает контролируемый `413` или `400`;
- слишком длинные credentials отклоняются до password hashing;
- limiter не растёт без ограничений и не выполняет линейный scan на каждом
  запросе в установившемся режиме;
- есть regression tests для body size, high-cardinality keys и нормального login.

### SEC-02 — чтение вложений требует права `read`

**Статус.** Требуется продуктовое решение. Текущий тест явно закрепляет
`read OR write`, поэтому статический аудит не может считать это случайной
уязвимостью. REST v2 демонстрирует другую модель, но не доказывает, что она
нормативная для UI.

**Наблюдение.** UI разрешает скачивание вложения при `read OR write`, тогда как
REST v2 требует именно `read`.

**Доказательства:**

- UI: `internal/ui/handlers_attachments.go:121`;
- тест, закрепляющий write-only доступ: `internal/ui/attachments_rbac_test.go:59`;
- REST v2: `internal/api/v2_attachments.go:180`;
- REST v2 regression test: `internal/api/v2_attachments_test.go:263`;
- сходное legacy-поведение изображений находится в
  `internal/ui/handlers_image.go:142`.

**Условный риск.** Если write-only роль является поддерживаемым сценарием и
`write` намеренно не означает `read`, пользователь может получить содержимое
вложения при знании UUID. Если продукт определяет `write` как включающее чтение,
расхождение следует документировать, а не исправлять как vulnerability.

**Рекомендация при независимых правах.** Для постоянного скачивания требовать
`read`. Предпросмотр только что загруженного файла реализовать короткоживущей
capability, привязанной к uploader/session. Ownerless legacy blobs мигрировать к
владельцам или закрыть.

**Рекомендация при иерархии прав.** Явно зафиксировать `write ⇒ read` как
продуктовый инвариант и привести REST v2, UI, документацию и tests к одной модели.

**Критерии готовности:**

- владелец продукта письменно выбрал независимую или иерархическую модель прав;
- UI и REST имеют одинаково объяснённую модель доступа;
- при независимых правах write-only пользователь не может скачать существующее
  вложение;
- разрешённый upload-preview не создаёт постоянное обходное право;
- миграция legacy blobs и обратная совместимость описаны явно.

### REL-01 — безопасное обновление через `install.ps1`

**Наблюдение.** PowerShell installer выбирает asset wildcard-выражением, не
проверяет публикуемый checksum и распаковывает архив непосредственно в рабочую
директорию.

**Доказательства:**

- wildcard выбора asset — `install.ps1:19`;
- download/extract без checksum verification — `install.ps1:26`;
- рекурсивный поиск exe при обновлении — `install.ps1:37`.

**Риск.** Возможны выбор `.sha256` вместо ZIP, частичное обновление, no-op при
выборе старого exe и замена рабочего бинарника до проверки нового.

**Рекомендация:**

1. Использовать точные имена archive/checksum.
2. Проверять SHA-256 до распаковки.
3. Распаковывать во staging directory.
4. Проверять `onebase --version`.
5. Атомарно заменять бинарник и гарантированно очищать staging.

**Критерии готовности:**

- изменение байта в архиве или checksum приводит к отказу установки;
- неоднозначный или отсутствующий asset приводит к контролируемому отказу;
- неуспешное обновление сохраняет прежний рабочий бинарник;
- повторное обновление не может выбрать уже установленный старый exe;
- installer имеет automated happy-path и failure-path tests.

### REL-02 — публикация только проверенного SHA

**Наблюдение.** Release workflow запускается независимо от результата основного
CI, а write permission задан шире job, которой он нужен.

**Доказательства:**

- release triggers — `.github/workflows/release.yml:3`;
- глобальный `contents: write` — `.github/workflows/release.yml:10`.

**Рекомендация.** Использовать reusable verification workflow либо
`workflow_run`/эквивалентную проверку для exact SHA. Для stable tag проверять
semver и допустимую ancestry policy. Write permission выдавать только job
публикации; внешние Actions с write token закрепить по immutable SHA.

**Критерии готовности:**

- workflow может быть запущен вручную, но публикация невозможна без зелёного
  verification result для того же SHA;
- красный или отсутствующий CI result не создаёт GitHub release;
- содержимое архива распаковывается и smoke-тестируется до публикации;
- архив включает обязательные `LICENSE`/third-party notices;
- build jobs не получают `contents: write`.

### REL-03 — криптографическая provenance release artifacts

**Статус.** Подтверждённый gap, но P2 delivery priority: подпись уже сознательно
отложена в `Plans/92-platform-selfupdate.md`. Это не должно незаметно стать
обязательным условием ближайшего релиза без отдельного решения владельца.

**Наблюдение.** Checksum self-update загружается из того же trust domain, что и
архив: `internal/selfupdate/download.go:34`. Компрометация источника позволяет
заменить и архив, и checksum.

**Рекомендация.** Спроектировать подписанный manifest: Ed25519, Sigstore или
TUF-compatible схему со встроенным доверенным ключом, ротацией и offline
verification.

**Критерии готовности:** online и offline update проверяют независимую подпись
manifest; documented key rotation и recovery process протестированы. Требования
к детерминированным/reproducible builds в эту находку не входят и должны
рассматриваться отдельно.

### DR-01 — проверяемое восстановление PostgreSQL

**Наблюдение.** Для SQLite есть dump/restore test, а PostgreSQL round-trip в CI
не найден.

**Доказательства:**

- SQLite test — `internal/backup/sqlite_test.go:13`;
- native PostgreSQL backup зависит от `pg_dump`/`psql` —
  `internal/backup/backup.go:20`;
- PostgreSQL CI job тестирует несколько подсистем, но не полный backup/restore.

**Риск.** Backup может создаваться успешно, но не восстанавливать актуальную
схему, системные таблицы, пользователей, вложения или прикладные данные.

**Рекомендация.** В CI или scheduled workflow выполнять:

```text
исходная PostgreSQL БД
  → заполнение контрольными данными
  → pg_dump
  → восстановление в новую пустую БД
  → проверка схемы, пользователей, конфигурации, данных и вложений
```

**Критерии готовности:**

- тест проверяет не только exit code, но и содержимое восстановленной системы;
- после утверждения support matrix покрыта минимально поддерживаемая PostgreSQL
  версия, а текущая версия проверяется scheduled job;
- documented recovery procedure повторяет проверенный автоматикой путь;
- failure logs сохраняются как CI artifact и позволяют диагностировать этап
  dump/restore/verification.

RPO/RTO остаются отдельным эксплуатационным решением владельца продукта и не
являются критерием завершения самого round-trip теста.

### SEC-03 — внешний bootstrap fresh deployment

**Наблюдение.** При внешнем bind и отсутствии пользователей приложение выводит
предупреждение, но продолжает запуск: `internal/cli/run.go:477`. Loopback bind по
умолчанию является существующим смягчением риска.

**Рекомендация.** Для non-loopback fresh deployment требовать заранее заданного
администратора, одноразовый bootstrap token либо явный
`--allow-insecure-bootstrap` с заметным предупреждением и аудитом.

**Критерии готовности:** automated startup tests отдельно покрывают loopback,
external bind с безопасным bootstrap, external bind без bootstrap и явный
insecure override; поведение описано в deployment guide.

### SEC-04 — plaintext fallback для TOTP seed

**Наблюдение.** При отсутствии master key TOTP seed сохраняется открыто:
`internal/auth/twofactor.go:129`; `_users` и backup codes входят в universal
backup: `internal/backup/universal.go:64`.

**Существующее смягчение.** Fallback документирован и сопровождается
предупреждением. Gap состоит в отсутствии enforced production profile,
постоянного runtime/admin health signal и гарантированной миграции старых seeds.

**Рекомендация.** Для production/2FA требовать master key или автоматически
создавать локальный key file с минимальными правами. Добавить миграцию plaintext
seeds и постоянный health/admin alert до её завершения.

**Критерии готовности:** tests покрывают создание 2FA без ключа в development и
production profiles, чтение legacy plaintext, успешную миграцию, backup/restore
зашифрованного seed и поведение при неверном ключе.

### SEC-05 — forwarded headers и trusted proxies

**Наблюдение.** Если public URL не задан, OIDC может строить callback из
`X-Forwarded-Proto/Host` без модели trusted proxies:
`internal/auth/handlers_oidc.go:139`.

**Существующие смягчения.** `ONEBASE_PUBLIC_URL` и
`ONEBASE_SECURE_COOKIES` документированы и позволяют безопасно настроить
deployment вручную.

**Рекомендация.** Для OIDC за proxy требовать валидный HTTPS public URL либо
явный список trusted proxies. Forwarded headers принимать только от них, а
`SecureCookies` выводить из public URL, если нет более строгой настройки.

**Критерии готовности:** deployment tests проверяют прямой HTTP, прямой HTTPS,
trusted proxy, untrusted forwarded headers и secure-cookie derivation.

### INT-01 — freshness и replay generic HMAC requests

**Наблюдение.** Generic HTTP service HMAC подписывает только body:
`internal/ui/services.go:319`. Подпись не связывает запрос с методом, путём и
временем, а generic `/hs/*` не имеет общего idempotency contract.

Intake использует сходную подпись в `internal/ui/intake_http.go:167`, но уже
имеет обязательную idempotency key и duplicate/replay tests. Это существенно
снижает риск повторного бизнес-эффекта для intake; его нельзя приравнивать к
generic service без дополнительной проверки.

**Рекомендация.** Для generic services ввести versioned canonical signature над
timestamp, method, path и body hash, ограниченное окно времени и nonce/event ID
cache. Для intake отдельно решить, нужна ли freshness поверх существующей
идемпотентности.

**Критерии готовности:**

- generic request с изменёнными method/path/timestamp отклоняется;
- повтор event ID и запрос за пределами окна отклоняются;
- старый формат имеет измеренный, ограниченный compatibility window;
- intake regression tests подтверждают, что миграция подписи не ломает
  существующую идемпотентность.

### INT-02 — blank `auth` как неявный публичный контракт

**Статус.** Подтверждённое поведение, но необходимость изменения требует
продуктового решения: публичный endpoint может быть сознательным контрактом.

**Доказательства:**

- пустой `auth` нормализуется к `none`:
  `internal/httpservice/service.go:88`;
- validator принимает blank/none: `internal/configcheck/services.go:47`;
- CRM example содержит анонимный изменяющий POST endpoint:
  `examples/crm/services/Уведомления.yaml:4`.

**Рекомендация.** Отсутствующий `auth` считать ошибкой, сохранив явный
`auth: none` для сознательно публичных сервисов. Linter должен предупреждать или
падать для mutating anonymous endpoints без rate limit. CRM example либо
защитить token/HMAC, либо явно обозначить как небезопасный локальный demo.

**Критерии готовности:** blank и explicit none различаются в validation tests;
совместимость существующих конфигураций имеет migration warning/window; shipped
examples не создают неявно публичный изменяющий endpoint.

### ARCH-01 — oversized bootstrap и зависимость API → UI

**Наблюдение.** Наличие composition root рядом с entrypoint нормально. Проблема
здесь уже: `runServer` в `internal/cli/run.go:62` одновременно открывает БД,
загружает проект, мигрирует схему, строит interpreter/scheduler, запускает backup,
email, webhooks, API и hot reload. `api.Server` содержит `*ui.Server` в
`internal/api/server.go:27`, а API создаёт UI и извлекает из него `EntitySvc`.

`storage.DB` также является широким инфраструктурным объектом: у него сотни
методов, и он используется напрямую большим числом пакетов.

**Рекомендация.** Выделить тестируемую сборку приложения, например в
`internal/app` или `internal/bootstrap`:

```go
app, err := bootstrap.Build(options, dependencies)
if err != nil {
    return err
}
defer app.Close()
return app.Run(ctx)
```

Общие use-case сервисы следует построить один раз и внедрить в UI и API как в
соседние адаптеры. Cobra-команда может оставаться composition entrypoint, но
длинный lifecycle-сценарий должен быть доступен как тестируемый объект. Далее
интерфейсы выделяются со стороны потребителей: `EntityStore`, `MovementStore`,
`SettingsStore`, `TxManager`.

**Критерии готовности:**

- запуск приложения можно тестировать без сигналов процесса и `os.Exit`;
- API не создаёт UI и не извлекает из него бизнес-сервисы;
- constructors не мутируют скрыто переданные зависимости;
- lifecycle фоновых компонентов имеет явные start/stop/close границы;
- новый use-case не обязан зависеть от полного `*storage.DB`.

### ARCH-02 — единый sandboxed report evaluator

**Наблюдение.** Почти одинаковые evaluator implementations находятся в
`internal/api/report_eval.go:18` и `internal/ui/report_eval.go:23`. API применяет
`CallSandboxed` с ограничениями, UI использует обычный `RunWithResult`.

**Рекомендация.** Один evaluator в `internal/report` или `internal/dslexpr`,
который невозможно вызвать без явного `SandboxProfile`.

**Критерии готовности:** tests запускают одинаковое нормальное и runaway
выражение через UI/API evaluator; оба имеют одинаковые wall-clock/iteration/output
ограничения и одинаково классифицируют timeout/limit errors.

### ARCH-03 — единая схема Query Builder

**Наблюдение.** Query Builder schema продублирована в
`internal/ui/query_builder.go:11` и
`internal/launcher/configurator_load.go:706`. Уже есть различия по account
register sources и slice sources для information registers.

**Рекомендация.** Вынести transport-neutral модель в `internal/queryschema`.
UI и configurator должны сериализовать один и тот же результат.

**Критерии готовности:** golden test подтверждает паритет Query Builder JSON;
новые типы источников добавляются один раз; публичная схема не меняется без
versioning/migration note.

После закрепления behavior golden tests можно отдельно разделить
`internal/query/query.go` на этапы `lex → resolve/access → plan → dialect render`.

### QUAL-01 — конфигурационные тесты examples как CI gate

**Наблюдение.** Инструмент `onebase test` и тестовые сущности в shipped examples
реализованы, но общий CI выполняет для конфигураций в основном lint.

**Рекомендация.** Автоматически находить ожидаемые test suites в `minimal`,
`callcenter` и `trade`, запускать их на SQLite, а выбранные критические suites —
на PostgreSQL; сохранять JUnit.

**Критерии готовности:** CI проверяет заранее зафиксированное ненулевое число
test cases для каждого обязательного example; ноль найденных тестов является
ошибкой, а не успешным результатом; role/RLS failures блокируют merge.

### QUAL-02 — browser smoke suite

**Наблюдение.** Frontend gate проверяет синтаксис JavaScript, но не критические
пользовательские пути.

**Рекомендация.** Добавить 5–10 Playwright scenarios: login/bootstrap, создание
и изменение элемента каталога, проведение документа и движения, RLS denial,
configurator save/reload и upload/download attachment.

**Критерии готовности:** suite запускается на чистой SQLite БД в CI, сохраняет
trace/screenshot при ошибке, использует устойчивые selectors и блокирует merge
для перечисленных critical paths.

### QUAL-03 — fuzzing parser-heavy границ

**Наблюдение.** В проекте не найдено fuzz targets, хотя DSL, query, YAML, archive
и import parsers обрабатывают сложный или недоверенный ввод.

**Рекомендация.** Начать с seed corpora для DSL lexer/parser, query compiler и
archive paths, затем добавить YAML/import. Короткий smoke-fuzz запускать в CI,
длительный — scheduled.

**Критерии готовности:** каждый target имеет seeds из regression cases;
crashers сохраняются в corpus; scheduled job публикует команду, seed и stack;
найденный crash превращается в deterministic regression test.

### QUAL-04 — coverage, dependencies и workflow hygiene

**Наблюдение.** Coverage сохраняется как artifact без явного non-regression
gate; dependency/vulnerability проверки не имеют полного scheduled процесса;
тяжёлые workflows не везде ограничены concurrency/timeout.

**Рекомендация.** Ввести diff/non-regression coverage для критических пакетов,
scheduled `govulncheck`, dependency updates, `go mod tidy -diff`, `concurrency`,
`cancel-in-progress` и `timeout-minutes`. Минимальный ESLint/JSDoc слой добавить
без смены frontend stack.

**Критерии готовности:** baseline и допустимое снижение задокументированы по
критическим пакетам; scheduled jobs действительно создают видимый сигнал при
регрессии; отменённые/зависшие CI runs не потребляют ресурсы бесконечно.

### CLI-01 — команда `device-agent`

**Наблюдение.** Команда объявлена в `internal/cli/deviceagent.go:13` и описана в
документации, но отсутствует среди `rootCmd.AddCommand` в
`internal/cli/root.go:34`.

**Рекомендация.** Зарегистрировать команду и добавить CLI contract test через
`rootCmd.Find`. Перед включением также задать HTTP server timeouts и требовать
token при bind не на loopback.

**Критерии готовности:** documented command обнаруживается root command,
запускается в smoke test, безопасно обрабатывает внешний bind и завершение.

### DOC-01 — единый источник состояния и исполняемая документация

**Наблюдение.** В `Plans` есть дублирующиеся номера и противоречащие друг другу
статусы. `Plans/README.md:5` прямо предупреждает, что статусы требуется сверять.
Также найдены примеры документационного дрейфа:

- `QUICKSTART.md:87` указывает старое требование к Go;
- `docs/release-template.md:92` использует отсутствующую команду
  `onebase serve`;
- README сообщает другое количество examples;
- `README.md:5` и `QUICKSTART.md:6` описывают информационную базу как
  PostgreSQL-only, тогда как `release_v0.9.9.md:107` говорит, что PostgreSQL
  нужен только для многопользовательского режима, а SQLite не требует внешних
  зависимостей.

**Рекомендация:**

- один машиночитаемый источник feature/status/compatibility;
- уникальные IDs планов и архив завершённых документов;
- Cobra-generated command reference;
- CI smoke test документированных команд и локальных Markdown links;
- короткий zero-dependency SQLite quickstart;
- compatibility/deprecation policy перед v1.

**Критерии готовности:** документация проверяется автоматикой там, где это
возможно; один статус не требуется вручную обновлять в нескольких файлах;
новый пользователь может запустить минимальный проект без PostgreSQL.

### PRODUCT-01 — порядок продуктового расширения

**Наблюдение.** В backlog одновременно присутствуют направления для SMB,
enterprise, headless, marketplace и дальнейшего масштабирования. Каждое из них
увеличивает compatibility и support matrix.

**Рекомендация.** До marketplace и horizontal clustering определить v1 bar:

- один pipeline изменения данных;
- строгий production preflight;
- подписанный и gated release;
- проверенный PostgreSQL restore;
- browser smoke tests;
- compatibility/deprecation policy.

После этого marketplace можно начинать с малого pilot: signed package manifest,
version constraints, capability declaration и rollback. Native PostgreSQL RLS,
LDAP и clustering следует планировать по подтверждённому спросу, поскольку
application-level RLS уже закрывает значительную часть текущего сценария.

## 8. Связь с существующими планами и решениями

Эта таблица нужна, чтобы ревьюер не предложил повторно уже реализованную или
сознательно отложенную работу. Статус самого плана не является доказательством:
его всё равно нужно сверять с кодом.

| Finding | Связанный документ | Что учесть при ревью |
|---|---|---|
| DATA-01, CORE-01 | `Plans/115-process-hardening.md` | Уже зафиксирован класс дефектов «инвариант применён в N−1 месте» |
| DR-01 | `Plans/30-universal-backup.md` | Сопоставить заявленную модель backup с фактическими round-trip tests |
| SEC-04, SEC-05 | `Plans/84-enterprise-auth.md` | Проверить сознательные auth/deployment trade-offs |
| REL-03 | `Plans/92-platform-selfupdate.md` | Подпись artifacts явно отложена отдельным шагом |
| QUAL-01 | `Plans/108-config-testing-tooling.md` | Runner/JUnit уже реализованы; finding только про включение в CI |
| QUAL-02, QUAL-04 | `Plans/55-monolith-split-embed-frontend.md`, `Plans/56-techdebt-ci-observability.md`, `Plans/109-ci-linter-hardening.md` | Не повторять выполненное, выделить только остающиеся gates |
| DOC-01 | `Plans/README.md` | Сам индекс признаёт необходимость сверки статусов |

## 9. Предлагаемый порядок реализации

### Фаза 0 — верификация и продуктовые решения

- DATA-01: воспроизвести или опровергнуть расхождение удаления;
- SEC-02: определить независимую либо иерархическую модель прав;
- INT-02: определить политику для blank/explicit anonymous auth;
- утвердить PostgreSQL support matrix для DR-01.

Эта фаза превращает гипотезы в задачи и предотвращает исправление сознательных
compatibility contracts как случайных дефектов.

### Фаза A — безопасность релиза и восстановление

- SEC-01: bounded JSON и rate limiter hardening;
- SEC-02: единые права на вложения, только если риск подтверждён решением фазы 0;
- REL-01: checksum и staging installer;
- REL-02: publication gate для exact SHA;
- DR-01: PostgreSQL restore round-trip;
- CLI-01: регистрация и hardening `device-agent`.

Ожидаемый результат: следующий релиз нельзя опубликовать или установить без
проверок, а основной disaster-recovery путь доказан тестом.

### Фаза B — инварианты ядра

- contract matrix UI/REST v1/REST v2/DSL;
- `Delete` через единую обязательную границу;
- затем `Post`/`Unpost`, `Save` и post-commit effects;
- ARCH-02: единый sandboxed evaluator.

Ожидаемый результат: транспорт перестаёт определять бизнес-поведение операции.

### Фаза C — quality gate и документация

- QUAL-01: конфигурационные тесты examples в CI;
- QUAL-02: Playwright smoke suite;
- QUAL-04: coverage/dependency/workflow gates;
- единый status source и SQLite quickstart.

### После стабилизационного milestone

- composition root и разделение UI/API;
- consumer-side storage interfaces;
- `internal/queryschema`;
- постепенное разделение query/UI/launcher hotspots;
- QUAL-03: расширенный scheduled fuzzing;
- REL-03: signed provenance после отдельного решения;
- marketplace pilot только после выполнения v1 bar.

Продолжительность фаз намеренно не указана: она зависит от результатов DATA-01,
compatibility решений и фактической стоимости PostgreSQL/browser infrastructure.

## 10. Что не предлагается делать сейчас

- Переписывать frontend на другой framework только из-за размера JavaScript
  файлов.
- Одновременно заменять весь `storage.DB` новой repository-архитектурой.
- Делить крупные файлы механически без закреплённых behavior tests.
- Вводить глобальный высокий coverage threshold ради числа.
- Начинать clustering или marketplace до стабилизации release и command pipeline.

Эти действия имеют высокий churn и сами по себе не гарантируют снижение риска.

## 11. Чек-лист независимого ревью

Ревьюер не должен принимать выводы документа на доверии. Для каждого finding ID
нужно:

- [ ] проверить `git rev-parse HEAD`; при несовпадении читать baseline через
  `git show`, не переключая и не очищая пользовательское рабочее дерево;
- [ ] проверить ссылки и актуальность строк на базовом коммите;
- [ ] найти существующие guards/tests, которые могли быть пропущены;
- [ ] подтвердить либо опровергнуть достижимость проблемного пути;
- [ ] отделить реальный дефект от design trade-off;
- [ ] независимо оценить confidence, impact severity и delivery priority;
- [ ] проверить, не нарушает ли предлагаемое исправление compatibility;
- [ ] сверить решение со связанными документами из раздела 8;
- [ ] оценить полноту критериев готовности;
- [ ] предложить минимальный безопасный change set;
- [ ] отметить зависимости между задачами;
- [ ] пересмотреть P0/P1/P2 с учётом реальных требований пользователей;
- [ ] отдельно проверить выводы, не подтверждённые запуском Go tests;
- [ ] привести выполненные команды и значимые отрицательные результаты поиска.

Рекомендуемый формат ответа ревьюера:

| ID | Вердикт | Confidence | Impact | Delivery | Доказательство | Пропущенные защиты | Действие |
|---|---|---|---|---|---|---|---|
| CORE-01 | confirm / partial / reject / unverified | high/medium/low | high/medium/low | P0/P1/P2 | файл, строка, тест | описание | минимальный шаг |

В конце ревью желательно дать:

1. список подтверждённых P0;
2. список false positives или завышенных severity;
3. порядок реализации с зависимостями;
4. тестовую матрицу для приёмки;
5. решение, можно ли начинать marketplace до закрытия выбранных пунктов.

## 12. Готовый запрос для другого агента

```text
Проведи независимое техническое ревью документа
Plans/audit-report-2026-08-11.md на базовом коммите
4c2cf3701fb1e574ca2dfd1b81d59e6b0b2b46e9.

Сначала выполни git rev-parse HEAD. Если HEAD отличается, читай baseline-файлы
через git show 4c2cf3701fb1e574ca2dfd1b81d59e6b0b2b46e9:<path>; не переключай ветку,
не очищай и не перезаписывай рабочее дерево.

Не доверяй выводам отчёта автоматически. Для каждого finding ID проверь код,
существующие тесты, защитные механизмы и достижимость проблемного пути. Отметь
вердикт confirm/partial/reject/unverified; отдельно оцени confidence, impact
severity и delivery priority; предложи минимальный безопасный change set.
Сверь finding с существующими Plans и сознательными compatibility trade-offs.
Особое внимание удели DATA-01, CORE-01, SEC-01, SEC-02, REL-01, REL-02 и DR-01.
Не вноси изменения в код: нужен только evidence-backed review.

Учитывай ограничение исходного аудита: Go toolchain в окружении отсутствовал,
поэтому выводы, зависящие от runtime behavior, требуют дополнительной проверки.
Если Go недоступен и путь нельзя доказать статически, ставь unverified, а не
confirm. В отчёте перечисли выполненные команды и значимые отрицательные поиски.
```

## 13. Решения, требующие владельца продукта

Технический аудит не может самостоятельно решить следующие вопросы:

1. Является ли write-only роль поддерживаемым бизнес-сценарием?
2. Допустим ли внешний bootstrap без предварительно созданного администратора?
3. Какой compatibility window требуется REST v1 и старому HMAC формату?
4. Какие PostgreSQL версии официально поддерживаются?
5. Является ли marketplace обязательной частью v1?
6. Какие RPO/RTO ожидаются от штатного backup/restore?

Ответы на эти вопросы могут изменить приоритет или отменить конкретную
рекомендацию. Независимо от выбранной архитектуры критические инварианты должны
быть явно описаны и проверяться автоматикой; единый service является одним из
способов обеспечить это, а не самоцелью.
