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
