package configcheck

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestRunFullWithLintWarnings(t *testing.T) {
	dir := t.TempDir()
	mkFile(t, filepath.Join(dir, "catalogs", "клиент.yaml"), `name: Клиент
unknown_top_key: true
fields:
  - name: Наименование
    type: string
`)
	mkFile(t, filepath.Join(dir, "documents", "заказ.yaml"), `name: Заказ
fields:
  - name: Номер
    type: string
`)
	mkFile(t, filepath.Join(dir, "processors", "мусор.yaml"), `name: Мусор
params: []
`)
	mkFile(t, filepath.Join(dir, "src", "мусор.proc.os"), `Процедура Выполнить() Экспорт
  Перем Лишняя, Нужная;
  Нужная = 1;
  Сообщить(Нужная);
КонецПроцедуры

Процедура Мертвая()
КонецПроцедуры
`)
	mkFile(t, filepath.Join(dir, "roles", "оператор.yaml"), `name: Оператор
permissions:
  catalogs:
    Клиент: [read]
  processors: {}
`)

	plain := RunFull(dir)
	if !plain.OK {
		t.Fatalf("plain check should be OK: %+v", plain.Issues)
	}
	for _, w := range plain.Warnings {
		if w.Code == "metadata.unvalidated-key" || w.Code == "dsl.unused-var" ||
			w.Code == "dsl.dead-procedure" || w.Code == "rbac.object-without-role" {
			t.Fatalf("plain RunFull unexpectedly returned lint warning: %+v", w)
		}
	}

	lint := RunFullWithOptions(dir, Options{Lint: true})
	if !lint.OK {
		t.Fatalf("lint check should keep OK=true for warnings: %+v", lint.Issues)
	}
	want := map[string]bool{
		"metadata.unvalidated-key": false,
		"dsl.unused-var":           false,
		"dsl.dead-procedure":       false,
		"rbac.object-without-role": false,
	}
	for _, w := range lint.Warnings {
		if _, ok := want[w.Code]; ok {
			want[w.Code] = true
		}
	}
	for code, found := range want {
		if !found {
			t.Fatalf("lint warning %s not found; got %+v", code, lint.Warnings)
		}
	}
}

func TestLintTreatsUnpostHooksAsEntityRoots(t *testing.T) {
	dir := t.TempDir()
	mkFile(t, filepath.Join(dir, "documents", "заказ.yaml"), `name: Заказ
posting: true
fields:
  - name: Номер
    type: string
`)
	mkFile(t, filepath.Join(dir, "src", "заказ.posting.os"), `Процедура ОбработкаУдаленияПроведения()
КонецПроцедуры

Процедура OnUnpost()
КонецПроцедуры
`)

	res := RunFullWithOptions(dir, Options{Lint: true})
	if !res.OK {
		t.Fatalf("lint failed: %+v", res.Issues)
	}
	for _, warning := range res.Warnings {
		if warning.Code == "dsl.dead-procedure" &&
			(strings.Contains(warning.Message, "ОбработкаУдаленияПроведения") || strings.Contains(warning.Message, "OnUnpost")) {
			t.Fatalf("unpost hook отмечен как недостижимый: %+v", warning)
		}
	}
}

func TestLintYAML_ActivityKeyKnown(t *testing.T) {
	dir := t.TempDir()
	// Блок activity (активность справочников) читается загрузчиком — линт не
	// должен помечать его как неизвестный ключ.
	mkFile(t, filepath.Join(dir, "catalogs", "товар.yaml"), `name: Товар
fields:
  - name: Активный
    type: bool
activity:
  field: Активный
  default_scope: active
  hide_from_choice: true
`)
	for _, is := range CheckLintYAML(dir) {
		if is.Code == "metadata.unvalidated-key" {
			t.Fatalf("блок activity должен быть известен линту, получено: %+v", is)
		}
	}
}

func TestLintYAML_IndexesKeyKnown(t *testing.T) {
	dir := t.TempDir()
	mkFile(t, filepath.Join(dir, "catalogs", "товар.yaml"), `name: Товар
fields:
  - name: Артикул
    type: string
indexes:
  - fields: [Артикул]
    unique: true
`)
	for _, is := range CheckLintYAML(dir) {
		if is.Code == "metadata.unvalidated-key" {
			t.Fatalf("блок indexes должен быть известен линту, получено: %+v", is)
		}
	}
}

func TestLintYAML_LiveListKeysKnown(t *testing.T) {
	dir := t.TempDir()
	mkFile(t, filepath.Join(dir, "documents", "задача.yaml"), `name: Задача
notify_changes: true
list_refresh_on:
  - данные.задача
fields:
  - name: Номер
    type: string
`)
	for _, is := range CheckLintYAML(dir) {
		if is.Code == "metadata.unvalidated-key" {
			t.Fatalf("ключи живого списка должны быть известны линту, получено: %+v", is)
		}
	}
}

func TestLintYAML_DetailPanelChecksNestedKeys(t *testing.T) {
	dir := t.TempDir()
	mkFile(t, filepath.Join(dir, "catalogs", "товар.yaml"), `name: Товар
fields:
  - name: Артикул
    type: string
detail_panel:
  title: Артикул
  widht: 360
  tabs:
    - name: Main
      titles: {en: Main}
      fields: [Артикул]
      tableparts: []
      attachments: false
      filed: Артикул
`)
	var paths []string
	for _, issue := range CheckLintYAML(dir) {
		if issue.Code == "metadata.unvalidated-key" {
			paths = append(paths, issue.Message)
		}
	}
	joined := strings.Join(paths, "\n")
	for _, want := range []string{"detail_panel.widht", "detail_panel.tabs[].filed"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("nested typo %q not reported; issues=%s", want, joined)
		}
	}
	for _, known := range []string{"detail_panel.tabs[].tableparts", "detail_panel.tabs[].attachments"} {
		if strings.Contains(joined, known) {
			t.Fatalf("reserved known key %q reported as unknown; issues=%s", known, joined)
		}
	}
}

func TestLintProject_ListFormFieldWithoutIndex(t *testing.T) {
	dir := t.TempDir()
	mkFile(t, filepath.Join(dir, "catalogs", "товар.yaml"), `name: Товар
fields:
  - name: Артикул
    type: string
  - name: Наименование
    type: string
list_form: [Артикул, Наименование]
indexes:
  - fields: [Наименование]
`)

	lint := RunFullWithOptions(dir, Options{Lint: true})
	if !lint.OK {
		t.Fatalf("lint check should keep OK=true for warnings: %+v", lint.Issues)
	}
	var found *Issue
	for i := range lint.Warnings {
		if lint.Warnings[i].Code == "metadata.list-field-without-index" {
			found = &lint.Warnings[i]
			break
		}
	}
	if found == nil {
		t.Fatalf("metadata.list-field-without-index not found; got %+v", lint.Warnings)
		return
	}
	if !strings.Contains(found.Message, "Артикул") {
		t.Fatalf("warning should point to Артикул, got %+v", found)
	}
}

func TestLintProject_ListFormLeadingIndexSuppressesWarning(t *testing.T) {
	dir := t.TempDir()
	mkFile(t, filepath.Join(dir, "catalogs", "товар.yaml"), `name: Товар
fields:
  - name: Артикул
    type: string
list_form: [Артикул]
indexes:
  - fields: [Артикул]
`)

	lint := RunFullWithOptions(dir, Options{Lint: true})
	for _, w := range lint.Warnings {
		if w.Code == "metadata.list-field-without-index" {
			t.Fatalf("unexpected index warning: %+v", w)
		}
	}
}

func TestLintYAML_JournalConditionalKnown(t *testing.T) {
	dir := t.TempDir()
	mkFile(t, filepath.Join(dir, "journals", "ж.yaml"), `name: Ж
documents: [Док]
columns:
  - field: Сумма
conditional:
  - when: Сумма < 0
    field: Сумма
    style:
      color: "#c00"
conditional_formatting:
  - when: Документ = "Док"
    then:
      background: yellow
`)
	for _, is := range CheckLintYAML(dir) {
		if is.Code == "metadata.unvalidated-key" {
			t.Fatalf("условное оформление журнала должно быть известно линту, получено: %+v", is)
		}
	}
}

func TestLintYAML_FormConditionalKnown(t *testing.T) {
	dir := t.TempDir()
	mkFile(t, filepath.Join(dir, "forms", "заказ", "объекта.form.yaml"), `schema: onebase.form/v1
form:
  name: ФормаОбъекта
  kind: object
  entity: Заказ
elements:
  - kind: ТабличнаяЧасть
    name: ТаблицаТовары
    data_path: Объект.Товары
conditional:
  - target: Товары
    when: Количество < 0
    field: Сумма
    style:
      color: "#c00"
conditional_formatting:
  - element: ТаблицаТовары
    when: Сумма < 0
    then:
      background: yellow
`)
	for _, is := range CheckLintYAML(dir) {
		if is.Code == "metadata.unvalidated-key" {
			t.Fatalf("условное оформление формы должно быть известно линту, получено: %+v", is)
		}
	}
}

func TestLintYAML_FormAccessKeyKnown(t *testing.T) {
	dir := t.TempDir()
	mkFile(t, filepath.Join(dir, "forms", "заказ", "объекта.form.yaml"), `schema: onebase.form/v1
form:
  name: ФормаОбъекта
  kind: object
  entity: Заказ
elements:
  - kind: ПолеВвода
    name: ПолеНомер
    data_path: Объект.Номер
    accesskey: "N"
	  - kind: Кнопка
	    name: КнопкаКопировать
	    accesskey: "7"
	    hotkey: F7
`)
	for _, is := range CheckLintYAML(dir) {
		if is.Code == "metadata.unvalidated-key" {
			t.Fatalf("accesskey формы должен быть известен линту, получено: %+v", is)
		}
	}
}

func TestLintYAML_FormHotkeyWarnings(t *testing.T) {
	dir := t.TempDir()
	mkFile(t, filepath.Join(dir, "forms", "заказ", "объекта.form.yaml"), `schema: onebase.form/v1
form:
  name: ФормаОбъекта
  kind: object
  entity: Заказ
elements:
  - kind: Кнопка
    name: КнопкаКопировать
    hotkey: F7
  - kind: Кнопка
    name: КнопкаСоздатьНаОсновании
    hotkey: f7
  - kind: Кнопка
    name: КнопкаОбновить
    hotkey: F5
  - kind: ПолеВвода
    name: ПолеНомер
    data_path: Объект.Номер
    hotkey: F8
`)
	got := map[string]int{}
	for _, is := range CheckLintYAML(dir) {
		got[is.Code]++
	}
	for _, code := range []string{"form.duplicate-hotkey", "form.unsupported-hotkey", "form.ignored-hotkey"} {
		if got[code] == 0 {
			t.Fatalf("ожидался warning %s, получено: %+v", code, got)
		}
	}
	if got["metadata.unvalidated-key"] != 0 {
		t.Fatalf("hotkey формы должен быть известен линту, получено: %+v", got)
	}
}

func TestLintYAML_RoleRowAccessKeysKnown(t *testing.T) {
	dir := t.TempDir()
	mkFile(t, filepath.Join(dir, "roles", "manager.yaml"), `name: Manager
permissions:
  ai_data_access: true
  catalogs:
    Клиент: [read]
  row_access:
    catalogs:
      Клиент:
        read:
          field: Owner
          op: eq
          value: { user: login }
`)
	for _, is := range CheckLintYAML(dir) {
		if is.Code == "metadata.unvalidated-key" {
			t.Fatalf("row_access/ai_data_access роли должны быть известны YAML-линту, получено: %+v", is)
		}
	}
}

func TestLintRoles_RowAccessDiagnostics(t *testing.T) {
	dir := t.TempDir()
	mkFile(t, filepath.Join(dir, "catalogs", "клиент.yaml"), `name: Клиент
fields:
  - name: Owner
    type: string
`)
	mkFile(t, filepath.Join(dir, "roles", "manager.yaml"), `name: Manager
permissions:
  catalogs:
    Клиент: [read, delete]
  row_access:
    catalogs:
      Клиент:
        read:
          field: НетТакогоПоля
          op: eq
          value: { user: login }
        write:
          field: Owner
          op: eq
          value: { user_attr: department }
        delete:
          same_as: missing
      Несуществующий:
        read:
          field: Owner
          op: eq
          value: { user: login }
`)

	res := RunFullWithOptions(dir, Options{Lint: true})
	if !res.OK {
		t.Fatalf("row_access lint warnings should not fail check: %+v", res.Issues)
	}
	got := map[string]int{}
	for _, w := range res.Warnings {
		got[w.Code]++
	}
	for _, code := range []string{"rls.invalid-policy", "rls.policy-without-permission", "rls.unknown-object"} {
		if got[code] == 0 {
			t.Fatalf("expected %s warning, got codes=%+v warnings=%+v", code, got, res.Warnings)
		}
	}
	if got["rls.invalid-policy"] < 3 {
		t.Fatalf("expected invalid field, invalid user_attr and invalid same_as warnings, got codes=%+v warnings=%+v", got, res.Warnings)
	}
}

// Опечатка в whitelist `roles:` подсистемы или страницы прячет объект у всех
// не-админов — линт должен предупредить, но не валить check (роль может жить
// только в БД).
func TestLintRoles_UnknownRoleRefs(t *testing.T) {
	dir := t.TempDir()
	mkFile(t, filepath.Join(dir, "roles", "кладовщик.yaml"), `name: Кладовщик
permissions:
  catalogs:
    Товар: [read]
`)
	mkFile(t, filepath.Join(dir, "catalogs", "товар.yaml"), `name: Товар
fields:
  - name: Наименование
    type: string
`)
	mkFile(t, filepath.Join(dir, "subsystems", "склад.yaml"), `name: Склад
roles: [Кладовщик, Упрвленец]
contents:
  catalogs: [Товар]
`)
	mkFile(t, filepath.Join(dir, "pages", "арм.yaml"), `name: АРМ
title: АРМ
roles: [НетТакойРоли]
`)
	mkFile(t, filepath.Join(dir, "src", "арм.page.os"), `Процедура ПриФормировании(Страница)
КонецПроцедуры
`)

	res := RunFullWithOptions(dir, Options{Lint: true})
	if !res.OK {
		t.Fatalf("unknown-role warnings should not fail check: %+v", res.Issues)
	}
	var got []string
	for _, w := range res.Warnings {
		if w.Code == "rbac.unknown-role" {
			got = append(got, w.Object+":"+w.Message)
		}
	}
	if len(got) != 2 {
		t.Fatalf("expected 2 rbac.unknown-role warnings (подсистема + страница), got %v (all=%+v)", got, res.Warnings)
	}
	joined := strings.Join(got, "\n")
	if !strings.Contains(joined, "Упрвленец") || !strings.Contains(joined, "НетТакойРоли") {
		t.Fatalf("warnings should name the missing roles, got:\n%s", joined)
	}
}

func TestLintRoles_RowAccessValid(t *testing.T) {
	dir := t.TempDir()
	mkFile(t, filepath.Join(dir, "catalogs", "клиент.yaml"), `name: Клиент
fields:
  - name: Owner
    type: string
`)
	mkFile(t, filepath.Join(dir, "roles", "manager.yaml"), `name: Manager
permissions:
  catalogs:
    Клиент: [read, write]
  row_access:
    catalogs:
      Клиент:
        read:
          field: Owner
          op: eq
          value: { user_attr: full_name }
        write:
          same_as: read
`)

	res := RunFullWithOptions(dir, Options{Lint: true})
	for _, w := range res.Warnings {
		if strings.HasPrefix(w.Code, "rls.") {
			t.Fatalf("unexpected rls lint warning: %+v", w)
		}
	}
}

func TestLintRoles_FieldAccessDiagnostics(t *testing.T) {
	dir := t.TempDir()
	mkFile(t, filepath.Join(dir, "catalogs", "клиент.yaml"), `name: Клиент
fields:
  - name: Телефон
    type: string
  - name: Возраст
    type: number
`)
	mkFile(t, filepath.Join(dir, "catalogs", "архив.yaml"), `name: Архив
fields:
  - name: Owner
    type: string
`)
	mkFile(t, filepath.Join(dir, "catalogs", "секрет.yaml"), `name: Секрет
fields:
  - name: X
    type: string
`)
	mkFile(t, filepath.Join(dir, "roles", "operator.yaml"), `name: Оператор
permissions:
  catalogs:
    Клиент: [read]
    Архив: [write]
    Секрет: [disclose]
  field_access:
    catalogs:
      Клиент:
        Телефон: { read: mask_tail, keep: 4 }
        НетТакогоПоля: { read: hide }
        Возраст: { read: mask_city }
      Архив:
        Owner: { read: hide }
      Несуществующий:
        Owner: { read: hide }
`)

	res := RunFullWithOptions(dir, Options{Lint: true})
	if !res.OK {
		t.Fatalf("field_access lint warnings should not fail check: %+v", res.Issues)
	}
	got := map[string]int{}
	for _, w := range res.Warnings {
		got[w.Code]++
	}
	for _, code := range []string{"mask.invalid-policy", "mask.policy-without-permission", "mask.unknown-object", "mask.disclose-without-read"} {
		if got[code] == 0 {
			t.Fatalf("expected %s warning, got codes=%+v warnings=%+v", code, got, res.Warnings)
		}
	}
	if got["mask.invalid-policy"] < 2 {
		t.Fatalf("expected unknown-field and mask_city-type warnings, got codes=%+v", got)
	}
}

func TestLintCrossScopeRead(t *testing.T) {
	dir := t.TempDir()
	mkFile(t, filepath.Join(dir, "processors", "касса.yaml"), `name: Касса
params: []
`)
	// ПрочитатьСекрет читает «Секрет», объявленную только в Выполнить: сегодня
	// работает лишь из-за утечки области видимости вызова.
	mkFile(t, filepath.Join(dir, "src", "касса.proc.os"), `Процедура Выполнить() Экспорт
  Секрет = 42;
  Сообщить(ПрочитатьСекрет());
КонецПроцедуры

Функция ПрочитатьСекрет()
  Возврат Секрет;
КонецФункции
`)

	plain := RunFull(dir)
	if !plain.OK {
		t.Fatalf("plain check should keep legacy cross-scope read non-blocking: %+v", plain.Issues)
	}
	for _, w := range plain.Warnings {
		if w.Code == "dsl.cross-scope-read" {
			t.Fatalf("plain RunFull unexpectedly returned cross-scope warning: %+v", w)
		}
	}

	lint := RunFullWithOptions(dir, Options{Lint: true})
	if !lint.OK {
		t.Fatalf("lint should keep OK=true for warnings: %+v", lint.Issues)
	}
	var found *Issue
	for i := range lint.Warnings {
		if lint.Warnings[i].Code == "dsl.cross-scope-read" {
			found = &lint.Warnings[i]
		}
	}
	if found == nil {
		t.Fatalf("dsl.cross-scope-read not found; got %+v", lint.Warnings)
	}
	if !strings.Contains(found.Message, "Секрет") {
		t.Errorf("message should name the leaked variable: %q", found.Message)
	}
	if found.Line != 7 {
		t.Errorf("expected warning at line 7 (Возврат Секрет), got %d", found.Line)
	}
}

// Обращение к полю несуществующего имени — молчаливая порча данных: nil
// склеивается со строкой как «<nil>» и уезжает в результат. Ни ошибки, ни
// предупреждения сегодня нет, потому что dsl.unknown-function смотрит только на
// callee вызова, а тут — чтение поля.
func TestLintUnknownGlobalMember(t *testing.T) {
	dir := t.TempDir()
	mkFile(t, filepath.Join(dir, "processors", "письмо.yaml"), `name: Письмо
params: []
`)
	mkFile(t, filepath.Join(dir, "src", "почта.module.os"), `Функция Подпись() Экспорт
  Возврат "--";
КонецФункции
`)
	mkFile(t, filepath.Join(dir, "src", "письмо.proc.os"), `Процедура Выполнить() Экспорт
  Текст = "Итого" + Симвлы.ПС + Почта.Подпись();
  Локальная = Новый Структура("Поле", 1);
  Сообщить(Текст + Строка(Локальная.Поле) + Строка(Справочники.Товары));
КонецПроцедуры
`)

	plain := RunFull(dir)
	for _, w := range plain.Warnings {
		if w.Code == "dsl.unknown-global-member" {
			t.Fatalf("без --lint предупреждения быть не должно: %+v", w)
		}
	}

	res := RunFullWithOptions(dir, Options{Lint: true})
	if !res.OK {
		t.Fatalf("предупреждение не должно валить проверку: %+v", res.Issues)
	}
	var found []Issue
	for _, w := range res.Warnings {
		if w.Code == "dsl.unknown-global-member" {
			found = append(found, w)
		}
	}
	if len(found) != 1 {
		t.Fatalf("ожидалось ровно одно предупреждение (опечатка Симвлы), получено %+v", found)
	}
	if !strings.Contains(found[0].Message, "Симвлы") {
		t.Errorf("в сообщении должно быть имя: %q", found[0].Message)
	}
	if found[0].Line != 2 {
		t.Errorf("ожидалась строка 2, получена %d", found[0].Line)
	}
}

// Ложные срабатывания дороже пропусков: имя модуля, инжектируемый глобал,
// тест-контекст и локальная переменная обязаны молчать.
func TestLintUnknownGlobalMemberIgnoresKnownNames(t *testing.T) {
	dir := t.TempDir()
	mkFile(t, filepath.Join(dir, "processors", "тестпочты.yaml"), `name: ТестПочты
kind: test
params: []
`)
	mkFile(t, filepath.Join(dir, "src", "почта.module.os"), `Функция Подпись() Экспорт
  Возврат "--";
КонецФункции
`)
	mkFile(t, filepath.Join(dir, "src", "тестпочты.proc.os"), `Перем Настройка;

Процедура Выполнить() Экспорт
  Локальная = Новый Структура("Поле", 1);
  Утверждать.Равно(Локальная.Поле, 1, "локальная");
  Утверждать.Равно(Почта.Подпись(), "--", "модуль");
  Утверждать.Равно(Мок.Email.Количество(), 0, "мок");
  Утверждать.Истина(Часы.Сейчас() <> Неопределено, "часы");
  Утверждать.Истина(Настройка.Поле = Неопределено, "переменная модуля");
  Утверждать.Истина(Справочники.Товары <> Неопределено, "глобал");
КонецПроцедуры
`)

	res := RunFullWithOptions(dir, Options{Lint: true})
	for _, w := range res.Warnings {
		if w.Code == "dsl.unknown-global-member" {
			t.Fatalf("ложное срабатывание: %+v", w)
		}
	}
}

func TestLintUnknownGlobalMemberTestGlobalsAreTestOnly(t *testing.T) {
	dir := t.TempDir()
	mkFile(t, filepath.Join(dir, "processors", "обычная.yaml"), `name: Обычная
params: []
`)
	mkFile(t, filepath.Join(dir, "src", "обычная.proc.os"), `Процедура Выполнить() Экспорт
  Мок.Email.Количество();
КонецПроцедуры
`)

	res := RunFullWithOptions(dir, Options{Lint: true})
	for _, warning := range res.Warnings {
		if warning.Code == "dsl.unknown-global-member" && strings.Contains(warning.Message, "Мок") {
			return
		}
	}
	t.Fatalf("обычная обработка не должна получать test-only глобал Мок: %+v", res.Warnings)
}

func TestLintUnknownGlobalMemberObjectProgramIsNotGlobalNamespace(t *testing.T) {
	dir := t.TempDir()
	mkFile(t, filepath.Join(dir, "documents", "заказ.yaml"), `name: Заказ
fields: []
`)
	mkFile(t, filepath.Join(dir, "processors", "проверка.yaml"), `name: Проверка
params: []
`)
	mkFile(t, filepath.Join(dir, "src", "заказ.object.os"), `Процедура ПриЗаписи() Экспорт
КонецПроцедуры
`)
	mkFile(t, filepath.Join(dir, "src", "проверка.proc.os"), `Процедура Выполнить() Экспорт
  Заказ.НесуществующаяПроцедура();
КонецПроцедуры
`)

	res := RunFullWithOptions(dir, Options{Lint: true})
	for _, warning := range res.Warnings {
		if warning.Code == "dsl.unknown-global-member" && strings.Contains(warning.Message, "Заказ") {
			return
		}
	}
	t.Fatalf("объектный модуль не должен считаться глобальным namespace: %+v", res.Warnings)
}

func TestStrictLexicalScopePromotesCrossScopeRead(t *testing.T) {
	dir := t.TempDir()
	mkFile(t, filepath.Join(dir, "config", "app.yaml"), `name: Test
dsl:
  strict_lexical_scope: true
`)
	mkFile(t, filepath.Join(dir, "processors", "касса.yaml"), `name: Касса
params: []
`)
	mkFile(t, filepath.Join(dir, "src", "касса.proc.os"), `Процедура Выполнить() Экспорт
  Секрет = 42;
  Сообщить(ПрочитатьСекрет());
КонецПроцедуры

Функция ПрочитатьСекрет()
  Возврат Секрет;
КонецФункции
`)

	res := RunFullWithOptions(dir, Options{Lint: true})
	if res.OK {
		t.Fatalf("strict lexical scope should fail on cross-scope read")
	}
	var issue *Issue
	for i := range res.Issues {
		if res.Issues[i].Code == "dsl.cross-scope-read" {
			issue = &res.Issues[i]
		}
	}
	if issue == nil {
		t.Fatalf("dsl.cross-scope-read issue not found; got %+v", res.Issues)
	}
	if !strings.Contains(issue.Message, "strict_lexical_scope") {
		t.Fatalf("strict issue should mention strict mode: %+v", issue)
	}
	for _, w := range res.Warnings {
		if w.Code == "dsl.cross-scope-read" {
			t.Fatalf("strict + lint should not duplicate cross-scope warning: %+v", w)
		}
	}
}

func TestLintCrossScopeRead_ParamIsClean(t *testing.T) {
	dir := t.TempDir()
	mkFile(t, filepath.Join(dir, "processors", "касса.yaml"), `name: Касса
params: []
`)
	// Здесь «Секрет» передаётся параметром — утечки нет, предупреждения быть не должно.
	mkFile(t, filepath.Join(dir, "src", "касса.proc.os"), `Процедура Выполнить() Экспорт
  Секрет = 42;
  Сообщить(ПрочитатьСекрет(Секрет));
КонецПроцедуры

Функция ПрочитатьСекрет(Секрет)
  Возврат Секрет;
КонецФункции
`)

	lint := RunFullWithOptions(dir, Options{Lint: true})
	for _, w := range lint.Warnings {
		if w.Code == "dsl.cross-scope-read" {
			t.Fatalf("unexpected cross-scope-read for a parameter: %+v", w)
		}
	}
}

func TestStrictLexicalScopeParamIsClean(t *testing.T) {
	dir := t.TempDir()
	mkFile(t, filepath.Join(dir, "config", "app.yaml"), `name: Test
dsl:
  strict_lexical_scope: true
`)
	mkFile(t, filepath.Join(dir, "processors", "касса.yaml"), `name: Касса
params: []
`)
	mkFile(t, filepath.Join(dir, "src", "касса.proc.os"), `Процедура Выполнить() Экспорт
  Секрет = 42;
  Сообщить(ПрочитатьСекрет(Секрет));
КонецПроцедуры

Функция ПрочитатьСекрет(Секрет)
  Возврат Секрет;
КонецФункции
`)

	res := RunFull(dir)
	if !res.OK {
		t.Fatalf("strict lexical scope should allow explicit parameter passing: %+v", res.Issues)
	}
	for _, is := range res.Issues {
		if is.Code == "dsl.cross-scope-read" {
			t.Fatalf("unexpected cross-scope-read issue: %+v", is)
		}
	}
}

// Устойчивый `id` реквизита линт обязан знать (#873, дефект Д11 из #668).
//
// Линт объявлял его неизвестным ключом («загрузчик его игнорирует») и советовал
// удалить. Совет прямо противоречил механизму защиты данных: именно по `id`
// миграция отличает переименование от «удалили одно поле, добавили другое», а
// PlanTableChanges строит по нему сторож от тихой потери колонки. Пользователь,
// послушавшийся линта, снимал страховку.
func TestLintYAML_FieldIDKeyKnown(t *testing.T) {
	dir := t.TempDir()
	mkFile(t, filepath.Join(dir, "catalogs", "контрагенты.yaml"), `name: Контрагенты
fields:
  - {id: f_name, name: Наименование, type: string}
tableparts:
  - name: Контакты
    fields:
      - {id: tp_phone, name: Телефон, type: string}
`)
	for _, is := range CheckLintYAML(dir) {
		if is.Code == "metadata.unvalidated-key" {
			t.Fatalf("id реквизита должен быть известен линту, получено: %+v", is)
		}
	}
}

// Обратная сторона: опечатка в имени ключа по-прежнему ловится. Ради этого
// проверка и заведена, и «разрешить всё» вместо неё было бы хуже дефекта.
func TestLintYAML_ОпечаткаВКлючеПоляЛовится(t *testing.T) {
	dir := t.TempDir()
	mkFile(t, filepath.Join(dir, "catalogs", "контрагенты.yaml"), `name: Контрагенты
fields:
  - {ид: f_name, name: Наименование, type: string}
`)
	found := false
	for _, is := range CheckLintYAML(dir) {
		if is.Code == "metadata.unvalidated-key" && strings.Contains(is.Message, "ид") {
			found = true
		}
	}
	if !found {
		t.Fatal("опечатка «ид» вместо «id» не замечена — проверка перестала ловить то, ради чего заведена")
	}
}

// Расширенная запись item_form (#1011) должна проходить линт как есть, а
// опечатка в её ключе — ловиться. Иначе `readonly: true`, написанное как
// `read_only`, молча ничего не делает: поле остаётся редактируемым, и понять
// это можно только по поведению формы.
func TestLintItemFormReadonlyEntry(t *testing.T) {
	dir := t.TempDir()
	mkFile(t, filepath.Join(dir, "catalogs", "клиент.yaml"), `name: Клиент
fields:
  - name: Наименование
    type: string
  - name: ТелефоныНорм
    type: string
item_form:
  - Наименование
  - name: ТелефоныНорм
    readonly: true
`)
	res := RunFullWithOptions(dir, Options{Lint: true})
	for _, w := range res.Warnings {
		if w.Code == "metadata.unvalidated-key" {
			t.Fatalf("корректная запись item_form помечена как неизвестный ключ: %+v", w)
		}
	}

	mkFile(t, filepath.Join(dir, "catalogs", "клиент.yaml"), `name: Клиент
fields:
  - name: Наименование
    type: string
  - name: ТелефоныНорм
    type: string
item_form:
  - Наименование
  - name: ТелефоныНорм
    read_only: true
`)
	res = RunFullWithOptions(dir, Options{Lint: true})
	var found bool
	for _, w := range res.Warnings {
		if w.Code == "metadata.unvalidated-key" && strings.Contains(w.Message, "read_only") {
			found = true
		}
	}
	if !found {
		t.Fatalf("опечатка в ключе записи item_form не замечена: %+v", res.Warnings)
	}
}
