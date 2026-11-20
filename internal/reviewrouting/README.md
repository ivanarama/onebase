# Чтение review-routing-v1 в pipelinehealth

Принятый срез A ([#1908](https://github.com/ivanarama/onebase/issues/1908) /
[PromptPilot #151](https://github.com/ivanarama/PromptPilot/issues/151)) добавляет
строгую историю и source-bound технический FIX. Срез B (#1909 / PromptPilot #152)
добавляет положительный typed proof для native MERGE/TAIL и парную межстадийную
регрессию. Совместная приёмка точных версий остаётся обязательной; установка,
Dashboard, включение и наблюдение пилота относятся к отдельному срезу C.
По умолчанию новый режим выключен. Обычный JSON-отчёт checker не меняется.

## Публичные команды

Проверить историю одного PR без выборки очереди:

```sh
go run ./tools/pipelinehealth -repo ivanarama/PromptPilot -owner ivanarama -routing-inspect 227 -json
```

Номер после `-routing-inspect` — номер PR, а не issue. Команда подходит также
для закрытого или влитого PR. Она только читает GitHub; результат имеет purpose
`read-only-routing-inspection` и `mutation_authorized: false`. Для OneBase укажите
`-repo ivanarama/onebase` и номер его PR. Для офлайн-проверки можно передать `-prs` с одним
PR и полным нормализованным `routing_snapshot`; такой результат явно помечен
`unverified-offline-input`.

Диагностический режим очереди включается отдельно:

```sh
go run ./tools/pipelinehealth -json -technical-handoff -routing-max-attempts 2 -contract .claude/skills/review-queue/SKILL.md
```

Он требует GraphQL. Checker дважды читает всю timeline каждого управляемого PR,
проверяет идентичность заголовков на всех страницах и полное совпадение двух
проходов. В снимке сохраняются полная идентичность PR и автора, комментарии,
перемещения HEAD/base, изменения меток и удаления. Частичный ответ, повторный
курсор, исчезнувший fork или смена идентичности не становятся пустой историей.
Для глобального ID автора запрос использует `author{login ... on Node{id}}`:
поле `id` отсутствует непосредственно в интерфейсе GitHub `Actor`.

## Что проверяется

`internal/reviewrouting` читает закрытые схемы publication, lease, claim,
completion, attempt, phase, settlement и resume. Канонические UTF-8 JSON/LF,
хеши полных отчётов, contexts и PR identity совпадают с Python-реализацией
PromptPilot. Большие server ID сохраняются десятичным текстом; дробные числа,
повторяющиеся JSON-поля, неизвестные категории и повреждённый Unicode отклоняются.

Исторический reducer проверяет ранний канонический claim, владельца lease
в момент каждой записи, последовательность фаз и попыток, точный источник
исправления и непрерывный бюджет. Старые legacy-круги считаются по неизменяемой
связи review → claim → completion без восстановления прошлой epoch из
сегодняшнего timeline: GitHub может вставить поздно добавленный коммит по его
старой дате. Текущий HEAD по-прежнему проходит полный epoch-safety gate.
Для новых typed REVIEW `audit-history-v1` сохраняет исходный header и
упорядоченный manifest cursor/node-ID. Checker восстанавливает **сохранённый**
снимок из тех же неизменённых узлов и проверяет полный snapshot hash,
идентичность PR, автора и epoch/anchor. Текущие метки/state и поздние вставки
коммитов по старой дате не стирают завершённый аудит. Пропажа, перестановка
или изменение исходного узла и постороннее событие остаются инженерной
парковкой. Для старых записей разработки без manifest требуется доступный
точный prefix; отсутствующие прошлые метаданные не выдумываются. Перед живой
мутацией по-прежнему проверяется полный текущий prefix и текущая epoch.
Одна marker-строка
не увеличивает глубину ревью. Удалённая или изменённая доверенная история
остаётся неизвестной.

Две дополнительные технические попытки — конечный бюджет всего PR. Новый HEAD,
задача, истечение lease или settlement не возвращают потраченный слот. После
settlement только один resume разрешает независимое REVIEW точного нового HEAD.
Успешный текст агента, отсутствие PID и статус задачи не доказывают остановку
старого исполнителя. Это отдельно проверяет координатор PromptPilot.

## Диагностика и граница полномочий

Режим очереди добавляет `technical_waiting`, `engineering_waiting`, закрытый
`technical_fix_candidates` с полным source и отдельный
`technical_fix_settlement_candidates`. Техническая передача или инженерная
парковка не получают MERGE и не занимают интеграционную полосу. Ошибка доказательства
одного PR не превращает его в новое обычное REVIEW и не скрывает другие цели.
Продуктовый выбор и исключение из правил безопасности остаются в `human_waiting`.

`technical_fix_progress_candidates` содержит только точный старый source/HEAD,
attempt ID и наблюдаемый новый HEAD. Это readback hint, а не подтверждение
успешного push или остановки executor. Координатор независимо проверяет конечный
forward graph, полную identity, source trailers, исходную границу процесса,
локальную задачу и reservation перед любой финализацией. Неподтверждённый новый
HEAD сохраняет инженерную диагностику и возможность административного settlement;
другая модельная попытка из progress hint не создаётся.

`review_routing_contract` появляется только при полном снимке и совпадении
четырёх явно версионированных деревьев потребителей REVIEW/FIX/MERGE/TAIL.
Проверяются все их файлы, а не только SKILL.md. Отсутствующая версия, ссылка,
необычный тип файла или превышение лимита закрывают совместимость. Сам хеш
подтверждает байты процедур, не их независимую приёмку.

При явном `-technical-handoff`, полном снимке и совместимых четырёх деревьях
checker публикует отдельные `fix_bridge_contract` и `fix_execution_contract`.
Их закрытые purposes совпадают с PromptPilot. Это декларации совместимости,
а не lease, разрешение на процесс, push или merge. Изменённые процедуры находятся
в исходниках; установленные процедуры, настройки и планировщик не обновляются.
Полный paired B reader дополнительно объявляет `positive_review_contract`
(`positive-review-v1`, purposes `merge`/`tail`) и `positive_merge_candidates`.
Положительный source содержит исходные typed ID, identity/автора, epoch и node
IDs, а не синтетический legacy proof. Для MERGE требуются trusted ship и все
независимые live-гейты координатора. Technical/engineering не создают merge owner.
Контракт четырёх процедур описан в
[канонической процедуре](../../.claude/skills/review-queue/references/review-routing-v1.md).

`-routing-inspect M -routing-positive-purpose merge|tail` проверяет положительный
source без выборки очереди и мутаций. Для `tail` нужна завершённая до единственного
`MergedEvent` публикация текущего HEAD; после merge допустим лишь один конечный
delete ветки без restore. JSON сохраняет `mutation_authorized:false`; при
недостаточном proof содержит `positive_review_error`, а не новый исполнимый маршрут.
Политика флагов и все mutation-гейты остаются во внешнем координаторе.

## Проверка совместимости

В `internal/reviewrouting/testdata` сохранены побайтовые исходные эталоны
PromptPilot с manifest и дополнительные результаты его исторического reducer.
Тесты сравнивают полные wire bytes, contexts, identity hashes, ledger, depth и
audit binding. Публичные CLI-сценарии проверяют публикацию, одну выдачу START,
post-push REVIEW, исчерпание бюджета, settlement/resume, запрет повторного resume
и продолжение другой цели рядом с инженерной парковкой.

Во время разработки 1 ноября 2026 года оба reader получили с настоящего GitHub
одинаковый полный снимок [PromptPilot PR #227](https://github.com/ivanarama/PromptPilot/pull/227):
SHA-256 канонического снимка
`57c5c1e6fefca3cb01a85f61ac7f49b819fb7f711bab74bdfff98ddb0e3d05a4`,
21 timeline edge, HEAD `1ebedcc79a2983086e5d13b5410870ea4cf83851`.
Это проверка реального read-only транспорта на исходниках разработки. Она не
заменяет повторную проверку окончательных коммитов, обязательные CI, независимое
ревью и полный публичный сценарий согласованной пары.
