# Коммерческое использование и изменённые сборки OneBase

OneBase распространяется по MIT; полный текст находится в файле `LICENSE`.
Эта лицензия разрешает
использование, изменение, распространение и продажу копий при соблюдении её
условий, включая сохранение уведомления об авторских правах и текста разрешения.
Сам текст MIT не требует публикации собственных доработок или бесплатного
распространения производного продукта.

Собственная конфигурация и собственные доработки могут иметь выбранные вами
условия распространения. Они не отменяют условия лицензий заимствованных
компонентов. Разрешение использовать OneBase не означает передачу исключительных
прав на исходную платформу, сторонние библиотеки или чужой вклад.

## Документы поставки

Для проверки конкретной поставки нужны версия и исходная ревизия, перечень
применяемых компонентов и их версии, полные тексты лицензий и необходимые
уведомления правообладателей. Состав серверной и GUI-сборки может различаться.
Зависимости Go, встроенные браузерные библиотеки, шрифты и нативные компоненты
проверяются отдельно. Перечень компонентов сам по себе не заменяет их лицензии.

## Название и ответственность за изменённую сборку

Изменённую сборку следует обозначать так, чтобы пользователь мог определить
её поставщика и отличить её версию от версии исходной платформы. У конфигурации
есть отдельные свойства `name`, `version`, `author`, `copyright`, `license` и
`support` в `config/app.yaml`; они не меняют авторство платформы.

Для форка задайте необязательные строковые параметры компоновщика
`internal/version.DistributionName`, `DistributionVersion` и
`DistributionSupplier`. Например, из корня исходников:

```sh
go build -ldflags="-X github.com/ivantit66/onebase/internal/version.Build=v0.13.0 -X github.com/ivantit66/onebase/internal/version.DistributionName=Example -X github.com/ivantit66/onebase/internal/version.DistributionVersion=2.1 -X github.com/ivantit66/onebase/internal/version.DistributionSupplier=ExampleLtd" -o onebase ./cmd/onebase
```

`Build` — версия исходной платформы; версия форка задаётся отдельно. Значения
отображаются в `onebase version`, пользовательском «О программе» и «О программе»
конфигуратора. Пустые значения скрывают дополнительные строки. Эти поля не
передают права и не заменяют MIT или уведомления сторонних правообладателей.

Поставщик изменённой сборки определяет собственные условия поддержки и обновления.
Указание названия форка само по себе не настраивает канал обновлений и не означает
поддержку этой сборки сопровождающими исходного OneBase.

## Дополнительное подтверждение автора

Подписанное подтверждение может дополнительно идентифицировать конкретный
релиз, ревизию и текст разрешения. Наличие PDF или электронной подписи не
является условием самой MIT. Русский перевод условий служит пояснением;
канонический текст MIT сохраняется на английском языке.

Для отдельного подтверждения заранее сообщите, кто его запрашивает, какой объект
и какую версию вы оформляете, какие сведения и вид подписи требуются. Подпись
одного автора не заменяет разрешения остальных правообладателей. Достаточность
документов для конкретной регистрационной процедуры проверяется отдельно.

Источники: [MIT](https://opensource.org/license/mit),
[ГК РФ, часть IV, ст. 1286.1](https://rospatent.gov.ru/ru/documents/grazhdanskiy-kodeks-rossiyskoy-federacii-chast-chetvertaya),
[обсуждение #1890](https://github.com/ivanarama/onebase/discussions/1890).

---

# Commercial use and modified distributions

OneBase is distributed under the MIT license in `LICENSE`. Subject to its terms,
including retention of the copyright and permission notices, MIT permits use,
modification, redistribution, sublicensing and sale. MIT itself does not require
publication of your changes or free distribution of a derived product.

Your configuration and your own changes may have your chosen distribution terms.
These terms do not replace the applicable licenses for upstream code and
third-party components or transfer their exclusive rights to you.

Identify the exact version and source revision, retain the component inventory,
full license texts and required notices for that distribution. Check Go
dependencies, embedded browser assets, fonts and native GUI components; an
inventory alone does not replace their license terms.

Identify a modified distribution's name, version and supplier separately from
the upstream platform and the application configuration. Configuration metadata
lives in `config/app.yaml`. Distribution metadata does not configure an update
channel or imply support by the upstream maintainers.

Set the optional linker strings `internal/version.DistributionName`,
`DistributionVersion` and `DistributionSupplier` using the full module path
shown in the build example above. Keep `Build` as the upstream platform version.
Metadata appears in `onebase version` and both About screens; empty values leave
the upstream display unchanged. Metadata neither transfers rights nor replaces
upstream copyright and license notices.

A separately signed confirmation can identify a specific release, revision and
license text. Neither a PDF nor a signature is a requirement of MIT itself.
Specify the requesting party, registration procedure, exact work/version and
required signature format before requesting such a document. A signer's
confirmation does not replace third-party permissions or guarantee registration.

## Генерация комплекта / Generating a bundle

После сборки CLI и, если поставляется, GUI тем же Go toolchain выполните:
Build the CLI and optional GUI with the same Go toolchain, then run:

```sh
go run ./tools/licensebundle -release -out license-bundle -version v0.13.0 ./onebase
# Windows amd64: pass both onebase.exe and onebase-gui.exe instead.
```

Каталог назначения должен отсутствовать; инструмент не перезаписывает старый
комплект. `-release` требует чистую отслеживаемую рабочую копию и бинарники,
собранные из её текущего коммита без незакоммиченных изменений. Без этого флага
возможна локальная проверка, которая не подтверждает соответствие ассетов
исходникам бинарника. В релизах флаг обязателен.

The destination must not exist. `-release` binds clean binaries to the current
clean tracked source commit. Omitting it supports local inspection without
certifying that source assets match those embedded in the binary. Releases
always use the strict flag.

В комплект входят / The bundle contains:

- `LICENSE`: MIT исходной платформы / upstream platform MIT;
- `COMMERCIAL_USE.md`: это пояснение / this guide;
- `components.json`: версии модулей из build info, замены, хеши бинарников,
  параметры цели и исходная ревизия / linked module versions, replacements,
  binary hashes, target settings and source revision;
- `THIRD_PARTY_NOTICES.md` и `licenses/`: перечень и полные исходные тексты
  лицензий и уведомлений / inventory and complete original licensing material.

Все эти файлы и каталог `licenses/` переносятся в корень каждого архива релиза.
Go-модули включаются по фактическим бинарникам, а встроенные браузерные библиотеки,
шрифты и нативные заголовки — по проверенному каталогу снимков. Если исходная
версия пакета не была сохранена, это прямо указано и приведены хеши содержимого.
Перечень не является гарантией юридической пригодности конкретной поставки.

All files and `licenses/` are copied to each release archive's root. Go modules
come from actual binaries; browser bundles, fonts and native headers use reviewed
snapshots. Where the original package version was not recorded, the inventory
explicitly says so and identifies the snapshot by hashes. The inventory does not
certify the legal suitability of a particular distribution.

## Обновление каталога / Catalog maintenance

Для Quill сохранены лицензии всех семи зависимостей, присутствующих в source map
поставляемого полного JS-бандла: BSD-3-Clause, MIT и Apache-2.0. Их архивы и
совпавшие исходники закреплены в `quill/UPSTREAM_DEPENDENCIES.json`. У пакетов
ApiDOM сохранены также вложенные `LICENSES/Apache-2.0.txt` и `LICENSES/MIT.txt`,
на которые ссылается исходный NOTICE. При проверке архива учитывайте все файлы
в юридических каталогах, даже если их имена не начинаются с LICENSE или NOTICE.

Quill retains the original licenses of all seven dependencies represented in
the shipped full bundle's source map: BSD-3-Clause, MIT and Apache-2.0. Archive
integrity and matching sources are recorded in `quill/UPSTREAM_DEPENDENCIES.json`.
ApiDOM packages also retain the nested `LICENSES/Apache-2.0.txt` and
`LICENSES/MIT.txt` referenced by their original NOTICE. Review every file in
legal directories, including filenames without a LICENSE or NOTICE prefix.

Для браузерных пакетов сохраняются `PROVENANCE.json` с integrity опубликованного
архива и границами проверки. Для RapiDoc дополнительно сохранены исходные
лицензии производственных зависимостей из upstream lockfile. Это консервативный
набор документов, а не утверждение, что каждый пакет целиком встроен в JS.
У `@apitools/openapi-parser` upstream публикует объявление MIT и имя автора в
`package.json`, но не отдельный LICENSE; у `short-unique-id` сохранено объявление
Apache и README. В комплекте эти ограничения отмечены, оригинальные объявления
сохранены и приложены полные стандартные тексты условий без придуманного
уведомления о правах или подписи. Они не доказывают цепочку прав upstream.

Browser packages retain archive integrity and verification scope in
`PROVENANCE.json`. RapiDoc also retains production dependencies' licensing
material from its upstream lockfile, conservatively; this is not a claim that
every package is fully linked into the bundle. Two upstream packages do not
publish a standalone license: `@apitools/openapi-parser` declares MIT in its
package metadata, and `short-unique-id` declares Apache in metadata and README.
Their original declarations and complete standard terms are retained, with the
limitation recorded and no invented copyright notice or signature. These
documents do not independently establish upstream ownership.

`third_party/licenses/catalog.json` связывает компоненты с исходными лицензиями,
уведомлениями и SHA-256 ассетов. При обновлении зависимости проверьте полные
новые условия, сохраните необходимые исходные LICENSE/NOTICE и обновите каталог
после проверки происхождения. Не пересчитывайте хеши автоматически ради зелёного
CI. Для npm-пакетов сверяйте опубликованный архив и его integrity с вендоренными
файлами; сохраняйте основной LICENSE и уведомления встроенных компонентов.
Хеши текста в каталоге нормализуют CRLF в LF; в комплекте тексты сохраняются
побайтно, их хеши и хеши бинарников считаются без нормализации.

Review complete new terms and provenance before updating the catalog. Preserve
required upstream LICENSE/NOTICE files, including bundled components' notices.
Do not regenerate pins merely to bypass CI. Catalog text hashes normalize CRLF
to LF; distributed licensing documents retain their original bytes and use exact
hashes, as do binaries. The Go toolchain's own legal files are reviewed too.
