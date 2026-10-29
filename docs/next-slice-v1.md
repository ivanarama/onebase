# Next-slice v1: bootstrap A

Этот контракт реализует только срез A [плана 163](../Plans/163-next-slice-handoff.md)
для #1379. Общая Go-грамматика и диагностический reducer находятся в
`internal/nextslice`, общие входы/ожидания —
`internal/nextslice/testdata/golden-v1.json`. Они не выдают разрешений на мутации:
`authoritative=false`, `executable=false` даже у завершённого handoff.
Текущий установленный PromptPilot не обновляется этим PR.

## Байты и manifest

План — UTF-8 без BOM, CR и NUL. SHA-256 вычисляется по всему исходному файлу,
включая конечный LF, если он есть. Путь имеет вид
`Plans/[0-9]+-[a-z0-9][a-z0-9-]*.md`; вложенные каталоги и traversal запрещены.
Перед каждым заголовком среза — один отдельный HTML-блок:

```markdown
<!-- pp:plan-slice key=A next=B -->
### Срез A

<!-- pp:plan-slice key=B next=done -->
### Срез B
```

Не менее двух уникальных ключей `[A-Za-z0-9][A-Za-z0-9._-]{0,31}`;
`done` зарезервирован. Ровно одна линейная цепочка, один начальный ключ и один
переход в done, без циклов, недостижимых ключей и нескольких предшественников.
Markdown AST исключает примеры в code fences/code spans. Неправильный настоящий
marker не игнорируется. Порядок заголовков не подменяет явную цепочку.
План 159 получает A→B→C→D→done без изменения содержания срезов.

## Строгие records

Records — полные тела отдельных комментариев, ASCII/LF, без префикса/суффикса,
с одним необязательным финальным LF. Порядок полей фиксирован, пробелы,
повторы, неизвестные поля/версии и пустые значения отвергаются. Номера issues,
PR и REST comments — положительные десятичные строки без ведущих нулей;
BigInt не преобразуется в float или платформенный int. SHA — lowercase hex,
HEAD/merge 40 знаков, остальные хеши 64. Формы декларации и root точно
соответствуют разделам «Доказательство активного среза» и «Committed next-slice
handoff» плана 163. `done` допустим в next декларации, но не в next root.

Декларация сверяет issue, путь/хеш плана, объявленный ключ и next, HEAD и
SHA-256 точного тела PR. Тело содержит ровно по одной строке Slice-Issue,
Slice-Plan, Slice-Key и Next-Slice. Частичный PR требует `Part of #N` без
closing-ссылки на ведущую, финальный — одну closing-ссылку. Проверка bootstrap
консервативна: closing-подобный текст тоже может потребовать человека. Полный
GitHub closing/reference gate и привязка review epoch поставляются срезом C.
Старая редактируемая фраза «это срез A» декларацию не заменяет.

Для fingerprint root фиксируется точная сериализация ранее неявного поля:
все строки root от `pp-next-slice-v1` до `previous-handoff=...` включительно,
каждая с LF, затем `decision-sha256=<сохранённый digest решения>\n`.
Marker claim и UUID владельца в fingerprint не входят; JSON и Unicode
нормализация не используются. Разные UUID у побайтово эквивалентных payload
допустимы; каноничен первый доверенный root по server-createdAt/BigInt id.
Различные payload/fingerprints не объединяются по догадке.

Точные phase records (отдельные строки):

```text
<!-- pp:next-slice-lease claim=<root-id> previous=<active-id> owner=<lowercase-uuid> -->
<!-- pp:next-slice-labels claim=<root-id> fingerprint-sha256=<64hex> events-through=<event-id|none> labels-sha256=<64hex> -->
<!-- pp:next-slice-done claim=<root-id> fingerprint-sha256=<64hex> -->
```

Root/lease действует 30 минут от server-createdAt. Для previous выбирается
первый допустимый child; до expiry продлевает тот же UUID, после — takeover.
Loser не становится владельцем. Labels/done требуют canonical root и его
fingerprint, порядок root→labels→done, совпадение наблюдённого labels hash;
phase после истечения активной lease не принимается. Даже полный fixture
остаётся диагностическим recovery с сообщением bootstrap mutations disabled.
Неопознанные, отредактированные/удалённые доверенные records, конфликт,
неполные доказательства, hold/manual/human transition — human.

## Публичная проверка и наблюдаемость

```text
go run ./tools/pipelinehealth -slice-vectors internal/nextslice/testdata/golden-v1.json
go run ./tools/pipelinehealth -prs tools/pipelinehealth/testdata/next-slice/legacy-1374-prs.json -issues tools/pipelinehealth/testdata/next-slice/legacy-1274-issues.json -json
```

Первый запуск строго проверяет общий JSON-набор, не читает GitHub, возвращает
число пройденных vectors и `mutations_enabled=false`. Это воспроизводимый
контракт для будущего внешнего потребителя; PromptPilot должен прогнать те же
vectors перед объявлением capability `next-slice-v1`.

Второй показывает историческую #1274 сразу после merge #1374 как
`legacy-bootstrap-required` в `next_slice_human_waiting` и `human_waiting`;
она не исчезает из отчёта и не попадает в FIX. Fixture содержит реальные
неизменяемые HEAD/MergedEvent SHA и комментарии, существовавшие к merge
07.09.2026 21:27:48 UTC. Labels — историческое наблюдение из #1379, а не
сегодняшний статус #1274: теперь она закрыта. Реальные issues/refs не меняются.

`pipelinehealth` добавляет массивы `next_slice_recovery` и
`next_slice_human_waiting`. Старые потребители видят human и обычные findings.
Открытый PR продолжает обычную REVIEW-очередь; in-work без открытого PR больше
не скрывает orphan. Только точный неотредактированный owner active/root record
с выбранным plan-ready запускает targeted lookup закрытого PR внутри прежних
полных стабильных GraphQL snapshots и их бюджета. Комментарии пагинируются,
MergedEvent проверяется на единственность и совпадение merge SHA. Другие issues
не запускают сканирование закрытых PR. `-plan-dir` позволяет задать каталог
планов; чтение ограничено `os.OpenRoot`.

## Что остаётся выключенным

Нет POST/DELETE/label/ref/merge, branch claims, снятия in-work/hold, нового
FIX candidate или capability negotiation. Живой observer не восстанавливает
decision digest из редактируемого текста и не доказывает историческое удаление
комментария: modern chain без полного proof остаётся human. Полная сборка
decision/review/timeline/deletion proofs, consumption previous-handoff и
применение labels event watermark относятся к B/C. Typed reducer допускает
эти доказательства только как вход для общих offline vectors; его результат
никогда не является mutation authority.

#1379 остаётся на hold после публикации bootstrap; принятый merge A не означает
выполнение B–D. Legacy bootstrap на реальной issue — отдельный срез D и отдельная
свежая проверка состояния. Односрезовые FIX/REVIEW/MERGE не меняются. Откат A —
revert наблюдаемости/manifest, без удаления GitHub comments и refs.
