# План 127 — Публичные URL файлов (публикация вложений)

Дата проектирования: 2026-08-17.
Статус: ⬜ **Проект** (не начато).
Ветка: `feature/127-public-file-urls`.
Часть группы «веб-примитивы» (124–128). Самостоятельная ценность вне CMS:
отправить контрагенту ссылку на счёт/акт, не заводя ему учётную запись;
картинка в HTML-письме (внешний почтовый клиент не имеет сессии); QR/логотип в
печатной форме, открываемой по ссылке.

## Контекст

Вложения (план 22) отдаются только авторизованному пользователю с правом чтения
владельца — `attachmentDownload` (`internal/ui/handlers_attachments.go:108`)
проверяет `rowAllowsOwnerID(..., "read", ...)` и режет IDOR. Это правильно для
админки, но означает, что **никакой файл из базы нельзя показать анониму**: в
присланной CMS-конфигурации картинки товаров поэтому ссылаются на внешний CDN
`nortena.ru`, то есть контент живёт не в системе.

Технически отдача уже сделана как надо: `http.ServeContent`
(`handlers_attachments.go:135`, `internal/api/v2_attachments.go:199`) сам
обрабатывает `Range`, `If-Modified-Since` и `If-None-Match`. Не хватает только
**способа сказать «этот файл публичный»** — и сделать это так, чтобы не открыть
перебор чужих вложений по UUID.

## Что мешает в текущем коде (проверено по `3350679f`)

1. **Публичность нельзя выразить.** У `storage.Attachment`
   (`internal/storage/attachments.go:22`) полей доступа нет — только владелец
   (`OwnerKind`/`OwnerName`/`OwnerID`), имя, MIME, размер, кто загрузил.
2. **Флаг `public` у вложения был бы дырой.** Ссылка вида `/pub/<uuid вложения>`
   означает: угадал UUID — получил файл. UUID v4 не перебирается, но он **утекает**
   — в REST-ответах, в HTML админки, в логах; вложение, опубликованное однажды,
   осталось бы публичным навсегда и по тому же адресу.
3. **Служебные таблицы заводятся централизованно** — `EnsureServiceSchema`
   (`internal/storage/service_schema.go:24`). Новая таблица обязана быть
   добавлена именно туда, иначе матричные тесты на PostgreSQL падают непонятно
   почему (issue #827, зафиксировано в CLAUDE.md).
4. **Предохранитель сети** (план 62) применяется к `/hs/*` в
   `serviceDispatch` (`internal/ui/services.go:146`): при выключенной сети
   публичная поверхность конфигурации отвечает 503.
5. **Свободный префикс** — верхнеуровневые маршруты заняты `/ui`, `/api`, `/hs`,
   `/auth`, `/login`, `/health(z)`, `/status`, `/identity`, `/debug`,
   `/catalogs`, `/documents`, `/vendor`. `/pub` свободен.

## Синтаксис

```
// Опубликовать вложение (Ид вложения известен из Файлы.Список / REST v2)
Ссылка = Файлы.Опубликовать(ИдВложения);
// → "/pub/8f3c1a…"  (относительный путь; домен добавляет конфигурация)

// С параметрами
Опции = Новый Структура;
Опции.Вставить("КэшСекунд", 86400);          // Cache-Control: public, max-age=86400
Опции.Вставить("ДействуетДо", '20260901');    // после — 404
Опции.Вставить("Имя", "прайс-август.pdf");    // имя файла при скачивании
Ссылка = Файлы.Опубликовать(ИдВложения, Опции);

// Узнать текущую публикацию (повторный вызов Опубликовать возвращает ту же ссылку)
Ссылка = Файлы.Публикация(ИдВложения);        // Строка или Неопределено

// Отозвать
Файлы.СнятьПубликацию(ИдВложения);
```

Английские имена: `Files.Publish(id[, options])`, `Files.PublicURL(id)`,
`Files.Unpublish(id)`.

Отдача: `GET /pub/<токен>` — без авторизации, `Range`/`ETag`/`Last-Modified`,
`Cache-Control: public, max-age=<КэшСекунд>, immutable`.

## Принятые решения

1. **Публикация — явное действие, адрес — capability-токен.** 32 случайных
   байта (`crypto/rand`) в base64url. Знание токена = право на файл; UUID
   вложения наружу не попадает. Отзыв публикации ломает ссылку немедленно.
2. **Токен хранится хэшем? — Нет, хранится как есть.** Это не пароль: сервер
   обязан по токену из URL найти запись за один индексный поиск. Компрометация
   БД и так означает доступ ко всем файлам напрямую. (Явно фиксируем решение,
   чтобы к нему не возвращались на ревью.)
3. **Опасные типы не отдаются inline.** `text/html`, `image/svg+xml`,
   `application/xhtml+xml`, `application/xml` на своём домене = XSS с доступом
   к cookie админки. Такие файлы отдаются с
   `Content-Disposition: attachment` и `Content-Type: application/octet-stream`.
   Всегда: `X-Content-Type-Options: nosniff` и
   `Content-Security-Policy: default-src 'none'; sandbox`.
   Inline (в браузере) показываются только `image/*` (кроме svg),
   `application/pdf`, `text/plain`, `video/*`, `audio/*`.
4. **Публикация пишется в аудит.** Действие «файл стал доступен всему
   интернету» обязано иметь автора и время: запись в журнал аудита при
   `Опубликовать` и при `СнятьПубликацию` (кто — из контекста аудита,
   `storage.AuditUserLogin`, `storage/audit.go:35`).
5. **В песочнице запрещено.** Недоверенному коду (ИИ-генерация, marketplace,
   `RestrictedProfile`) глобал `Файлы.Опубликовать` не даётся: сгенерированный
   код не должен уметь выставить наружу вложение. Точка отсечения — там же, где
   режутся прочие привилегированные глобалы (см. решение 6 плана 123).
6. **Права при публикации не проверяются повторно.** Код конфигурации
   доверенный и уже исполняется с полномочиями своего контекста; вводить
   отдельную RBAC-проверку внутри `Опубликовать` — иллюзия защиты (код может
   прочитать файл и отдать его сам). Защита строится на решениях 4 и 5.
7. **Срок жизни необязателен.** `ДействуетДо` пуст → публикация бессрочная.
   Истёкшая публикация даёт `404` (не `410`): существование файла — тоже
   информация.
8. **Повторная публикация идемпотентна** — возвращает существующий токен и
   обновляет опции. Иначе каждый вызов в цикле рендера плодил бы записи.
9. **Предохранитель сети действует** — при выключенной сети `/pub/*` отвечает
   `503`, как и `/hs/*`. Публичная отдача файлов — та же поверхность
   конфигурации наружу.
10. **Удаление вложения удаляет публикацию** (FK с `ON DELETE CASCADE`) —
    иначе ссылка переживёт файл и будет отдавать 500 вместо 404.

## Хранилище

```sql
CREATE TABLE IF NOT EXISTS _public_files (
    token         TEXT PRIMARY KEY,
    attachment_id <uuid>  NOT NULL REFERENCES _attachments(id) ON DELETE CASCADE,
    filename      TEXT    NOT NULL DEFAULT '',
    cache_seconds INTEGER NOT NULL DEFAULT 3600,
    expires_at    <ts>    NULL,
    created_at    <ts>    NOT NULL,
    created_by    TEXT    NOT NULL DEFAULT ''
);
CREATE UNIQUE INDEX IF NOT EXISTS _public_files_att ON _public_files(attachment_id);
```

Типы `<uuid>`/`<ts>` — через существующий `Dialect` (как в остальных
`Ensure*Schema`). Уникальный индекс по `attachment_id` обеспечивает решение 8.

## Изменения в коде

| Файл | Что делаем |
|---|---|
| `internal/storage/public_files.go` (новый) | `EnsurePublicFilesSchema(ctx)`; `PublishAttachment(ctx, attID uuid.UUID, opts PublishOptions) (token string, err error)` (идемпотентно, генерация токена, запись аудита); `UnpublishAttachment(ctx, attID) error`; `PublicFileByToken(ctx, token) (*PublicFile, error)`; `PublicURLFor(ctx, attID) (string, error)` |
| `internal/storage/service_schema.go:24` | Добавить шаг `{"public files", db.EnsurePublicFilesSchema}` — **обязательно** (грабля #827) |
| `internal/ui/handlers_public_files.go` (новый) | `GET /pub/{token}`: поиск публикации, проверка срока, `OpenAttachment`, выбор inline/attachment по MIME (решение 3), заголовки, `http.ServeContent`; 404 на любую неудачу |
| `internal/ui/server.go` | Регистрация маршрута `/pub/{token}` вне session-middleware; предохранитель сети |
| `internal/ui/dsl_files_publish.go` (новый) | Методы глобала `Файлы`: `Опубликовать`/`Publish`, `Публикация`/`PublicURL`, `СнятьПубликацию`/`Unpublish` |
| `internal/dslvars/dslvars.go` | Инжекция методов в общую карту (доступно и в регламентных заданиях, и в HTTP-сервисах) |
| `internal/storage/public_files_test.go`, `internal/ui/handlers_public_files_test.go` (новые) | Тесты (см. ниже) |
| `docs/features.md` | Секция «Публичные ссылки на файлы», `status: testing` |
| `internal/cli/aiguide.go` | Раздел про `Файлы.Опубликовать` с предупреждением «ссылка = доступ» |

Сигнатуры:

```go
type PublishOptions struct {
    Filename     string
    CacheSeconds int
    ExpiresAt    *time.Time
}

type PublicFile struct {
    Token        string
    AttachmentID uuid.UUID
    Filename     string
    CacheSeconds int
    ExpiresAt    *time.Time
    CreatedAt    time.Time
    CreatedBy    string
}
```

## Тесты

**`internal/storage/public_files_test.go`** — через `dbtest.ForEachDialect`
(таблица и SQL затрагиваются, значит матричный тест обязателен по CLAUDE.md):

1. `TestPublishAttachment_Idempotent` — два вызова дают один токен, опции
   обновляются.
2. `TestPublishAttachment_TokenUniqueness` — 1000 публикаций дают 1000 разных
   токенов длиной 43 символа base64url.
3. `TestUnpublish` — после отзыва `PublicFileByToken` не находит запись.
4. `TestPublicFile_CascadeOnAttachmentDelete` — удаление вложения убирает
   публикацию (решение 10).
5. `TestPublicFile_Expired` — публикация с прошедшим `expires_at` не отдаётся.

**`internal/ui/handlers_public_files_test.go`** — через реальный HTTP:

6. `TestPublicFile_ServedAnonymously` — запрос **без cookie/токена** отдаёт
   содержимое (это точка входа пользователя, не приватная функция).
7. `TestPublicFile_UnknownToken404` — неизвестный/отозванный токен → 404.
8. `TestPublicFile_RangeAndETag` — `Range: bytes=0-3` даёт 206 и правильный
   кусок; повтор с `If-None-Match` → 304.
9. `TestPublicFile_CacheControl` — заголовок содержит `max-age` из опций.
10. `TestPublicFile_HTMLNotInline` — вложение `text/html` (и `image/svg+xml`)
    отдаётся с `Content-Disposition: attachment`, `Content-Type:
    application/octet-stream`, `nosniff` и CSP `sandbox`. **Регрессионный тест
    на XSS-вектор**, решение 3.
11. `TestPublicFile_ImageInline` — `image/png` отдаётся inline с родным MIME.
12. `TestPublicFile_NetworkDisabled503` — при выключенном предохранителе сети
    маршрут отвечает 503 (решение 9).
13. `TestPublicFile_PublishFromDSL` — публикация выполняется **исполнением
    DSL-кода** (`Файлы.Опубликовать`), затем файл скачивается по полученной
    ссылке. Полный путь пользователя, а не вызов Go-функции напрямую.
14. `TestPublicFile_SandboxDenied` — из sandbox-контекста глобал недоступен.
15. `TestPublicFile_AuditRecorded` — публикация оставляет запись в журнале
    аудита с логином автора.

## Verification

```powershell
taskkill /IM onebase.exe /F
go build ./... ; go test ./internal/storage/ ./internal/ui/
$env:TEST_DATABASE_URL="postgres://localhost/onebase_test"; go test -count=1 -tags=integration ./internal/storage
```

Ручная проверка:

```powershell
.\onebase.exe run --project <конф> --sqlite test.db --port 8080
# в обработке: Ссылка = Файлы.Опубликовать(ИдВложения); Сообщить(Ссылка);
curl -i http://localhost:8080/pub/<токен>          # 200, Cache-Control, ETag
curl -i -H "Range: bytes=0-9" http://localhost:8080/pub/<токен>   # 206
# в браузере в режиме инкогнито (без сессии) — картинка открывается
```

## Границы (чего не делаем)

- Не делаем ресайз/превью картинок на лету (аналог `resize_cache` Битрикса) —
  отдельный план, P2 в ТЗ на CMS.
- Не делаем публичную загрузку файлов (аноним → в базу): приём файлов от
  посетителей сайта — это формы + антиспам, отдельная работа плана 129.
- Не публикуем «папками» (весь справочник Медиа одним махом) — публикация
  поштучная и явная.
- Не вводим подписанные URL со сроком в самой подписи (HMAC): токен в БД
  отзываем мгновенно, подпись — нет.

## Эстимейт

| Работа | Оценка |
|---|---|
| Схема + storage-слой + аудит | 0.75 дня |
| HTTP-маршрут с политикой типов и заголовками | 0.5 дня |
| DSL-глобал + инжекция + запрет в песочнице | 0.5 дня |
| Тесты (15 сценариев, включая матричные) | 1 день |
| Документация | 0.25 дня |
| **Итого** | **~3 дня** |
