# Текущий приоритет развития onebase

Дата ревизии: 2026-07-07.

## Рекомендуемый порядок

1. **План 76/78 — guardrails готовности к нагрузке и сессии закрыты для
   single-process PostgreSQL.** Закрыты REST RBAC/list limits, атомарная
   optimistic locking запись, declarative indexes + tablepart/register indexes,
   server-side reference picker, bounded parent-folder options, runtime
   limits/backpressure, metrics, PostgreSQL advisory locks для
   `БлокировкаДанных` в Save-хуках, background export job для Excel/PDF,
   k6 PostgreSQL validation runner, параллельные сессии, админка активных
   сессий, изолированные окна Предприятия и нативные WebView2-профили на
   Windows. В очереди остаются future horizontal scale и row-level security как
   отдельные решения.

2. **План 60B — marketplace конфигураций.**
   Версионирование конфигурации в БД и UI истории уже реализованы, а shipped
   examples/templates теперь проходят CI lint-gate. Marketplace можно брать как
   следующий продуктовый слой над этими конфигурациями.

3. **План 55, этап 3 — остаток разбора template-bound UI JS.**
   Фаза 3a уже вынесла глобальные UI-скрипты и reference picker в
   `/static/ui.js`; дальше стоит выносить скрипты списков/форм/отчётов только
   через явный bootstrap JSON, потому что они завязаны на Go-template данные.

4. **План 79 — row-level access для данных.**
   Объектный RBAC уже закрывает права на справочник/документ целиком, но
   сценарии "менеджер видит только свои сделки" требуют отдельной платформенной
   модели row filters. Проект зафиксирован в
   [`79-row-level-access.md`](79-row-level-access.md): app-level policy поверх
   RBAC, SQL-side фильтры для UI/REST и fail-closed контур для отчетов/AI до
   alias-aware внедрения policy в query compiler.

## Зафиксированные находки аудита

- **План 60A уже не нужно брать.** Ядро `_config_versions`, snapshots, diff,
  rollback, UI истории и export ZIP/OBZ уже есть в коде; впереди только
  marketplace-часть.
- **План 43.2 уже закрыт готовым PR #203.** После merge он должен уйти из
  активного списка.
- **План 76 стоит зафиксировать как следующий guardrail-план.** Текущий REST API,
  большие списки, reference-options, конкурентная запись и тяжёлые отчёты
  требуют ограничителей; core-срез A/B/C/D/E/F, background export UX, k6
  PostgreSQL validation runner и сессионная часть Plan 78 уже реализованы,
  остатки описаны выше.
- **`onebase lint` по shipped examples/templates теперь закреплен в CI.**
  PR #263 собирает CLI и прогоняет `onebase lint --json` по `examples/*` и
  `templates/*`, падая на любых issues/warnings.
- **Особая находка:** `examples/tasks/src/учёт_времени.posting.os` привязан к
  имени файла с подчёркиванием и использует `this.Задача.Проект`; после PR #261
  Plan 34 F3 закрыт безопасным single-hop доступом к реквизитам ссылок, поэтому
  здесь остаётся только нормализация имён posting-файлов.

## Не в топе сейчас

- **План 34 F3** реализован в PR #261: `this.X.Y` / `Стр.X.Y` работают через
  предзагрузку и request-scope кэш; вложенное неявное разыменование не включено.
- **План 28** и **план 70** фактически реализованы.
- **План 56** полностью реализован: CI/race/coverage, lint baseline, RBAC
  вложений, `/metrics`, `slog` и `onebase lint`.
- **План 65** реализован: richtext-поле, Quill, санитайзер, лимит размера и
  вывод richtext в HTML/PDF печатных формах.
- **План 152 AI/dev tools** реализован: `fmt`, schema/describe/query/eval/MCP,
  impact/refactor helpers, tool trace и rollback через snapshots.
- **План 55, этап 3** частично закрыт: глобальный UI JS вынесен в
  `/static/ui.js`; остаток — template-bound скрипты.
- **План 46** относится скорее к упаковке и маркетингу PWA.
