# webview_go (vendored + onebase patch)

Копия https://github.com/webview/webview_go, коммит
`v0.0.0-20240831120633-6173450d4dd6` (см. `libs/*/version.txt`), лицензия MIT
(файлы LICENSE сохранены). Подключается через `replace` в корневом `go.mod`.

## Зачем вендорим

Патч `libs/webview/include/webview.h` (метод `win32_edge_engine::embed`, план 78
п. 4.2): каталог профиля WebView2 читается из переменной окружения
`ONEBASE_WEBVIEW_PROFILE`. Оригинал жёстко берёт `%APPDATA%\<имя exe>` и передаёт
его явным параметром `CreateCoreWebView2EnvironmentWithOptions` — из-за этого все
окна одного exe делят один профиль (cookie-jar), а стандартная переменная
`WEBVIEW2_USER_DATA_FOLDER` игнорируется (явный параметр сильнее). Без патча
изолированные нативные окна Предприятия невозможны.

Второй патч там же (метод `embed`): выключены встроенное автозаполнение форм и
сохранение паролей WebView2 (`ICoreWebView2Settings4`:
`IsGeneralAutofillEnabled`, `IsPasswordAutosaveEnabled`). У SDK по умолчанию
`IsGeneralAutofillEnabled` = TRUE — из-за него окно Предприятия подсказывало
оператору телефоны и адреса из прошлых звонков; `IsPasswordAutosaveEnabled` =
FALSE. Патч явно ставит оба свойства в FALSE, не полагаясь на умолчание. Без
`ICoreWebView2Settings4` (старый runtime) окно открывается как раньше.

Патчи помечены комментарием `onebase patch` — при обновлении vendored-копии
перенесите их в новую версию.

Из оригинала не скопированы только examples/, CI и тесты — код модуля не менялся,
кроме описанных патчей.
