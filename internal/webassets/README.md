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
