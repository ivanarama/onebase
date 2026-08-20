# План 146 — Синтакс-помощник OneBase — Implementation Plan

> Номер сменён с 52 на 146 (issue #1035): под 52 жили разные планы, и ссылка «план 52» перестала быть однозначной. Номер 52 остался за планом «Потокобезопасность DSL-интерпретатора» (52-interpreter-concurrency). Ссылки «план 52» на эту тему читать как «план 146».

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Дать в конфигураторе полноценную справку по встроенному языку (как Синтакс-помощник 1С): автодополнение/hover/подсказку параметров в редакторе Monaco и отдельное окно-справочник — всё из единого Go-реестра дескрипторов, который заодно обогащает `ai-guide` сигнатурами.

**Architecture:** Новый пакет `internal/dsl/langref` — единственный источник истины (дескрипторы функций/методов/конструкций/слов запросов). Потребители: `cli/ai-guide` (импорт напрямую) и конфигуратор (JSON-эндпоинт `/bases/{id}/configurator/langref`, из него питаются Monaco-провайдеры и окно-справочник). `interpreter` пакет `langref` не импортирует — циклов нет; связь с реестром функций только в тесте полноты.

**Tech Stack:** Go (stdlib + chi v5), Monaco Editor (vendored JS), Go `html/template`. Без новых зависимостей; сборка без CGo не меняется.

**Спека:** `Plans/146-syntax-assistant.md` (согласованные решения по развилкам).

**Соглашения проекта:** ветка `feature/146-syntax-assistant` (уже создана от `main`). Коммиты — `тип(scope): описание` по-русски, с `Co-Authored-By`. После каждой Go-правки гонять `go build ./...` и `go test ./...`; после правок конфигураций — `onebase check`.

---

## Карта файлов

**Создаются:**
- `internal/dsl/langref/langref.go` — типы `Kind/Param/Descriptor`, `All()/ByName()/Objects()/Groups()`.
- `internal/dsl/langref/functions.go` — `var functionDescriptors []Descriptor` (глобальные функции).
- `internal/dsl/langref/keywords.go` — `var keywordDescriptors []Descriptor` (конструкции языка).
- `internal/dsl/langref/query.go` — `var queryDescriptors []Descriptor` (язык запросов).
- `internal/dsl/langref/methods.go` — `var methodDescriptors []Descriptor` (методы объектов).
- `internal/dsl/langref/completeness_test.go` — гибридный гейт + структурная валидация + отчёт охвата.
- `internal/dsl/langref/langref_test.go` — юнит-тесты `ByName/Objects/Groups`.
- `internal/cli/aiguide_test.go` — тест нового рендера guide.
- `internal/launcher/langref_handlers.go` — хендлер эндпоинта.
- `internal/launcher/langref_handlers_test.go` — тест эндпоинта.

**Модифицируются:**
- `internal/dsl/interpreter/builtins.go:402-409` — добавить `NewFileFunctions()` в `factoryMaps` (фикс бага чекера).
- `internal/cli/aiguide.go` — рендер «## Язык DSL» из `langref`; удалить `builtinGroup`/`builtinGroups()`; поправить импорты.
- `internal/launcher/server.go` (внутри группы `cfgAuthMiddleware`, строки 114-188) — роут `/configurator/langref`.
- `internal/launcher/configurator_tmpl.go` — Monaco-провайдеры (completion/hover/signature), загрузчик `loadLangref`, хук фокуса в `startEdit`, новый const-шаблон окна-справочника, подключение его в `cfg-main`, и добавление `cfgSyntaxRef` в `cfgTmpl.Parse(...)`.

---

## Task 1: Пакет `langref` — типы и API (скелет, компилируется)

**Files:**
- Create: `internal/dsl/langref/langref.go`
- Create: `internal/dsl/langref/functions.go`
- Create: `internal/dsl/langref/keywords.go`
- Create: `internal/dsl/langref/query.go`
- Create: `internal/dsl/langref/methods.go`
- Test: `internal/dsl/langref/langref_test.go`

- [ ] **Step 1: Написать падающий тест `ByName`/`Objects`/`Groups`**

Create `internal/dsl/langref/langref_test.go`:

```go
package langref

import "testing"

func TestByName_CaseInsensitiveAndAlias(t *testing.T) {
	// Добавляем временный дескриптор через срез functions.go в Task 3;
	// здесь проверяем механику на синтетическом наборе.
	saved := functionDescriptors
	defer func() { functionDescriptors = saved }()
	functionDescriptors = []Descriptor{{
		Name: "сообщить", Display: "Сообщить", Aliases: []string{"Message"},
		Kind: KindFunc, Signature: "Сообщить(Текст)", Doc: "Выводит текст.",
	}}
	if _, ok := ByName("СООБЩИТЬ"); !ok {
		t.Error("ByName должен находить по имени регистронезависимо")
	}
	if _, ok := ByName("message"); !ok {
		t.Error("ByName должен находить по англоязычному алиасу")
	}
	if _, ok := ByName("неттакого"); ok {
		t.Error("ByName не должен находить несуществующее имя")
	}
}

func TestObjectsAndGroups_UniqueSorted(t *testing.T) {
	savedF, savedM := functionDescriptors, methodDescriptors
	defer func() { functionDescriptors, methodDescriptors = savedF, savedM }()
	functionDescriptors = []Descriptor{
		{Name: "b", Display: "B", Kind: KindFunc, Group: "Строки", Signature: "B()", Doc: "d"},
		{Name: "a", Display: "A", Kind: KindFunc, Group: "Даты", Signature: "A()", Doc: "d"},
	}
	methodDescriptors = []Descriptor{
		{Name: "добавить", Display: "Добавить", Kind: KindMethod, Object: "Массив", Signature: "Массив.Добавить(З)", Doc: "d"},
	}
	g := Groups()
	if len(g) != 2 || g[0] != "Даты" || g[1] != "Строки" {
		t.Errorf("Groups должен быть уникален и отсортирован, got %v", g)
	}
	o := Objects()
	if len(o) != 1 || o[0] != "Массив" {
		t.Errorf("Objects: got %v", o)
	}
}
```

- [ ] **Step 2: Запустить тест — должен НЕ компилироваться (типов ещё нет)**

Run: `go test ./internal/dsl/langref/ -run TestByName`
Expected: FAIL — build error `undefined: functionDescriptors / KindFunc / Descriptor`.

- [ ] **Step 3: Создать `langref.go` с типами и API**

Create `internal/dsl/langref/langref.go`:

```go
// Package langref — единый машиночитаемый справочник встроенного языка OneBase
// (функции, методы объектов, конструкции, язык запросов). Источник истины для
// ai-guide (AGENTS.md) и справки в конфигураторе (автодополнение/hover/окно).
// ВАЖНО: пакет interpreter этот пакет НЕ импортирует — циклов нет; связь с
// реестром функций живёт только в completeness_test.go.
package langref

import (
	"sort"
	"strings"
)

// Kind классифицирует запись для группировки, фильтрации и иконок в UI.
type Kind string

const (
	KindFunc    Kind = "func"    // глобальная функция: Сообщить(...)
	KindMethod  Kind = "method"  // метод объекта: Запрос.Выполнить()
	KindKeyword Kind = "keyword" // конструкция языка: Если … Тогда … КонецЕсли
	KindQuery   Kind = "query"   // язык запросов: ВЫБРАТЬ, .Остатки()
)

// Param — параметр функции/метода.
type Param struct {
	Name     string `json:"name"`
	Type     string `json:"type"`
	Doc      string `json:"doc,omitempty"`
	Optional bool   `json:"optional,omitempty"`
}

// Descriptor — одна запись справочника.
type Descriptor struct {
	Name      string   `json:"name"`              // канон. рус. имя (для матчинга сравнивается lower-case)
	Display   string   `json:"display"`           // как показывать: "СтрЗаменить"
	Aliases   []string `json:"aliases,omitempty"` // англ. синонимы: "StrReplace"
	Kind      Kind     `json:"kind"`
	Object    string   `json:"object,omitempty"`    // для методов: "Запрос", "Массив"…
	Signature string   `json:"signature"`           // готовая строка сигнатуры
	Params    []Param  `json:"params,omitempty"`
	Returns   string   `json:"returns,omitempty"`   // тип возврата ("" если нет)
	Doc       string   `json:"doc"`                 // 1–3 предложения
	Example   string   `json:"example,omitempty"`
	Group     string   `json:"group,omitempty"` // для дерева функций: "Строки", "Даты"…
}

// All возвращает все дескрипторы из всех файлов пакета.
func All() []Descriptor {
	out := make([]Descriptor, 0,
		len(functionDescriptors)+len(keywordDescriptors)+len(queryDescriptors)+len(methodDescriptors))
	out = append(out, functionDescriptors...)
	out = append(out, keywordDescriptors...)
	out = append(out, queryDescriptors...)
	out = append(out, methodDescriptors...)
	return out
}

// ByName ищет дескриптор по имени или алиасу, регистронезависимо.
func ByName(name string) (Descriptor, bool) {
	ln := strings.ToLower(strings.TrimSpace(name))
	for _, d := range All() {
		if strings.ToLower(d.Name) == ln {
			return d, true
		}
		for _, a := range d.Aliases {
			if strings.ToLower(a) == ln {
				return d, true
			}
		}
	}
	return Descriptor{}, false
}

// Objects — уникальные имена объектов (для дерева методов), отсортированы.
func Objects() []string {
	seen := map[string]bool{}
	var out []string
	for _, d := range All() {
		if d.Kind == KindMethod && d.Object != "" && !seen[d.Object] {
			seen[d.Object] = true
			out = append(out, d.Object)
		}
	}
	sort.Strings(out)
	return out
}

// Groups — уникальные группы функций (для дерева функций), отсортированы.
func Groups() []string {
	seen := map[string]bool{}
	var out []string
	for _, d := range All() {
		if d.Kind == KindFunc && d.Group != "" && !seen[d.Group] {
			seen[d.Group] = true
			out = append(out, d.Group)
		}
	}
	sort.Strings(out)
	return out
}
```

- [ ] **Step 4: Создать четыре файла-источника с пустыми срезами**

Create `internal/dsl/langref/functions.go`:

```go
package langref

// functionDescriptors — глобальные функции. Наполняется в Task 3, далее
// поддерживается жёстким гейтом completeness_test.go.
var functionDescriptors = []Descriptor{}
```

Create `internal/dsl/langref/keywords.go`:

```go
package langref

// keywordDescriptors — конструкции языка (Task 4).
var keywordDescriptors = []Descriptor{}
```

Create `internal/dsl/langref/query.go`:

```go
package langref

// queryDescriptors — язык запросов (Task 5).
var queryDescriptors = []Descriptor{}
```

Create `internal/dsl/langref/methods.go`:

```go
package langref

// methodDescriptors — методы объектов (Task 6).
var methodDescriptors = []Descriptor{}
```

- [ ] **Step 5: Запустить тесты — должны пройти**

Run: `go test ./internal/dsl/langref/ -run 'TestByName|TestObjectsAndGroups' -v`
Expected: PASS (обе функции).

- [ ] **Step 6: Commit**

```bash
git add internal/dsl/langref/
git commit -m "feat(langref): каркас пакета справочника языка (типы, All/ByName/Objects/Groups)"
```

---

## Task 2: Гибридный гейт полноты + фикс `KnownBuiltinNames`

Сначала чиним известный баг: `KnownBuiltinNames()` не включает `NewFileFunctions`, хотя её функции вшиты в рантайм (`internal/dslvars/dslvars.go:67`). Без фикса `ДекодироватьФайл`/`DecodeFile` нельзя ни задокументировать (reverse-гейт уронит), ни поймать в `onebase check`.

**Files:**
- Modify: `internal/dsl/interpreter/builtins.go:402-409`
- Create: `internal/dsl/langref/completeness_test.go`

- [ ] **Step 1: Фикс — добавить `NewFileFunctions()` в `factoryMaps`**

In `internal/dsl/interpreter/builtins.go`, the slice currently is:

```go
	factoryMaps := []map[string]any{
		NewHTTPFunctions(),
		NewEmailFunctions(nil),
		NewTxFunctions(nil, nil),
		NewChartFunctions(),
		NewSpreadsheetFunctions(),
		NewLLMFunctions(nil),
	}
```

Replace with (add `NewFileFunctions()`):

```go
	factoryMaps := []map[string]any{
		NewHTTPFunctions(),
		NewEmailFunctions(nil),
		NewTxFunctions(nil, nil),
		NewChartFunctions(),
		NewSpreadsheetFunctions(),
		NewFileFunctions(),
		NewLLMFunctions(nil),
	}
```

- [ ] **Step 2: Проверить, что фикс компилируется и ничего не сломал**

Run: `go build ./... && go test ./internal/dsl/... ./internal/cli/...`
Expected: PASS (NewFileFunctions уже существует и возвращает `map[string]any`; `ДекодироватьФайл`/`DecodeFile` теперь попадут в известный набор).

- [ ] **Step 3: Написать гейт + структурную валидацию + отчёт охвата**

Create `internal/dsl/langref/completeness_test.go`:

```go
package langref

import (
	"sort"
	"strings"
	"testing"

	"github.com/ivantit66/onebase/internal/dsl/interpreter"
)

// notDocumented — имена из реестра, которые НЕ документируются как функции
// (специальные переменные контекста — не вызываются как функции).
var notDocumented = map[string]bool{
	"this":       true,
	"этотобъект": true,
}

func descriptorNameSet() map[string]bool {
	set := map[string]bool{}
	for _, d := range All() {
		set[strings.ToLower(d.Name)] = true
		for _, a := range d.Aliases {
			set[strings.ToLower(a)] = true
		}
	}
	return set
}

// TestCompleteness_AllBuiltinsDescribed — ЖЁСТКИЙ ГЕЙТ: каждое имя из реестра
// функций имеет дескриптор (кроме спец-переменных из notDocumented).
func TestCompleteness_AllBuiltinsDescribed(t *testing.T) {
	have := descriptorNameSet()
	var missing []string
	for name := range interpreter.KnownBuiltinNames() {
		ln := strings.ToLower(name)
		if notDocumented[ln] || have[ln] {
			continue
		}
		missing = append(missing, ln)
	}
	sort.Strings(missing)
	if len(missing) > 0 {
		t.Fatalf("нет описания (%d): %s", len(missing), strings.Join(missing, ", "))
	}
}

// TestCompleteness_NoOrphanFunctions — ЖЁСТКИЙ ГЕЙТ: дескриптор-функция не
// ссылается на несуществующее имя реестра (висячая ссылка = баг).
func TestCompleteness_NoOrphanFunctions(t *testing.T) {
	known := interpreter.KnownBuiltinNames()
	in := func(s string) bool { _, ok := known[strings.ToLower(s)]; return ok }
	var orphan []string
	for _, d := range All() {
		if d.Kind != KindFunc {
			continue
		}
		if !in(d.Name) {
			orphan = append(orphan, strings.ToLower(d.Name))
		}
		for _, a := range d.Aliases {
			if !in(a) {
				orphan = append(orphan, strings.ToLower(a))
			}
		}
	}
	sort.Strings(orphan)
	if len(orphan) > 0 {
		t.Fatalf("лишний дескриптор-функция (нет в реестре) (%d): %s", len(orphan), strings.Join(orphan, ", "))
	}
}

// TestDescriptors_Structural — дешёвая структурная валидация по ВСЕМ дескрипторам.
func TestDescriptors_Structural(t *testing.T) {
	seen := map[string]bool{}
	for _, d := range All() {
		if d.Name == "" || d.Display == "" || d.Doc == "" {
			t.Errorf("пустое обязательное поле: %+v", d)
		}
		if d.Kind == KindMethod && d.Object == "" {
			t.Errorf("метод без Object: %s", d.Name)
		}
		key := string(d.Kind) + "|" + strings.ToLower(d.Object) + "|" + strings.ToLower(d.Name)
		if seen[key] {
			t.Errorf("дубль дескриптора: %s", key)
		}
		seen[key] = true
	}
}

// TestCoverage_Report — МЯГКИЙ отчёт (не блокирует) по тому, что автоматически
// не сверить: методы объектов и язык запросов.
func TestCoverage_Report(t *testing.T) {
	var fn, method, kw, q int
	objs := map[string]bool{}
	for _, d := range All() {
		switch d.Kind {
		case KindFunc:
			fn++
		case KindMethod:
			method++
			objs[d.Object] = true
		case KindKeyword:
			kw++
		case KindQuery:
			q++
		}
	}
	t.Logf("охват langref: функций=%d, методов=%d по %d объектам, конструкций=%d, слов запросов=%d",
		fn, method, len(objs), kw, q)
}
```

- [ ] **Step 4: Запустить гейт — должен УПАСТЬ, перечислив все недостающие функции**

Run: `go test ./internal/dsl/langref/ -run TestCompleteness_AllBuiltinsDescribed -v`
Expected: FAIL — `нет описания (N): абс, абс…` (полный список имён из `KnownBuiltinNames()`). Это рабочий чек-лист для Task 3. `TestCompleteness_NoOrphanFunctions`, `TestDescriptors_Structural` — PASS (срезы пусты), `TestCoverage_Report` — PASS с `охват … функций=0…`.

- [ ] **Step 5: Commit**

```bash
git add internal/dsl/interpreter/builtins.go internal/dsl/langref/completeness_test.go
git commit -m "feat(langref): гибридный гейт полноты + фикс KnownBuiltinNames (NewFileFunctions)"
```

---

## Task 3: `functions.go` — дескрипторы глобальных функций

Цель: довести `TestCompleteness_AllBuiltinsDescribed` до зелёного. Тест — авторитетный чек-лист: гоняй его, он печатает недостающие имена, дописывай дескрипторы, повторяй.

**Полный инвентарь имён, которые надо покрыть** (RU+EN — один дескриптор с `Aliases`; источники: `internal/dsl/interpreter/builtins.go` core-карта, фабрики, и блок прямой инъекции в `KnownBuiltinNames`):

- **Строки:** `строка/str`, `врег/upper`, `нрег/lower`, `сокрлп/trimall`, `лев/left`, `прав/right`, `сред/mid`, `стрдлина/strlen`, `стрнайти/strfind`, `стрзаменить` *(проверь: есть ли в реестре — если нет, не добавляй)*.
- **Число:** `число/number`, `окр/round`, `абс/abs`, `цел/int`, `макс/max`, `мин/min`.
- **Даты:** `текущаядата/today`, `текущаядатавремя/now`, `год/year`, `месяц/month`, `день/day`.
- **JSON:** `прочитатьjson/readjson`, `записатьjson/writejson`.
- **Файлы:** `декодироватьфайл/decodefile`.
- **Сообщения/ошибки:** `сообщить/message`, `error`, `вызватьисключение`, `описаниеошибки/errordescription`, `информацияобошибке/errorinfo`.
- **Вычисления/контекст:** `вычислить/eval`, `блокировкаданных/datalock`, `текущийпользователь/currentuser`, `имяпользователя/username`, `значениереквизитаобъекта/objectattributevalue`.
- **HTTP:** `httpполучить/httpget`, `httpотправить/httppost`.
- **Email:** `отправитьписьмо/sendemail`.
- **Транзакции:** `начатьтранзакцию/begintransaction`, `зафиксироватьтранзакцию/committransaction`, `отменитьтранзакцию/rollbacktransaction`.
- **ИИ-помощник:** `запросии/aiquery`, `запросииджейсон/aiqueryjson`, `распознатьдокумент/recognizedocument`, `распознатьизображение/recognizeimage`.
- **Глобальный контекст (объекты-менеджеры):** `справочники/catalogs`, `документы/documents`, `регистрынакопления/accumulationregisters`, `предопределённыезначения/predefinedvalues`.

> ⚠️ `error`, `today`, `now` и т.п. в core-карте — отдельные ключи; некоторые без RU-пары (`error`, `message` отдельно). Сверяйся СТРОГО с выводом гейта: каждое перечисленное им имя (lower-case) должно найтись среди `Name`+`Aliases` (тоже сравнивается lower-case). `сообщить`/`message` встречаются и в core, и в инъекции — это один дескриптор.

**Files:**
- Modify: `internal/dsl/langref/functions.go`

- [ ] **Step 1: Записать дескрипторы-семена (точный формат) + начать наполнение**

Replace the body of `internal/dsl/langref/functions.go` with (семена показывают ТОЧНЫЙ формат; добавляй остальные по тому же образцу):

```go
package langref

// functionDescriptors — глобальные функции встроенного языка.
// Полнота гарантируется completeness_test.go (TestCompleteness_AllBuiltinsDescribed).
// Сигнатуры/описания авторские; имена обязаны совпадать с реестром interpreter.
var functionDescriptors = []Descriptor{
	{
		Name: "сообщить", Display: "Сообщить", Aliases: []string{"Message"},
		Kind: KindFunc, Group: "Сообщения",
		Signature: "Сообщить(Текст)",
		Params:    []Param{{Name: "Текст", Type: "строка", Doc: "выводимое сообщение"}},
		Doc:       "Выводит текст в окно сообщений пользователю.",
		Example:   `Сообщить("Готово");`,
	},
	{
		Name: "строка", Display: "Строка", Aliases: []string{"Str"},
		Kind: KindFunc, Group: "Строки",
		Signature: "Строка(Значение)", Returns: "строка",
		Params:    []Param{{Name: "Значение", Type: "произвольный", Doc: "что преобразовать"}},
		Doc:       "Преобразует значение любого типа в строку.",
		Example:   `Н = Строка(123);`,
	},
	{
		Name: "стрзаменить", Display: "СтрЗаменить", Aliases: []string{"StrReplace"},
		Kind: KindFunc, Group: "Строки",
		Signature: "СтрЗаменить(Строка, Что, Чем)", Returns: "строка",
		Params: []Param{
			{Name: "Строка", Type: "строка", Doc: "исходный текст"},
			{Name: "Что", Type: "строка", Doc: "что искать"},
			{Name: "Чем", Type: "строка", Doc: "на что заменить"},
		},
		Doc:     "Заменяет все вхождения подстроки «Что» на «Чем».",
		Example: `Н = СтрЗаменить("а-б-в", "-", "+");`,
	},
	{
		Name: "окр", Display: "Окр", Aliases: []string{"Round"},
		Kind: KindFunc, Group: "Число",
		Signature: "Окр(Число, Разрядность = 0)", Returns: "число",
		Params: []Param{
			{Name: "Число", Type: "число"},
			{Name: "Разрядность", Type: "число", Optional: true, Doc: "знаков после запятой"},
		},
		Doc: "Округляет число до заданной разрядности.",
	},
	{
		Name: "текущаядата", Display: "ТекущаяДата", Aliases: []string{"Today"},
		Kind: KindFunc, Group: "Даты",
		Signature: "ТекущаяДата()", Returns: "дата",
		Doc:       "Возвращает текущую дату (без времени).",
	},
	{
		Name: "справочники", Display: "Справочники", Aliases: []string{"Catalogs"},
		Kind: KindFunc, Group: "Глобальный контекст",
		Signature: "Справочники.<Имя>", Returns: "менеджер справочника",
		Doc:       "Глобальный доступ к менеджерам справочников: Справочники.Номенклатура.НайтиПоНаименованию(...).",
	},
	// … остальные функции из инвентаря Task 3 — по тому же формату …
}
```

- [ ] **Step 2: Итеративно дополнять, пока гейт не позеленеет**

Run (повторять после каждой порции): `go test ./internal/dsl/langref/ -run TestCompleteness -v`
Expected (в процессе): `нет описания (K): …` с уменьшающимся K. Цель — обе `TestCompleteness_*` зелёные.

- [ ] **Step 3: Прогнать структурную валидацию и отчёт**

Run: `go test ./internal/dsl/langref/ -v`
Expected: все тесты PASS; в логе `TestCoverage_Report` — `функций=N…` с непустым N.

- [ ] **Step 4: Commit**

```bash
git add internal/dsl/langref/functions.go
git commit -m "feat(langref): дескрипторы глобальных функций (гейт полноты зелёный)"
```

---

## Task 4: `keywords.go` — конструкции языка

Источник имён/конструкций: `internal/launcher/configurator_tmpl.go:1988-1998` (Monarch keywords) и README «Ключевые возможности языка». Эти записи в гейт полноты не входят (`Kind != KindFunc`), но проходят структурную валидацию.

Покрыть (RU + EN-алиас, где есть): `Процедура/КонецПроцедуры`, `Функция/КонецФункции`, `Если…Тогда…ИначеЕсли…Иначе…КонецЕсли`, `Для Каждого … Из … Цикл … КонецЦикла`, `Для … По … Цикл`, `Пока … Цикл … КонецПока`, `Попытка … Исключение … КонецПопытки`, `Возврат`, `Прервать`, `Продолжить`, `Новый`, `Перем`, `Истина/Ложь/Неопределено`, операторы `И/ИЛИ/НЕ`.

**Files:**
- Modify: `internal/dsl/langref/keywords.go`

- [ ] **Step 1: Записать дескрипторы конструкций (формат-семя)**

Replace body of `internal/dsl/langref/keywords.go`:

```go
package langref

// keywordDescriptors — конструкции встроенного языка. В гейт полноты не входят
// (Kind != KindFunc); проходят структурную валидацию (Name/Display/Doc, без дублей).
var keywordDescriptors = []Descriptor{
	{
		Name: "если", Display: "Если … Тогда … КонецЕсли", Aliases: []string{"If"},
		Kind: KindKeyword, Group: "Ветвление",
		Signature: "Если <условие> Тогда … ИначеЕсли <условие> Тогда … Иначе … КонецЕсли;",
		Doc:       "Условное ветвление. Ветки ИначеЕсли/Иначе необязательны.",
		Example:   "Если Сумма > 0 Тогда\n  Сообщить(\"плюс\");\nКонецЕсли;",
	},
	{
		Name: "для каждого", Display: "Для Каждого … Цикл", Aliases: []string{"For Each"},
		Kind: KindKeyword, Group: "Циклы",
		Signature: "Для Каждого <эл> Из <коллекция> Цикл … КонецЦикла;",
		Doc:       "Перебор элементов коллекции (Массив, выборка запроса, ТЧ).",
		Example:   "Для Каждого Стр Из Результат Цикл\n  Сообщить(Стр.Имя);\nКонецЦикла;",
	},
	{
		Name: "попытка", Display: "Попытка … Исключение … КонецПопытки", Aliases: []string{"Try"},
		Kind: KindKeyword, Group: "Обработка ошибок",
		Signature: "Попытка … Исключение … КонецПопытки;",
		Doc:       "Перехват исключений; в ветке Исключение доступно ОписаниеОшибки().",
	},
	// … процедуры/функции, Пока, Для…По, Возврат/Прервать/Продолжить, Новый, Перем,
	//   литералы Истина/Ложь/Неопределено, операторы И/ИЛИ/НЕ …
}
```

- [ ] **Step 2: Запустить тесты — структурная валидация зелёная**

Run: `go test ./internal/dsl/langref/ -v`
Expected: PASS; `TestCoverage_Report` теперь показывает `конструкций=N`.

- [ ] **Step 3: Commit**

```bash
git add internal/dsl/langref/keywords.go
git commit -m "feat(langref): дескрипторы конструкций языка"
```

---

## Task 5: `query.go` — язык запросов

Источник: `internal/launcher/configurator_tmpl.go:2001-2006` (Monarch query-builtins), `internal/query/query.go` (виртуальные таблицы). `Kind: KindQuery`.

Покрыть: `ВЫБРАТЬ`, `ИЗ`, `ГДЕ`, `УПОРЯДОЧИТЬ ПО`, `СГРУППИРОВАТЬ ПО`, соединения (`ЛЕВОЕ/ПРАВОЕ/ВНУТРЕННЕЕ/ПОЛНОЕ СОЕДИНЕНИЕ`), `КАК`, `ВОЗР/УБЫВ`; агрегаты `СУММА/КОЛИЧЕСТВО/МИНИМУМ/МАКСИМУМ/СРЕДНЕЕ`; виртуальные таблицы `РегистрНакопления.<Имя>.Остатки(&НаДату)`, `.Обороты(&Нач,&Кон)`, `РегистрСведений.<Имя>.СрезПоследних(&НаДату)`.

**Files:**
- Modify: `internal/dsl/langref/query.go`

- [ ] **Step 1: Записать дескрипторы языка запросов (формат-семя)**

Replace body of `internal/dsl/langref/query.go`:

```go
package langref

// queryDescriptors — встроенный язык запросов OneBase (1С-подобный, только ВЫБРАТЬ).
var queryDescriptors = []Descriptor{
	{
		Name: "выбрать", Display: "ВЫБРАТЬ", Aliases: []string{"SELECT"},
		Kind: KindQuery, Group: "Запрос",
		Signature: "ВЫБРАТЬ <поля> ИЗ <источник> [ГДЕ …] [СГРУППИРОВАТЬ ПО …] [УПОРЯДОЧИТЬ ПО …]",
		Doc:       "Начинает запрос выборки. Поддерживается только ВЫБРАТЬ (чтение).",
		Example:   "ВЫБРАТЬ Номенклатура, СУММА(Сумма) КАК Итог ИЗ … СГРУППИРОВАТЬ ПО Номенклатура",
	},
	{
		Name: "остатки", Display: "Остатки()", Aliases: []string{"Balances"},
		Kind: KindQuery, Group: "Виртуальные таблицы",
		Signature: "РегистрНакопления.<Имя>.Остатки(&НаДату)",
		Doc:       "Виртуальная таблица остатков регистра накопления на дату. Поля-ресурсы получают суффикс Остаток.",
		Example:   "ИЗ РегистрНакопления.ТоварыНаСкладах.Остатки(&НаДату)",
	},
	{
		Name: "сумма", Display: "СУММА", Aliases: []string{"SUM"},
		Kind: KindQuery, Group: "Агрегаты",
		Signature: "СУММА(<выражение>)", Returns: "число",
		Doc:       "Агрегатная функция: сумма по группе (используется с СГРУППИРОВАТЬ ПО).",
	},
	// … остальные слова и виртуальные таблицы по тому же формату …
}
```

- [ ] **Step 2: Запустить тесты**

Run: `go test ./internal/dsl/langref/ -v`
Expected: PASS; `TestCoverage_Report` показывает `слов запросов=N`.

- [ ] **Step 3: Commit**

```bash
git add internal/dsl/langref/query.go
git commit -m "feat(langref): дескрипторы языка запросов"
```

---

## Task 6: `methods.go` — методы объектов

Источник — инвентарь `CallMethod`-свитчей (см. ниже). `Kind: KindMethod`, обязательно `Object`. В гейт полноты не входят (мягкий отчёт охвата).

**Инвентарь объект → методы** (имя метода — `Display`, в `Name` его lower-case; `Object` — как в колонке):

| Object | Методы (Display) | Источник |
|---|---|---|
| Массив | Добавить, Количество, Получить, Удалить, Очистить, Найти, Установить, ВГраница, Вставить | collections.go:112 |
| Структура | Вставить, Удалить, Количество, Свойство | collections.go:240 |
| Соответствие | Вставить, Получить, Удалить, Количество, Очистить | collections.go:311 |
| ссылка | Удалить, ПолучитьОбъект, Пустая, УникальныйИдентификатор | collections.go:43 |
| Запрос | УстановитьПараметр, Выполнить | query_proxy.go:69 |
| Движения.X | Добавить, Очистить | runtime/movements.go:107 |
| Справочники.X | НайтиПоНаименованию, НайтиПоКоду, НайтиПоРеквизиту, Создать, Удалить | catalogs_proxy.go:134 |
| объект справочника | Записать, УстановитьЗначение, ЭтоНовый, Прочитать | catalogs_proxy.go:286 |
| Документы.X | Создать, НайтиПоНомеру, НайтиПоРеквизиту, Удалить, ОтменитьПроведение, ПометитьНаУдаление, СнятьПометку | ui/dsl_documents.go:80 |
| объект документа | Записать, Провести, Заполнить, УстановитьЗначение, ЭтоНовый, Прочитать | ui/dsl_documents.go:320 |
| табличная часть | Добавить, Очистить, Количество | ui/dsl_documents.go:566, ui/dsl_form_object.go:101 |
| РегистрыНакопления.X | Остатки, Движения/Выбрать, ВыбратьПоРегистратору | ui/dsl_registers.go:55 |
| БлокировкаДанных | Добавить, Заблокировать, Разблокировать | runtime/locks.go:99 |
| ТаблицаЗначений | Колонки, Добавить, Количество, Получить, Удалить, Очистить, Итог, ВыгрузитьКолонку, ЗагрузитьКолонку, Найти, НайтиСтроки, Сортировать, Свернуть | valuetable.go:61 |
| Диаграмма.Серии | Добавить, Количество | chart.go:186 |
| Диаграмма.Серия | УстановитьЗначение | chart.go:221 |
| Диаграмма.Точки | Добавить, Количество | chart.go:252 |
| ТабличныйДокумент | Вывести, Присоединить, Область, ПолучитьОбласть, Показать, Записать, Очистить, Объединить, Ячейка, … | spreadsheet_document.go:332 |
| ОбластьТабличногоДокумента | Параметры, Параметр, УстановитьПараметр, Ширина, Высота, Очистить, Объединить | spreadsheet_document.go:71 |
| ЧтениеТекста | Открыть, Прочитать, ПрочитатьСтроку, Закрыть | file_builtins.go:50 |
| ЗаписьТекста | Открыть, Записать, ЗаписатьСтроку, Закрыть | file_builtins.go:107 |
| HTTPСоединение | Получить, ОтправитьДля | http_builtins.go:20 |
| HTTPЗапрос | УстановитьЗаголовок, УстановитьТелоИзСтроки | http_builtins.go:82 |
| HTTPОтвет | ПолучитьТелоКакСтроку, ПолучитьЗаголовок | http_builtins.go:117 |
| ПисьмоEmail | Отправить | email_builtins.go:54 |

> Объекты без методов (только свойства): `Диаграмма`, `Файл` (Существует — метод), `ОбластьТабличногоДокумента.Параметры` — их можно не вносить в methods.go (документируются как объекты в окне через свойства — вне scope v1).

**Files:**
- Modify: `internal/dsl/langref/methods.go`

- [ ] **Step 1: Записать дескрипторы методов (формат-семя)**

Replace body of `internal/dsl/langref/methods.go`:

```go
package langref

// methodDescriptors — методы объектов DSL. Реестра методов в платформе нет
// (они в switch внутри CallMethod), поэтому полнота — на ревью (мягкий отчёт).
var methodDescriptors = []Descriptor{
	{
		Name: "добавить", Display: "Добавить", Aliases: []string{"Add"},
		Kind: KindMethod, Object: "Массив",
		Signature: "Массив.Добавить(Значение)",
		Params:    []Param{{Name: "Значение", Type: "произвольный"}},
		Doc:       "Добавляет элемент в конец массива.",
		Example:   "М.Добавить(123);",
	},
	{
		Name: "выполнить", Display: "Выполнить", Aliases: []string{"Execute"},
		Kind: KindMethod, Object: "Запрос",
		Signature: "Запрос.Выполнить()", Returns: "выборка",
		Doc:       "Выполняет запрос и возвращает результат для обхода Для Каждого.",
		Example:   "Результат = Запрос.Выполнить();",
	},
	{
		Name: "установитьпараметр", Display: "УстановитьПараметр", Aliases: []string{"SetParameter"},
		Kind: KindMethod, Object: "Запрос",
		Signature: "Запрос.УстановитьПараметр(Имя, Значение)",
		Params: []Param{
			{Name: "Имя", Type: "строка", Doc: "имя параметра без &"},
			{Name: "Значение", Type: "произвольный"},
		},
		Doc: "Передаёт значение параметра &Имя в текст запроса.",
	},
	{
		Name: "провести", Display: "Провести", Aliases: []string{"Post"},
		Kind: KindMethod, Object: "объект документа",
		Signature: "Док.Провести()", Returns: "ссылка",
		Doc:       "Записывает и проводит документ (запускает ОбработкаПроведения).",
	},
	// … остальные методы из таблицы инвентаря — по тому же формату …
}
```

- [ ] **Step 2: Запустить тесты — структурная валидация и отчёт**

Run: `go test ./internal/dsl/langref/ -v`
Expected: PASS; `TestCoverage_Report` показывает `методов=N по M объектам`. Все четыре файла-источника наполнены.

- [ ] **Step 3: Commit**

```bash
git add internal/dsl/langref/methods.go
git commit -m "feat(langref): дескрипторы методов объектов"
```

---

## Task 7: `ai-guide` рендерит из `langref` (с сигнатурами), фабрики удалены

**Files:**
- Modify: `internal/cli/aiguide.go`
- Create: `internal/cli/aiguide_test.go`

- [ ] **Step 1: Написать падающий тест guide**

Create `internal/cli/aiguide_test.go`:

```go
package cli

import "strings"
import "testing"

func TestGenerateAIGuide_HasSignaturesAndSections(t *testing.T) {
	g := generateAIGuide()
	for _, want := range []string{
		"## Язык DSL",
		"### Методы объектов",
		"### Язык запросов",
		"СтрЗаменить(",   // сигнатура функции
		"Запрос.Выполнить", // метод объекта
	} {
		if !strings.Contains(g, want) {
			t.Errorf("в guide нет ожидаемого фрагмента: %q", want)
		}
	}
	// старый дисклеймер про сигнатуры должен исчезнуть
	if strings.Contains(g, "Сигнатуры смотрите в примерах") {
		t.Error("guide всё ещё содержит устаревший дисклеймер о сигнатурах")
	}
}
```

- [ ] **Step 2: Запустить — упадёт (старый рендер)**

Run: `go test ./internal/cli/ -run TestGenerateAIGuide -v`
Expected: FAIL — нет `### Методы объектов`/сигнатур; присутствует старый дисклеймер.

- [ ] **Step 3: Добавить рендер-хелпер из `langref`**

In `internal/cli/aiguide.go`, add this function (e.g. near `builtinGroups`, which you delete in Step 5):

```go
// renderLangDSL печатает функции (по группам), методы объектов и язык запросов
// из реестра langref — единый источник, с сигнатурами.
func renderLangDSL(b *strings.Builder) {
	byGroup := map[string][]langref.Descriptor{}
	byObject := map[string][]langref.Descriptor{}
	var queries []langref.Descriptor
	for _, d := range langref.All() {
		switch d.Kind {
		case langref.KindFunc:
			byGroup[d.Group] = append(byGroup[d.Group], d)
		case langref.KindMethod:
			byObject[d.Object] = append(byObject[d.Object], d)
		case langref.KindQuery:
			queries = append(queries, d)
		}
	}
	line := func(d langref.Descriptor) {
		fmt.Fprintf(b, "- `%s` — %s\n", d.Signature, d.Doc)
	}
	for _, g := range langref.Groups() {
		fmt.Fprintf(b, "\n**%s:**\n\n", g)
		ds := byGroup[g]
		sort.Slice(ds, func(i, j int) bool { return ds[i].Name < ds[j].Name })
		for _, d := range ds {
			line(d)
		}
	}
	b.WriteString("\n### Методы объектов\n")
	for _, o := range langref.Objects() {
		fmt.Fprintf(b, "\n**%s:**\n\n", o)
		ds := byObject[o]
		sort.Slice(ds, func(i, j int) bool { return ds[i].Name < ds[j].Name })
		for _, d := range ds {
			line(d)
		}
	}
	b.WriteString("\n### Язык запросов\n\n")
	sort.Slice(queries, func(i, j int) bool { return queries[i].Name < queries[j].Name })
	for _, d := range queries {
		line(d)
	}
}
```

- [ ] **Step 4: Заменить старый цикл и дисклеймер в `generateAIGuide()`**

In `internal/cli/aiguide.go`, the current block (lines ~116-123) is:

```go
	for _, g := range builtinGroups() {
		if len(g.names) == 0 {
			continue
		}
		fmt.Fprintf(&b, "**%s:** %s\n\n", g.title, strings.Join(g.names, ", "))
	}
	b.WriteString(`> Справочник перечисляет имена функций (из реестра платформы — не устаревает).
> Сигнатуры смотрите в примерах существующих модулей конфигурации.
```

Replace it with (вызов рендера + удалить обе строки дисклеймера, оставив следующий за ними `## ИИ-помощник (LLM)`):

```go
	renderLangDSL(&b)
	b.WriteString(`
```

> Сохрани остаток второго `WriteString`-литерала начиная с пустой строки и `## ИИ-помощник (LLM)`. Итог: после `## Язык DSL` идёт `renderLangDSL`, затем сразу секция `## ИИ-помощник (LLM)` без дисклеймера.

- [ ] **Step 5: Удалить `builtinGroup` и `builtinGroups()`, поправить импорты**

Delete the type and function (lines ~275-317):

```go
type builtinGroup struct {
	title string
	names []string
}
// … и весь func builtinGroups() … (до закрывающей }) на строке ~317)
```

In the import block, remove the now-unused interpreter import and add langref:

```go
import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/ivantit66/onebase/internal/dsl/langref"
	"github.com/spf13/cobra"
)
```

> `sort` остаётся нужен (используется в `renderLangDSL`). `interpreter` больше не используется в этом файле.

- [ ] **Step 6: Запустить тесты guide и сборку**

Run: `go build ./... && go test ./internal/cli/ -run TestGenerateAIGuide -v`
Expected: PASS. (Также `internal/cli/init.go` продолжает компилироваться — `generateAIGuide()` и `claudePointer` не тронуты.)

- [ ] **Step 7: Глазами проверить вывод**

Run: `go run ./cmd/onebase ai-guide | sed -n '/## Язык DSL/,/## Безопасность/p' | head -60`
Expected: секция «## Язык DSL» с функциями по группам (сигнатуры + описания), затем «### Методы объектов» и «### Язык запросов».

- [ ] **Step 8: Commit**

```bash
git add internal/cli/aiguide.go internal/cli/aiguide_test.go
git commit -m "feat(ai-guide): рендер языка DSL из langref с сигнатурами; удаление фабричной группировки"
```

---

## Task 8: JSON-эндпоинт `/configurator/langref`

**Files:**
- Create: `internal/launcher/langref_handlers.go`
- Create: `internal/launcher/langref_handlers_test.go`
- Modify: `internal/launcher/server.go` (внутри auth-группы, строки 114-188)

- [ ] **Step 1: Написать падающий тест хендлера**

Create `internal/launcher/langref_handlers_test.go`:

```go
package launcher

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-chi/chi/v5"
)

func TestConfiguratorLangref_ReturnsJSON(t *testing.T) {
	s := newTestStore(t)
	b := &Base{Name: "Тест", DB: "postgres://localhost/x", Port: 8080}
	if err := s.Add(b); err != nil {
		t.Fatalf("Add: %v", err)
	}
	h := &handler{store: s}

	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("id", b.ID)
	req := httptest.NewRequest(http.MethodGet, "/bases/"+b.ID+"/configurator/langref", nil)
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))
	rec := httptest.NewRecorder()

	h.configuratorLangref(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("код %d, тело: %s", rec.Code, rec.Body.String())
	}
	if ct := rec.Header().Get("Content-Type"); ct != "application/json" {
		t.Errorf("Content-Type = %q", ct)
	}
	var items []map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &items); err != nil {
		t.Fatalf("ответ не JSON-массив: %v", err)
	}
	if len(items) == 0 {
		t.Fatal("ожидался непустой справочник langref")
	}
}
```

- [ ] **Step 2: Запустить — упадёт (хендлера нет)**

Run: `go test ./internal/launcher/ -run TestConfiguratorLangref -v`
Expected: FAIL — build error `h.configuratorLangref undefined`.

- [ ] **Step 3: Реализовать хендлер**

Create `internal/launcher/langref_handlers.go`:

```go
package launcher

import (
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/ivantit66/onebase/internal/dsl/langref"
)

// configuratorLangref отдаёт справочник встроенного языка (статический, из
// реестра langref) для автодополнения/hover/окна-справочника в конфигураторе.
// База по id не нужна для данных — но cfgAuthMiddleware уже валидирует id;
// guard ниже даёт чистый 404 при прямом вызове без middleware.
func (h *handler) configuratorLangref(w http.ResponseWriter, r *http.Request) {
	if _, err := h.store.Get(chi.URLParam(r, "id")); err != nil {
		http.NotFound(w, r)
		return
	}
	writeJSON(w, http.StatusOK, langref.All())
}
```

- [ ] **Step 4: Зарегистрировать роут в auth-группе**

In `internal/launcher/server.go`, внутри блока `r.Group(func(r chi.Router) { r.Use(s.h.cfgAuthMiddleware) … })` (рядом с существующим `r.Get("/bases/{id}/configurator/ai-enabled", s.h.cfgAIEnabled)`), добавить строку:

```go
		r.Get("/bases/{id}/configurator/langref", s.h.configuratorLangref)
```

- [ ] **Step 5: Запустить тест и сборку**

Run: `go build ./... && go test ./internal/launcher/ -run TestConfiguratorLangref -v`
Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add internal/launcher/langref_handlers.go internal/launcher/langref_handlers_test.go internal/launcher/server.go
git commit -m "feat(launcher): JSON-эндпоинт /configurator/langref из реестра langref"
```

---

## Task 9: Monaco-провайдеры (автодополнение/hover/подсказка параметров) + подсветка из langref

Весь JS — внутри const `cfgFoot` (`{{define \"cfg-foot\"}}`) в `internal/launcher/configurator_tmpl.go`. Провайдеры читают `window._langref` лениво (на вызове), поэтому регистрируются синхронно; данные дозагружаются `loadLangref()`.

**Files:**
- Modify: `internal/launcher/configurator_tmpl.go` (Monaco-блок ~1983-2078; `startEdit` ~898-969)
- Test: `internal/launcher/langref_render_test.go` (смоук-рендер)

- [ ] **Step 1: Смоук-тест рендера (падающий)**

Create `internal/launcher/langref_render_test.go`:

```go
package launcher

import (
	"bytes"
	"strings"
	"testing"
)

func renderCfgFoot(t *testing.T) string {
	t.Helper()
	data := &configuratorData{Base: &Base{ID: "test-base", Name: "Тест", ConfigSource: "file"}, Lang: "ru"}
	var buf bytes.Buffer
	if err := cfgTmpl.ExecuteTemplate(&buf, "cfg-foot", data); err != nil {
		t.Fatalf("ExecuteTemplate cfg-foot: %v", err)
	}
	return buf.String()
}

func TestConfigurator_LangrefWired(t *testing.T) {
	html := renderCfgFoot(t)
	for _, sub := range []string{
		"registerHoverProvider",
		"registerSignatureHelpProvider",
		"/configurator/langref",
		"function loadLangref",
	} {
		if !strings.Contains(html, sub) {
			t.Errorf("в cfg-foot нет ожидаемого фрагмента: %q", sub)
		}
	}
}
```

Run: `go test ./internal/launcher/ -run TestConfigurator_LangrefWired -v` → FAIL (фрагментов ещё нет).

- [ ] **Step 2: Добавить загрузчик `loadLangref()` в начало Monaco-init**

In `configurator_tmpl.go`, сразу после `require(['vs/editor/editor.main'], function() {` (строка ~1984) добавить:

```javascript
  // Единый загрузчик справочника языка (кешируется; используется провайдерами и окном-справочником)
  window._langref = window._langref || null;
  window._langrefPromise = null;
  function loadLangref() {
    if (window._langref) return Promise.resolve(window._langref);
    if (window._langrefPromise) return window._langrefPromise;
    window._langrefPromise = fetch('/bases/' + _dbgBase + '/configurator/langref')
      .then(function(r){ return r.json(); })
      .then(function(d){ window._langref = d || []; return window._langref; })
      .catch(function(){ window._langref = []; return window._langref; });
    return window._langrefPromise;
  }
```

> `_dbgBase` объявляется на странице (`var _dbgBase = '{{.Base.ID}}'`, строка ~2617) — он в области видимости всех скриптов страницы.

- [ ] **Step 3: Заменить статическое автодополнение на data-driven**

Replace the existing block (`configurator_tmpl.go:2029-2045`):

```javascript
  // Auto-completion
  monaco.languages.registerCompletionItemProvider('onebase-dsl', {
    provideCompletionItems: function(model, position) {
      var word = model.getWordUntilPosition(position);
      var range = { startLineNumber: position.lineNumber, endLineNumber: position.lineNumber, startColumn: word.startColumn, endColumn: word.endColumn };
      var kwSuggestions = [
        'Процедура','КонецПроцедуры','Функция','КонецФункции',
        'Если','Тогда','ИначеЕсли','Иначе','КонецЕсли',
        'Для','Каждого','Из','Цикл','КонецЦикла','Пока','КонецПока',
        'Возврат','Новый','Истина','Ложь','Неопределено',
        'Procedure','EndProcedure','Function','EndFunction'
      ].map(function(k) {
        return { label: k, kind: monaco.languages.CompletionItemKind.Keyword, insertText: k, range: range };
      });
      return { suggestions: kwSuggestions };
    }
  });
```

with:

```javascript
  // Иконка по виду дескриптора
  function _lrKind(kind){
    var K = monaco.languages.CompletionItemKind;
    if (kind === 'method') return K.Method;
    if (kind === 'keyword') return K.Keyword;
    if (kind === 'query') return K.Struct;
    return K.Function;
  }
  // Сниппет вставки: Имя(${1:П1}, ${2:П2})
  function _lrSnippet(d){
    if (!d.params || !d.params.length) return d.display;
    var parts = d.params.map(function(p,i){ return '${'+(i+1)+':'+p.name+'}'; });
    return d.display + '(' + parts.join(', ') + ')';
  }
  // Auto-completion (данные из langref, лениво)
  monaco.languages.registerCompletionItemProvider('onebase-dsl', {
    triggerCharacters: ['.'],
    provideCompletionItems: function(model, position) {
      loadLangref();
      var data = window._langref || [];
      var word = model.getWordUntilPosition(position);
      var range = { startLineNumber: position.lineNumber, endLineNumber: position.lineNumber, startColumn: word.startColumn, endColumn: word.endColumn };
      // best-effort контекст по точке: токен перед '.' — известный объект?
      var line = model.getLineContent(position.lineNumber).substring(0, word.startColumn - 1);
      var dot = /([A-Za-zА-Яа-яЁё0-9_]+)\.\s*$/.exec(line);
      var obj = dot ? dot[1].toLowerCase() : null;
      var objExists = obj && data.some(function(d){ return d.kind==='method' && d.object && d.object.toLowerCase()===obj; });
      var suggestions = [];
      data.forEach(function(d){
        if (objExists && !(d.kind==='method' && d.object && d.object.toLowerCase()===obj)) return;
        suggestions.push({
          label: d.display,
          kind: _lrKind(d.kind),
          detail: d.signature || '',
          documentation: { value: (d.doc||'') + (d.example ? '\n\n```bsl\n'+d.example+'\n```' : '') },
          insertText: _lrSnippet(d),
          insertTextRules: (d.params && d.params.length) ? monaco.languages.CompletionItemInsertTextRule.InsertAsSnippet : undefined,
          range: range
        });
      });
      return { suggestions: suggestions };
    }
  });
  // Hover — описание под курсором
  monaco.languages.registerHoverProvider('onebase-dsl', {
    provideHover: function(model, position) {
      loadLangref();
      var word = model.getWordAtPosition(position);
      if (!word) return null;
      var w = word.word.toLowerCase();
      var data = window._langref || [];
      var d = null;
      for (var i=0;i<data.length;i++){
        var x=data[i];
        if (x.name && x.name.toLowerCase()===w){ d=x; break; }
        if (x.aliases && x.aliases.some(function(a){return a.toLowerCase()===w;})){ d=x; break; }
      }
      if (!d) return null;
      var md = '**' + (d.signature||d.display) + '**\n\n' + (d.doc||'');
      if (d.returns) md += '\n\n_Возвращает:_ ' + d.returns;
      if (d.example) md += '\n\n```bsl\n' + d.example + '\n```';
      return { contents: [{ value: md }] };
    }
  });
  // Подсказка параметров
  monaco.languages.registerSignatureHelpProvider('onebase-dsl', {
    signatureHelpTriggerCharacters: ['(', ','],
    provideSignatureHelp: function(model, position) {
      loadLangref();
      var textBefore = model.getValueInRange({startLineNumber:1,startColumn:1,endLineNumber:position.lineNumber,endColumn:position.column});
      var m = /([A-Za-zА-Яа-яЁё0-9_]+)\s*\(([^()]*)$/.exec(textBefore);
      if (!m) return null;
      var name = m[1].toLowerCase();
      var data = window._langref || [];
      var d = null;
      for (var i=0;i<data.length;i++){
        var x=data[i];
        if ((x.name && x.name.toLowerCase()===name) || (x.aliases && x.aliases.some(function(a){return a.toLowerCase()===name;}))){ d=x; break; }
      }
      if (!d || !d.params || !d.params.length) return null;
      var active = (m[2].match(/,/g) || []).length;
      return { value: { signatures: [{
        label: d.signature || d.display,
        documentation: d.doc || '',
        parameters: d.params.map(function(p){ return { label: p.name, documentation: (p.type||'') + (p.doc ? ' — '+p.doc : '') }; })
      }], activeSignature: 0, activeParameter: Math.min(active, d.params.length-1) }, dispose: function(){} };
    }
  });
```

- [ ] **Step 4: Подсветка — обновить Monarch-список из langref после загрузки**

В конце Monaco-init (после строки `window._monacoReady = true;`, ~2068) добавить:

```javascript
  // Динамическая подсветка: дополнить список встроенных именами из langref
  loadLangref().then(function(data){
    var names = data.filter(function(d){ return d.kind==='func' || d.kind==='method' || d.kind==='query'; })
                    .map(function(d){ return d.display; });
    monaco.languages.setMonarchTokensProvider('onebase-dsl', _onebaseMonarch(names));
  });
```

И вынести текущий объект из `setMonarchTokensProvider('onebase-dsl', {...})` (строки ~1987-2028) в фабрику, заменив регистрацию на:

```javascript
  function _onebaseMonarch(extraBuiltins){
    var builtins = ['Error','Ошибка','Сообщить','Формат','ФорматСтроки','СтрЗаменить',
      'Запрос','Результат','Выполнить','УстановитьПараметр','Текст',
      'ВЫБРАТЬ','ИЗ','ГДЕ','УПОРЯДОЧИТЬ','ПО','СГРУППИРОВАТЬ',
      'ЛЕВОЕ','ПРАВОЕ','ВНУТРЕННЕЕ','ПОЛНОЕ','СОЕДИНЕНИЕ',
      'КАК','ВОЗР','УБЫВ','СУММА','КОЛИЧЕСТВО','МИНИМУМ','МАКСИМУМ','СРЕДНЕЕ'].concat(extraBuiltins||[]);
    return {
      keywords: [
        'Процедура','КонецПроцедуры','Функция','КонецФункции',
        'Если','Тогда','ИначеЕсли','Иначе','КонецЕсли',
        'Для','Каждого','Из','Цикл','КонецЦикла','Пока','КонецПока',
        'Возврат','Прервать','Продолжить','Истина','Ложь','Неопределено','Новый',
        'И','ИЛИ','НЕ','Не','Перем',
        'Procedure','EndProcedure','Function','EndFunction',
        'If','Then','ElseIf','Else','EndIf',
        'For','Each','In','Do','EndDo','While','EndWhile',
        'Return','Break','Continue','True','False','Undefined','New',
        'And','Or','Not','Var'
      ],
      builtins: builtins,
      special: ['this','ЭтотОбъект','Движения','Параметры'],
      tokenizer: {
        root: [
          [/#.*$/, 'comment'],
          ["\/\/.*$", 'comment'],
          [/"/, 'string', '@string'],
          [/\d+(\.\d+)?/, 'number'],
          [/[a-zA-Z_А-яЁё][a-zA-Z0-9_А-яЁё]*/, {
            cases: { '@keywords': 'keyword', '@builtins': 'type', '@special': 'variable.predefined', '@default': 'identifier' }
          }]
        ],
        string: [ [/[^"]+/, 'string'], [/"/, 'string', '@pop'] ]
      }
    };
  }
  monaco.languages.setMonarchTokensProvider('onebase-dsl', _onebaseMonarch());
```

> Заменяется именно вызов `setMonarchTokensProvider('onebase-dsl', { … })` на пару `function _onebaseMonarch(...)` + `setMonarchTokensProvider('onebase-dsl', _onebaseMonarch());`. Содержимое объекта (keywords/builtins/special/tokenizer) — то же, что было.

- [ ] **Step 5: Запомнить последний сфокусированный редактор (для «Вставить в редактор» в Task 10)**

В `startEdit`, сразу после `monacoEditors[name] = editor;` (~строка 936) добавить:

```javascript
    window._lastFocusedEditorId = window._lastFocusedEditorId || name;
    editor.onDidFocusEditorText(function(){ window._lastFocusedEditorId = name; });
```

- [ ] **Step 6: Запустить смоук-тест и сборку**

Run: `go build ./... && go test ./internal/launcher/ -run TestConfigurator_LangrefWired -v`
Expected: PASS.

- [ ] **Step 7: Ручная проверка IntelliSense**

Собрать и запустить лаунчер; открыть конфигуратор любой базы, редактор модуля. Проверить: автодополнение показывает функции с сигнатурами; hover над `СтрЗаменить` показывает описание; набор `СтрЗаменить(` показывает подсказку параметров; после `Запрос.` показываются методы Запроса.

- [ ] **Step 8: Commit**

```bash
git add internal/launcher/configurator_tmpl.go internal/launcher/langref_render_test.go
git commit -m "feat(configurator): Monaco-провайдеры справки из langref (автодополнение/hover/подсказка параметров)"
```

---

## Task 10: Окно-справочник (закреплённая сворачиваемая панель, F1/Esc, вставка)

Отдельный const `cfgSyntaxRef` с `{{define \"syntax-ref\"}}` (HTML панели + её `<script>`), подключаемый в `cfg-main`, и добавленный в `cfgTmpl.Parse(...)`. Панель строит дерево из `window._langref` (через `loadLangref()` из Task 9).

**Files:**
- Modify: `internal/launcher/configurator_tmpl.go` (новый const `cfgSyntaxRef`; `.Parse(...)` строка ~104; вставка `{{template \"syntax-ref\" .}}` в `cfg-main`)
- Test: `internal/launcher/langref_render_test.go` (дополнить)

- [ ] **Step 1: Дополнить смоук-тест ожиданиями панели (падающий)**

Append to `internal/launcher/langref_render_test.go`:

```go
func TestConfigurator_SyntaxPanelWired(t *testing.T) {
	data := &configuratorData{Base: &Base{ID: "test-base", Name: "Тест", ConfigSource: "file"}, Lang: "ru", Tab: "tree"}
	var buf bytes.Buffer
	if err := cfgTmpl.ExecuteTemplate(&buf, "cfg-main", data); err != nil {
		t.Fatalf("ExecuteTemplate cfg-main: %v", err)
	}
	html := buf.String()
	for _, sub := range []string{
		`id="syntax-ref-panel"`,
		`id="syntax-ref-toggle"`,
		"function toggleSyntaxRef",
		"function insertLangrefSignature",
	} {
		if !strings.Contains(html, sub) {
			t.Errorf("в cfg-main нет фрагмента окна-справочника: %q", sub)
		}
	}
}
```

Run: `go test ./internal/launcher/ -run TestConfigurator_SyntaxPanelWired -v` → FAIL.

- [ ] **Step 2: Объявить const `cfgSyntaxRef` с разметкой и скриптом панели**

Add a new package-level const in `internal/launcher/configurator_tmpl.go` (рядом с другими `cfg*` const):

```go
const cfgSyntaxRef = `{{define "syntax-ref"}}
<button id="syntax-ref-toggle" type="button" onclick="toggleSyntaxRef()" title="Синтакс-помощник (F1)"
  style="position:fixed;right:0;top:120px;z-index:40;background:#1a4a80;color:#fff;border:none;border-radius:4px 0 0 4px;padding:8px 6px;cursor:pointer;writing-mode:vertical-rl">{{t .Lang "Синтакс-помощник"}}</button>
<div id="syntax-ref-panel" style="display:none;position:fixed;right:0;top:0;bottom:0;width:520px;z-index:41;background:#fff;border-left:1px solid #cbd5e1;box-shadow:-4px 0 16px rgba(0,0,0,.12);font-size:13px;flex-direction:column">
  <div style="display:flex;align-items:center;gap:8px;padding:8px 10px;border-bottom:1px solid #e2e8f0;background:#f8fafc">
    <strong style="flex:0 0 auto">{{t .Lang "Синтакс-помощник"}}</strong>
    <input id="syntax-ref-search" oninput="renderSyntaxRefTree()" placeholder="{{t .Lang "поиск"}}…" style="flex:1;padding:4px 6px;border:1px solid #cbd5e1;border-radius:4px">
    <button type="button" onclick="toggleSyntaxRef(false)" style="border:none;background:none;font-size:16px;cursor:pointer">✕</button>
  </div>
  <div style="flex:1;display:flex;min-height:0">
    <div id="syntax-ref-tree" style="width:230px;overflow:auto;border-right:1px solid #e2e8f0;padding:6px"></div>
    <div id="syntax-ref-detail" style="flex:1;overflow:auto;padding:10px"></div>
  </div>
</div>
<script>
function toggleSyntaxRef(show){
  var p=document.getElementById('syntax-ref-panel');
  var open=(typeof show==='boolean')?show:(p.style.display==='none');
  p.style.display=open?'flex':'none';
  if(open){ loadLangref().then(renderSyntaxRefTree); }
}
function _lrFiltered(){
  var q=(document.getElementById('syntax-ref-search').value||'').toLowerCase();
  var data=window._langref||[];
  if(!q) return data;
  return data.filter(function(d){
    return (d.display&&d.display.toLowerCase().indexOf(q)>=0)
      || (d.name&&d.name.toLowerCase().indexOf(q)>=0)
      || (d.aliases&&d.aliases.some(function(a){return a.toLowerCase().indexOf(q)>=0;}))
      || (d.doc&&d.doc.toLowerCase().indexOf(q)>=0);
  });
}
function renderSyntaxRefTree(){
  var data=_lrFiltered(), tree=document.getElementById('syntax-ref-tree');
  var groups={'Глобальные функции':{}, 'Методы объектов':{}, 'Конструкции языка':{'_':[]}, 'Язык запросов':{'_':[]}};
  data.forEach(function(d){
    if(d.kind==='func'){ (groups['Глобальные функции'][d.group||'Прочее']=groups['Глобальные функции'][d.group||'Прочее']||[]).push(d); }
    else if(d.kind==='method'){ (groups['Методы объектов'][d.object||'Прочее']=groups['Методы объектов'][d.object||'Прочее']||[]).push(d); }
    else if(d.kind==='keyword'){ groups['Конструкции языка']['_'].push(d); }
    else if(d.kind==='query'){ groups['Язык запросов']['_'].push(d); }
  });
  var html='';
  Object.keys(groups).forEach(function(top){
    var sub=groups[top], subKeys=Object.keys(sub).filter(function(k){return sub[k].length;});
    if(!subKeys.length) return;
    html+='<div style="font-weight:600;margin:6px 0 2px">'+top+'</div>';
    subKeys.sort().forEach(function(sk){
      if(sk!=='_'){ html+='<div style="color:#64748b;margin:3px 0 1px;padding-left:6px">'+sk+'</div>'; }
      sub[sk].slice().sort(function(a,b){return a.display.localeCompare(b.display);}).forEach(function(d){
        var idx=(window._langref||[]).indexOf(d);
        html+='<div style="padding:2px 6px 2px 16px;cursor:pointer;border-radius:3px" onmouseover="this.style.background=\'#eef2ff\'" onmouseout="this.style.background=\'\'" onclick="showSyntaxRefDetail('+idx+')">'+d.display+'</div>';
      });
    });
  });
  tree.innerHTML=html||'<div style="color:#94a3b8">'+'{{t .Lang "ничего не найдено"}}'+'</div>';
}
function showSyntaxRefDetail(idx){
  var d=(window._langref||[])[idx]; if(!d) return;
  var h='<div style="font-family:monospace;font-size:14px;font-weight:600;margin-bottom:6px">'+(d.signature||d.display)+'</div>';
  if(d.returns) h+='<div style="color:#475569;margin-bottom:6px">'+'{{t .Lang "Возвращает"}}'+': '+d.returns+'</div>';
  h+='<div style="margin-bottom:10px">'+(d.doc||'')+'</div>';
  if(d.params&&d.params.length){
    h+='<div style="font-weight:600;margin-bottom:3px">'+'{{t .Lang "Параметры"}}'+':</div><ul style="margin:0 0 10px 16px;padding:0">';
    d.params.forEach(function(p){ h+='<li><code>'+p.name+'</code> : '+(p.type||'')+(p.doc?' — '+p.doc:'')+(p.optional?' <em>(необяз.)</em>':'')+'</li>'; });
    h+='</ul>';
  }
  if(d.example) h+='<pre style="background:#f1f5f9;padding:8px;border-radius:4px;white-space:pre-wrap">'+d.example+'</pre>';
  h+='<button type="button" onclick="insertLangrefSignature('+idx+')" style="margin-top:8px;background:#1a4a80;color:#fff;border:none;border-radius:4px;padding:6px 12px;cursor:pointer">'+'{{t .Lang "Вставить в редактор"}}'+'</button>';
  document.getElementById('syntax-ref-detail').innerHTML=h;
}
function insertLangrefSignature(idx){
  var d=(window._langref||[])[idx]; if(!d) return;
  var id=window._lastFocusedEditorId, ed=id&&monacoEditors[id];
  if(!ed){ alert('{{t .Lang "Откройте редактор модуля и поставьте курсор"}}'); return; }
  ed.executeEdits('syntax-ref',[{range: ed.getSelection(), text: (d.signature||d.display)}]);
  ed.focus();
}
document.addEventListener('keydown',function(e){
  if(e.key==='F1'){ e.preventDefault(); toggleSyntaxRef(); }
  else if(e.key==='Escape'){ var p=document.getElementById('syntax-ref-panel'); if(p&&p.style.display!=='none') toggleSyntaxRef(false); }
});
</script>
{{end}}`
```

- [ ] **Step 3: Подключить шаблон в `cfgTmpl.Parse` и в `cfg-main`**

In `configurator_tmpl.go:104`, текущий `.Parse(...)`:

```go
}).Parse(cfgCSS + cfgHead + cfgMain + cfgTabTree + cfgRegDetail + cfgTabConvert + cfgTabFiles + cfgTabBackup + cfgFoot))
```

Replace with (добавить `cfgSyntaxRef`):

```go
}).Parse(cfgCSS + cfgHead + cfgMain + cfgTabTree + cfgRegDetail + cfgTabConvert + cfgTabFiles + cfgTabBackup + cfgSyntaxRef + cfgFoot))
```

И в const `cfgMain` (`{{define \"cfg-main\"}}`, строка ~3509), перед закрывающим `{{end}}` этого define, добавить вызов:

```html
{{template "syntax-ref" .}}
```

> `cfg-main` рендерится с данными `*configuratorData`, у которых есть `.Lang` и `.Base` — `{{t .Lang …}}` в `syntax-ref` работает (FuncMap `t` уже в `cfgTmpl`).

- [ ] **Step 4: Запустить смоук-тесты и сборку**

Run: `go build ./... && go test ./internal/launcher/ -run 'TestConfigurator_LangrefWired|TestConfigurator_SyntaxPanelWired' -v`
Expected: PASS.

- [ ] **Step 5: Ручная проверка окна**

Запустить лаунчер, открыть конфигуратор. Нажать F1 (или кнопку справа) → панель открывается, слева дерево (функции по группам, методы по объектам, конструкции, запросы), поиск фильтрует, клик показывает деталь. Поставить курсор в редактор модуля, в панели выбрать функцию → «Вставить в редактор» → сигнатура вставляется в позицию курсора. Esc закрывает панель.

- [ ] **Step 6: Финальная проверка всего проекта**

Run: `go build ./... && go test ./...`
Expected: всё зелёное.

- [ ] **Step 7: Commit**

```bash
git add internal/launcher/configurator_tmpl.go internal/launcher/langref_render_test.go
git commit -m "feat(configurator): окно Синтакс-помощник (дерево/поиск/деталь/вставка, F1/Esc)"
```

---

## Self-review плана

**Покрытие спеки (Plans/146-syntax-assistant.md):**
- Часть A (пакет langref, модель) → Task 1. ✓
- Часть B (гибрид: гейт + структурная + отчёт) → Task 2. ✓ (+ фикс KnownBuiltinNames для корректности reverse-гейта).
- Часть C (ai-guide из langref, удаление фабрик, тест) → Task 7. ✓
- Часть D (эндпоинт + Monaco completion/hover/signature + подсветка + best-effort `.`) → Task 8 (эндпоинт), Task 9 (провайдеры). ✓
- Часть E (окно: дерево/поиск/деталь/вставка, F1/Esc, закреплённая сворачиваемая панель) → Task 10. ✓
- Часть F (тесты: langref ядро, ai-guide, эндпоинт, JS-смоук) → Tasks 1/2 (ядро), 7 (guide), 8 (эндпоинт), 9-10 (смоук). ✓
- Охват контента (функции+конструкции+методы+запросы) → Tasks 3/4/5/6. ✓
- RU-описания, EN через алиасы; `{{t .Lang}}` на UI-подписях → Task 10. ✓
- Без новых зависимостей → подтверждено (chi/Monaco/stdlib). ✓

**Границы v1 соблюдены:** нет вывода типов (best-effort по `.` в Task 9), нет открепляемого режима окна (фиксированная панель Task 10), нет EN-перевода описаний, нет поля Deprecated, JS — только смоук + ручная проверка.

**Согласованность типов:** `Descriptor`/`Param`/`Kind` определены в Task 1 и используются единообразно в Tasks 3–7 и в JSON (поля `name/display/aliases/kind/object/signature/params/returns/doc/example/group` — те же в Go-тегах и в JS-провайдерах/панели). `loadLangref()`/`window._langref`/`window._lastFocusedEditorId`/`_onebaseMonarch` определены в Task 9 и используются в Task 10.

**Замечание по контенту:** Tasks 3–6 — авторская работа по наполнению дескрипторов; полный инвентарь имён приведён, формат показан семенами, приёмочный критерий — зелёный гейт (функции) и непустой отчёт охвата (методы/конструкции/запросы). Это не плейсхолдеры: источник (реализации функций) и проверка (гейт + ручная) определены.
