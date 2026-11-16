# Ограниченное ядро источника строк отчёта (ENG12)

Это первый source срез `internal/report/rowsource`, **без подключения к runtime**.
Он не делает существующий `result_proc` преобразователем строк: тот продолжает
возвращать ТабличныйДокумент. Утверждение о готовом SDK/A004/A005 не следует из
этого пакета. Зависимость: исходниковый ENG04 #2640 с типами/lineage и общими gates.

## API и доказательства

`rowsource.Run(ctx, db, registry, initialQuery, source)` сам компилирует основной
и дополнительные запросы с текущими RLS, object read и обязательным
`ReportOutputQueryMaskPlan`, затем доказывает **каждую** входную колонку через
`ProjectionColumn.Path`, `SourceOccurrences`, metadata type и `RefColumns`.
`source` содержит доверенные Report/Entry/Code и Extras{Name,Type}; объявление
имени/типа extra не является доказательством происхождения его значения.

Основная выборка поступает как массив структур `Результат`. Экспортная функция
без параметров добавляет заранее объявленные scalar поля и возвращает этот
массив. Число/порядок строк, identity исходных Struct и каждое исходное значение
проверяются после выполнения. Возвращаемая таблица собирается из host originals
и проверенных extras. NULL, пустая строка, точное число, дата и UUID не заменяются
строкой или угаданным типом. Ref передаётся без manager/resolver, с техническим
UUID вместо исходной подписи. Extra reference пока не поддержан.

`Result.Columns` содержит host-generated origins: source kind/name/tablepart/
canonical field/type/ref root/path для полей; `literal` отдельно; доказанный
`validated-program` связан с `ModuleSHA256` и списком `Operations`. Для каждого
extra берётся консервативное объединение всех входов всей сессии. Декларация,
alias или возвращённый JSON в доказательство не входят. Смена registry generation
даёт отказ. Origins, module hash, generation и операции в клиентский JSON не идут.

## Профиль DSL

Свежий Interpreter получает только Результат и guarded Query factory, без
`buildDSLVarsTx`, common-module/runtime lookup, managers, транзакций, вложений,
сообщений, внешних callback и ambient secrets. Query wrapper непрозрачен даже
для Строка(). Внутренние `__*` имена недоступны.

AST разрешает присваивания, var, if, foreach/numeric for, return/break/continue;
скалярные литералы, чтение полей/индексов, массивы, bounded арифметику;
локальные проверенные функции. While/try/eval/неизвестный узел запрещены.
Конструкторы: массив/структура/соответствие/запрос. Методы: работа с этими данными,
УстановитьПараметр/Выполнить, УникальныйИдентификатор. Чистые функции: Строка,
СокрЛП, ВРег/НРег, Лев/Прав/Сред, СтрДлина/СтрНайти/СтрЗаменить,
СтрСоединить/СтрРазделить, Символ, ПустаяСтрока, ЗначениеЗаполнено, ТипЗнч
и явно перечисленные английские синонимы. Новый builtin не открывается автоматически.

Начальная выборка и каждый дополнительный Запрос используют **один** dataset/input
кодек kernel. Общий interpreter/query_proxy не участвует и не изменялся: SQL NULL
не подменяется typedempty, JOIN number материализуется как то же точное число.
Технические implicit ref/DSL aliases получаются только из compiler mappings,
не могут быть перезаписаны extra и проверяются как immutable часть DSL input.
Параметры Query допускают bounded scalar/ref/array данные, отвергают cycles и
capabilities; namespace lookup/RLS/mandatory policy до чтения сохранены.

RestrictedProfile запрещает сеть/файлы/exec/secrets; storage.ReadOnlyContext
действует на границе БД. Лимиты: 10 секунд, 100000 loop iterations, 256 запросов,
50000 исходных/100000 суммарно прочитанных строк, 8MiB входов/выхода, 1MiB значение,
128 колонок, 32 extras; source/parser/SQL/decimal/trace также ограничены.
Отказ не возвращает частичные строки.

## Проверенный минимум и дальнейшая интеграция

Синтетический обычный источник: одна заявка, две одинаковые работы из ТЧ,
разделитель `; ` + newline, заметка, Count=1/Amount=125.50. Дубликаты и порядок
сохраняются, native ref/date типы и обычный compose — тоже. Проверены immutable
originals, NULL, скрытые поля/исходные labels даже admin, дополнительные защищённые
чтения, object/field/report rights, RLS ТЧ, budget/cancel, unknown output/DSL.

**Остаток:** packaging/model/выбранный вариант и единый обычный UI/API/export/
background pipeline; эффективные multiSQL фильтры до лимита/compose; безопасные
подписи через owner gate; один server-side snapshot с текущими actor/read/RLS/
metadata/policy проверками; PG/native общая приёмка. Source543/report_pii и полный
ENG06 runtime пока HOLD. Основные native выражения/литералы, не имеющие доказанного
compiler типа/lineage, сейчас отклоняются; это намеренный предел первого ядра.
Сложные исходные A004/A005 этим тестом ещё не объявляются принятыми.


## Зарегистрированное определение (следующий source-срез)

Модель `report.RowSourceDefinition` содержит фиксированную `procedure` и до32
`columns: [{name: Работы, type: string}]` в `row_source`. Допустимы только scalar
string/number/bool/date/datetime; reference extra и internal/повторные имена
отклоняются. `onebase check` и YAML lint проверяют форму определения.

`rowsource.RunRegistered(ctx, db, registry, canonicalReportName, initialQuery,
trustedCodeLookup)` выбирает entry/extra schema из текущего зарегистрированного
отчёта. Loader получает каноническое имя этого отчёта и читает только его `.rep.os`.
External reports в этом первом профиле отклоняются до loader: им нужен отдельный
доказанный источник модуля. Loader является доверенным host loader, не callback из request/DSL. Код не берётся из
preset/запроса/возвращаемого descriptor. report.run и корректность metadata
проверяются до loader; ошибки/cancel/reload не дают частичного результата. Схема
копируется до загрузки, а private expected generation проверяется при входе Run
и затем обычной end-of-execution проверкой. Изменение роли после загрузки тоже
повторно проверяет Run. Все kernel read/RLS/ordinary field/report gates прежние.

InitialQuery обязан поступать от host после выбора доверенного варианта B30 и
нормализации типов объявленных параметров. Этот helper не принимает клиентский
SQL как trusted variant и сам не реализует B30 variant.Query. CodeLookup обязан
уважать context и быть ограниченным loader конфигурации, не общий module runner.

Это API и metadata source-срез. **Существующие HTTP/API/export/background handlers
на него ещё не переключены, runtime SDK с работающим row_source не опубликован.**
Одно добавление YAML ключа не запускает enrichment текущим бинарником. Конфиги
A004/A005 не менялись. Подключение к обычному compose и общему output/snapshot
контракту остаётся отдельной дельтой после совместимого B30/ENG06/ENG16.


## Host input frame v1: inputs-only foundation

`CaptureInputFrame(ctx, db, registry, report, request, preset, codeLookup)` получает
**принятые HOST значения**, уже нормализованные действующим transport parser,
в `AcceptedParameter{Text, Parsed}`. Этот Go тип не надо декодировать из клиентского
JSON как evidence. Имена разрешаются по текущим metadata Param; конкретный parsed
scalar type/ref root проверяются. Text используется для presence, не для определения
доверия. Request приоритетнее preset; принятый __has/пустой текст остаётся user-input.

Private handle связывает canonical report/variant, template/module/profile hash,
metadata generation+fingerprint, actor/role/attrs, strict compiled SQL/args/RLS,
FormatVersion2 и input slot/source/presence/type/ref/typed digest. Для старого source
model варианты меняют composition и используют основной query; B30 variant.Query
должен отдельно выбираться host из доверенных metadata, не из client SQL.

`value`, `explicit-empty`, `missing` различаются; false и0 не становятся missing.
Равные typed bytes из request/preset/default сохраняют разные kind/channel.
Отсутствие принятого значения использует только штатный empty-value parser;
сохраняются metadata default-expression identity и missing user controls.
Непустой metadata default с clock/constant/context зависимостями пока STOP до
отдельного host binding. Timezone — server-context, не fixed literal. Digests и
raw values остаются private; неизвестный слот/descriptor/type/root отклоняется.

`frame.Revalidate` повторяет текущий strict compile/object/field/report/RLS/mandatory
boundary и сверяет metadata/actor до/после loader, registry identity/generation,
module/profile/query/input fingerprints. Host передаёт свежий авторизованный ctx;
helper не загружает DB session/roles вместо middleware. Registry maps сортируются
для canonical fingerprints, SourceRef-key RLS map записывается sorted snapshot.
DB/registry/timezone process identity также проверяются. Неизвестный host state
кодеком не превращается в `{}`. Marshal/Unmarshal JSON отвергаются, fmt/String
показывают только ReportInputFrame; Inspect возвращает redacted copies без значений
и private digests. Клиентским literal/JSON такой handle не создать.

`program_evidence` связывает lexical AST literal с exact module/profile/node/type/
typed value, не с равенством строк. Чужой same-valued AST node и mutation invalid.
Для потенциальных `.Text/.Текст` assignment фиксируются отдельные statement/value
ordinals; concat/variable/function result/compound assignment не получает lexical
literal proof. Даже static node ВСЕГДА требует receiver/control bindings. Date
literal дополнительно требует timezone context. Этот ledger не наблюдает текущий
runtime Query.Text provenance и не разрешает protected-operation recipe.

**InputFrame не является successful producer receipt или final filter schema.**
Он не читает result rows, не запускает DSL, не переносит input values в interpreter
и не расширяет текущий readonly capability profile. Existing Result/Column/JSON
по-прежнему не authority. Полный frame all data/control origins, masked originals+
minimal DSL subset, actual query text taint, readonly effectiveFilters, extra schema
и единый UI/API/export/background/current-auth snapshot остаются отдельными шагами.
Native literal1/DateDiff/bare ref presentation/protected Адрес остаются current STOP.
Новых exceptions/callback allow/карточных правил этим кодом не введено; ENG06 HOLD.

### Actual Query.Text assignment ledger (source only)

The private host ledger now lowers validated plain `.Text`/`.Текст` assignments
to per-site host callbacks before interpreting the pure module. It evaluates the
right-hand side before the receiver, once each, as ordinary DSL assignment does.
The callback records the actual opaque Query instance, text version and original
AST site. Only the exact direct StringLit node may retain lexical evidence.
A variable, concatenation, function return, ordinary structure property or a
same-valued subsequent/compound assignment cannot inherit that evidence.

Each additional Query execution records private typed digests of the text and
normalized parameters. The trace is bounded with the query budget, excluded from
JSON and redacted when formatted. Cross-execution Query receivers are rejected.
Non-query Text fields retain ordinary structure assignment semantics.

All sites retain unresolved control lineage, including literals inside branches.
Dynamic text additionally retains unresolved data lineage. This observation
ledger does not authorize protected comparisons and does not implement full
runtime origin propagation, a successful producer seal or final filter schema.
No additional SQL expressions/fields become allowed. Purity profile version is
now `rowsource-pure/2`; old input-frame profile hashes cannot carry forward.

### Execution from captured host inputs (source only)

`RunCapturedInputs(ctx, db, registry, frame)` accepts only a private InputFrame.
It revalidates that frame and takes an immutable canonical query/module/entry/
extra-schema/typed-parameter snapshot from the matching metadata. It then runs
the guarded kernel with the captured generation and revalidates the complete
input identity again before returning any result. A stale actor, module, query,
metadata, RLS/context, database identity or cancellation rejects the whole result.
Client JSON cannot supply query/code/schema or reconstruct the private frame.
The caller must pass a fresh host-authorized context, as for frame revalidation.

This helper returns ordinary mutable `Result` data; it does not mint a successful
producer receipt, final filter schema or snapshot capability. Full data/control
lineage and masked-original/minimal-DSL channels remain pending. It does not
inject input values, effectiveFilters or ambient globals into DSL, activate a
report handler, or enable additional SQL/native expressions. Source-main variants
still share the canonical report query; B30 per-variant adaptation is separate.

### Explicit initial DSL input subset (source only)

Trusted `row_source.inputs: [Request, Note]` selects 1..128 unique initial result
columns for DSL. Names select from the existing strict compiler-proven schema;
they do not prove source lineage, type or authority. Every original output column
still passes the unchanged all-column schema/read/RLS/ordinary/mandatory policy
before SQL. Protected, unknown expression or unknown reference output still STOP,
even when excluded from the DSL subset. This is data minimization, not a new ACL.

RunRegistered and RunCapturedInputs copy that selection and construct fresh DSL
structures containing only its typed columns and their compiler-proven aliases.
The other proven originals remain host-side. Exact private row identity/order is
checked after DSL; the output is rebuilt from host originals plus typed extras.
An injected excluded original is rejected. Repeated UUIDs remain separate rows.
The full original output retains its previous driver/value representation for
ordinary compose; the DSL typed scalar codec remains unchanged.

Omitting `inputs` preserves the earlier all-proven-columns source kernel behavior;
explicit empty, duplicate/internal/unknown names reject. The explicit subset is
bound by captured metadata identity. Additional Query projections still pass the
same strict independent schema checks and are not granted access to masked data.
YAML/check validate definition shape; actual column membership/type/lineage are
validated by the runtime compile boundary, not by a name in this list.

This slice does not enable protected masked/native passthrough. SQL literal1,
DateDiff, bare reference presentation and protected Address require their separate
compiler/owner proofs and remain STOP. No current app config/handler/SDK is wired.

### Captured-only strict SQL proof sidecar (source candidate)

The proposed captured-input path uses portable CompileReportLineage with only
kernel-built CompileOpts and retains the immutable receipt privately. Every
Outputs/Uses expression is checked; every Use remains a membership/control input
for all outputs. Each origin must be a unique current canonical source field in
its exact FROM occurrence and pass ordinary object/field and mandatory report
policy. The first adapter conservatively rejects multi-hop navigation and
unbound/ambiguous SQL parameters; it adds no operator declassification.

The existing all-column Schema is retained. Literal1, DateDiff, bare reference
presentations, masked/protected originals and unknown expressions remain STOP.
Ordinary Run/RunRegistered keep their previous compile path. CaptureInputFrame and
RunCapturedInputs opt into this narrower proof profile; older frame policy hashes
are invalidated. Captured proof inspection is privately bound to input identity.
Execution/compile evidence is recorded separately for the exact source commit;
this description does not claim full runtime or native acceptance.

Before each database read, the exact receipt/typed arguments are rechecked against
current metadata/RLS/object/field/output decisions. Host DB/registry/dialect and
timezone identity are separate guards because SQL Matches does not prove them.
Actual successful reads retain private receipts; JSON/inspection/result aliases
cannot create one. Input kind, runtime Query.Text staticness/control, current
session authorization, producer seal and final filter schema remain separate.
No handler/SDK/native activation is included, and ENG06/ENG12 HOLD remains.
