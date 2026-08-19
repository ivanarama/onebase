<!-- summary: Быстрые итоги регистров, row-level access fail-closed, REST API v2, изолированные окна и усиление безопасности. -->

## Главное
**Параллельные сессии одной базы OneBase**

<img width="1873" height="927" alt="мультисессионность" src="https://github.com/user-attachments/assets/6fafc7af-025e-4fd5-95b5-06c93d6bf4d6" />

OneBase v0.9.0 закрывает крупный кусок масштабирования и безопасности: появились предрасчитанные итоги регистров для быстрых остатков, строковые политики доступа стали применяться fail-closed в запросах, а платформа получила REST API v2 и более зрелые режимы работы лаунчера.

## Новое

- **Итоги регистров накопления** — `totals: { enabled: true }` ведёт помесячные обороты в таблице `итоги_<рег>` и ускоряет `Остатки()` / `Остатки(&Момент)`. Есть команда `onebase recalc-totals` для полного пересчёта.
- **Row-level access policies** — строковый доступ применяется в query/API путях с финальной fail-closed проверкой внедрения предикатов.
- **REST API v2** — токены, RBAC guardrails, документы, отчёты и composition endpoint'ы.
- **Изолированные окна Предприятия** — параллельные пользовательские сессии и нативные WebView-профили на Windows.
- **Фоновые экспорты отчётов** — долгие выгрузки уходят в background jobs.
- **Условное оформление форм и журналов** — runtime API, табличные части, пользовательские колонки и настройки.

## Изменения

- `feat(storage/query)`: быстрый путь остатков через таблицы итогов, включая остатки на момент.
- `fix(query)`: `ОстаткиИОбороты(&Начало, &Конец)` с датами-параметрами корректно исполняется на SQLite; исправлен повтор анонимных `?`-плейсхолдеров.
- `feat(cli)`: `onebase recalc-totals --project ... --sqlite ... [--register ...]`.
- `fix(llm)`: `${env:VAR}` для секретов ИИ теперь разыменовывается и на пути `_settings`, без сохранения секрета обратно в конфиг.
- `ci`: добавлен `govulncheck`, обновлены уязвимые зависимости `pgx`, `x/net`, `x/crypto`, `x/text`, `x/sys`.
- `feat(dsl/configcheck)`: opt-in строгая лексическая область модулей и проверки утечек scope.
- `perf/storage`: индексы для регистров и guardrails для больших выборок.
- `fix/ui/launcher`: доработан редактор AI-моделей, static JS cache validation, разнесены крупные UI handlers и runtime assets.
- `docs`: обновлены планы масштабирования, безопасности и enterprise-подсистем.

## Breaking changes

Нет известных breaking changes для пользовательских конфигураций. Новые механизмы включаются явно: `totals.enabled`, RLS-политики, strict lexical scope и лимиты остаются opt-in либо совместимыми с текущими проектами.

## Установка

### Windows
1. Скачайте `onebase-windows-amd64.zip`.
2. Распакуйте в любую папку, например `C:\onebase\`.
3. Запустите `onebase-gui.exe` или из терминала: `onebase start`.

### Linux
```bash
tar xzf onebase-linux-amd64.tar.gz
sudo mv onebase-linux-amd64/onebase /usr/local/bin/
onebase start
```

### macOS
```bash
tar xzf onebase-darwin-amd64.tar.gz
sudo mv onebase-darwin-amd64/onebase /usr/local/bin/
onebase start
```

### Требования
- PostgreSQL 14+ для серверного режима или SQLite для локальных баз.
- Современный браузер: Chrome, Edge, Firefox.
