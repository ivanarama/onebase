# Техническая подготовка припаркованного PR

`needs-decision` само по себе не доказывает продуктовую развилку. Сначала прочитай
точную причину последней остановки и текущее состояние source. Исправленный CI,
технический конфликт и выбор пользовательского поведения требуют разных действий.
Решение владельца и действующий source-bound scope остаются обязательными: эта
процедура не расширяет механический allowlist MERGE и не снимает стопы сама.

Для уже разрешённой технической подготовки сохраняй отдельную устойчивую запись
`repository + PR + source SHA`. Она содержит точный scope с SHA-256, canonical
proof/epoch, identity и fingerprints исходных comments/contract, проверенное
разрешение конфликтов, результаты проверок и фактический этап передачи. Повтор
одного источника продолжает его запись, а не создаёт новое решение или комментарий.
Другой произвольный HEAD требует новой проверки; старый scope на него не переносится.
Доказанный переход intent/done продолжает штатную интеграционную цепочку.

Штатный OWNER может использовать `scripts/source_queue.py` как локальную очередь
состояний. Это отдельный операторский инструмент: он не выбирает GitHub-цель,
не исполняет код PR, не вызывает `next`, не выдаёт lease/proof, не обращается к
GitHub и не разрешает мутацию. Его установка и доставка инструкции в реальную
OWNER-задачу должны быть проверены; наличие файла в репозитории не включает
автоматику. Frozen процедуры и reader trees не заменяются на месте.

Дескриптор имеет ровно поля `repository`, `number`, `source`, `scope_path`,
`scope_sha256`. Путь — абсолютный путь к неизменяемому авторскому scope; внутри
scope сохраняются все исходные доказательства. Команды вызываются с аргументами
отдельно, без shell-eval. Публичные точки входа:

```text
python source_queue.py --queue QUEUE register --descriptor DESCRIPTOR
python source_queue.py --queue QUEUE list
python source_queue.py --queue QUEUE deliver --key KEY --native-db DB --series SERIES --directive FILE --directive-sha256 HASH
python source_queue.py --queue QUEUE verify --key KEY
python source_queue.py --queue QUEUE record --key KEY --phase PHASE --evidence FILE --evidence-sha256 HASH
```

`deliver` под одной native `BEGIN IMMEDIATE` CAS-транзакцией добавляет точный
блок в существующий незапущенный pending/rate_limited task и его unpaused series.
Task-specific инструкции, текущие running-задачи и другие серии сохраняются.
Нужен ровно один task без `started_at` и `session_id`. Нет task — фиксируется
`awaiting-pending-authority`; ни шаблон, ни GitHub не меняются. Не создавай второй
executor и не снимай парковку в ожидании. Следующий существующий OWNER повторяет
исходные гейты и доставляет ту же запись, когда task появился. Нативный commit
и локальная запись очереди — разные транзакции. До первой native-мутации отдельный
local commit сохраняет неизменяемый delivery-intent (series/native DB/directive/
scope/hash/block). После crash он запрещает смену адресата или инструкции, а точный
блок в native task/template позволяет повторить доставку без дубликата. Не выдавай local receipt
за атомарное подтверждение GitHub.

После доставки и непосредственно перед разрешённым снятием парковки выполни
`verify` и заново все canonical source/identity/proof/ship/CI/ownership/admission
гейты. `verify` требует ту же фактическую незапущенную задачу и неизменённые блоки;
started/paused/changed task останавливает передачу. Успех лишь подтверждает
доставку инструкции и всегда возвращает `mutation_authorized=false`. Не снимай
`hold`/`manual`, не меняй активные prompts и не обходи другой integration owner.

Фазы `released-to-merge`, `waiting-review-ci`, `merged`, `superseded` записываются
только как операторские наблюдения с неизменяемым evidence-файлом. Это не новые
канонические доказательства. Release подтверждай фактическим readback; merge —
серверным MergedEvent и штатным cleanup. Очередь не сбрасывает бюджеты/попытки и
не заменяет REVIEW, CI8, trusted ship, single-flight, intent/done, CAS или readback.
Новый integration HEAD проходит независимое REVIEW и все обязательные проверки.
