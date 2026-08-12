# webassets — вендоренные браузерные ассеты

Тяжёлые сторонние ассеты, которые нужны больше чем одному HTTP-серверу
(конфигуратор в `launcher` и инструменты разработчика в `ui`). Встроены через
`//go:embed` и отдаются по `/vendor/monaco/`. Самохостинг вместо CDN — редактор
и отладчик работают **офлайн**, десктопная база не зависит от интернета.

## Что внутри

Только **минимальный** срез Monaco (~4.2 МБ из ~14 МБ полного пакета):

```
monaco/vs/
  loader.js                                        # AMD-загрузчик
  editor/editor.main.js                            # ядро редактора
  editor/editor.main.css                           # стили (грузятся самим editor.main)
  base/worker/workerMain.js                        # web-воркер редактора
  base/browser/ui/codicons/codicon/codicon.ttf     # шрифт иконок
  basic-languages/yaml/yaml.js                      # подсветка YAML
```

Намеренно **не** вендорим: языковые сервисы TypeScript/CSS/HTML/JSON
(`min/vs/language/**`, ~7 МБ) и остальные грамматики `basic-languages/**`.
OneBase использует только `yaml`, `plaintext` и собственные Monarch-языки
(`onebase-dsl`, `onebase-query`), которые регистрируются в шаблонах и файлов
не требуют.

## Как обновить версию Monaco

Версия больше **не** прописана в шаблонах — путь всегда `/vendor/monaco/vs`.
Апгрейд = подмена файлов в этой папке:

```powershell
npm pack monaco-editor@<новая-версия>
tar xzf monaco-editor-<новая-версия>.tgz
# Скопировать из package/min/vs/ ровно те 6 файлов, что перечислены выше,
# сохраняя структуру каталогов, в internal/webassets/monaco/vs/.
go build ./...
go test ./internal/webassets/
```

Тест `assets_test.go` проверяет, что все 6 критичных файлов отдаются (200) и
что путь вне дерева даёт 404.

### Нюансы

- **404 в консоли webview** на `/vendor/monaco/...` после апгрейда означает,
  что новая версия лениво подгружает ещё какой-то файл — добавьте его в набор
  (и при желании в список в `assets_test.go`).
- **Новый язык** (кроме yaml/plaintext/наших) — докопируйте
  `basic-languages/<язык>/<язык>.js`.
- Дерево хранится байт-в-байт (`.gitattributes`: `monaco/** -text`), чтобы
  апгрейд давал чистый предсказуемый diff без нормализации EOL.

## Иконки Lucide (`lucide/sprite.svg`)

Весь набор Lucide (ISC) одним спрайтом ~480 КБ: каждая иконка — `<symbol
id="имя" viewBox="0 0 24 24">`, страница ссылается на неё через `<use
href="/vendor/lucide/sprite.svg?v=<хеш>#имя">`. Обводку символы не несут — её задаёт
внешний `<svg>` в `ui.LucideIcon` (`fill="none" stroke="currentColor"`), поэтому
цвет и размер по-прежнему наследуются от CSS.

Спрайт — **единственный источник правды**: список доступных имён не хранится
отдельным сгенерированным файлом, а разбирается из этих же байтов
(`LucideSymbolNames`, кэш на `sync.Once`). Разойтись им негде.

### Как обновить версию

```powershell
npm pack lucide-static@<новая-версия>
tar xzf lucide-static-<новая-версия>.tgz
# Скопировать package/sprite.svg и package/LICENSE в internal/webassets/lucide/
go test ./internal/webassets/ ./internal/ui/
```

`assets_test.go` проверяет, что спрайт отдаётся с `image/svg+xml`, что путь вне
дерева даёт 404 и что число разобранных имён совпадает с числом `<symbol>`. Тест
также разрешает в vendor-SVG только пассивную геометрию: скрипты, ссылки, стили,
обработчики событий и неизвестные элементы/атрибуты блокируют обновление.

### Нюансы

- Файл хранится байт-в-байт (`.gitattributes`: `lucide/sprite.svg -text`):
  CRLF-конверсия на Windows изменила бы вкомпилированный ассет.
- Директория `lucide` встраивается целиком: ISC `LICENSE` сопровождает копию
  спрайта и доступна рядом с ним по `/vendor/lucide/LICENSE`.
- Ссылка на **несуществующий** символ рисует пустоту молча — поэтому
  `ui.LucideIcon` сверяет имя со списком и подставляет `square`. Не убирайте
  эту сверку «за ненадобностью».
- Спрайт нужен обоим серверам: навигация базы и превью поля «Иконка» в
  конфигураторе ссылаются на один и тот же URL.
- `ui.LucideSpriteURL()` добавляет к пути первые 96 бит SHA-256 содержимого.
  Версионированный URL можно кэшировать как `immutable`; голый совместимый путь
  `/vendor/lucide/sprite.svg` всегда ревалидируется по ETag.

## Подключение в серверах

```go
r.Handle("/vendor/monaco/*", http.StripPrefix("/vendor/monaco/", webassets.MonacoHandler()))
```

Путь `/vendor/monaco/` намеренно отделён от catch-all `/static/*`, иначе chi
ругается на конфликт маршрутов. Шаблоны, использующие Monaco, должны:

1. задать `window.MonacoEnvironment.getWorkerUrl` (same-origin воркер из
   `/vendor/monaco/`) — иначе AMD-воркер не знает baseUrl и падает;
2. грузить `/vendor/monaco/vs/loader.js`;
3. вызвать `require.config({ paths: { vs: '/vendor/monaco/vs' }})`.
