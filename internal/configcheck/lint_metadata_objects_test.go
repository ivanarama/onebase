package configcheck

import (
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"unicode/utf8"
)

func TestLintUnknownMetadataObjectDefaults(t *testing.T) {
	for _, strict := range []bool{false, true} {
		mode := "legacy"
		if strict {
			mode = "strict"
		}
		for _, tc := range []struct {
			name, source string
			wantWarning  bool
		}{
			{"same parameter", `Функция Прочитать(Документы = Документы.НетТакого)
  Возврат Документы.Произвольный;
КонецФункции
`, true},
			{"body assignment", `Функция Прочитать(Параметр = Документы.НетТакого)
  Документы = Новый Структура("Произвольный", 42);
  Возврат Документы.Произвольный;
КонецФункции
`, true},
			{"body declaration", `Функция Прочитать(Параметр = Документы.НетТакого)
  Перем Документы;
  Возврат Документы.Произвольный;
КонецФункции
`, true},
			{"other parameter", `Функция Прочитать(Параметр = Документы.НетТакого)
  Возврат Параметр;
КонецФункции
`, true},
			{"preceding parameter", `Функция Прочитать(Документы, Параметр = Документы.НетТакого)
  Возврат Параметр;
КонецФункции
`, true},
			{"module declaration", `Перем Документы;
Функция Прочитать(Параметр = Документы.НетТакого)
  Возврат Параметр;
КонецФункции
`, false},
			{"known object", `Функция Прочитать(Документы = Документы.Приход)
  Возврат Документы.Произвольный;
КонецФункции
`, false},
		} {
			t.Run(mode+"/"+tc.name, func(t *testing.T) {
				dir := t.TempDir()
				mkFile(t, filepath.Join(dir, "documents", "приход.yaml"), "name: Приход\nfields: []\n")
				mkFile(t, filepath.Join(dir, "processors", "проверка.yaml"), "name: Проверка\nparams: []\n")
				mkFile(t, filepath.Join(dir, "src", "проверка.proc.os"), tc.source)
				if strict {
					mkFile(t, filepath.Join(dir, "config", "app.yaml"), "name: Тест\ndsl:\n  strict_lexical_scope: true\n")
				}
				for _, lint := range []bool{false, true} {
					res := RunFullWithOptions(dir, Options{Lint: lint})
					if !res.OK {
						t.Fatalf("проверка конфигурации: %+v", res.Issues)
					}
					var got []Issue
					for _, w := range res.Warnings {
						if w.Code == "dsl.unknown-metadata-object" {
							got = append(got, w)
						}
					}
					if !lint || !tc.wantWarning {
						if len(got) != 0 {
							t.Fatalf("lint=%v: ложное предупреждение: %+v", lint, got)
						}
						continue
					}
					if len(got) != 1 {
						t.Fatalf("ожидалось предупреждение в дефолте, получено: %+v", got)
					}
					firstLine := strings.Split(tc.source, "\n")[0]
					col := utf8.RuneCountInString(firstLine[:strings.Index(firstLine, "НетТакого")]) + 1
					w := got[0]
					if w.File != "src/проверка.proc.os" || w.Object != "Проверка" || w.Kind != "DSL обработка" || w.Line != 1 || w.Column != col || !strings.HasPrefix(w.Message, "Документы.НетТакого:") {
						t.Errorf("неверная диагностика дефолта: %+v, ожидалась колонка %d", w, col)
					}
				}
			})
		}
	}
}

// Обращение к несуществующему объекту через глобальный менеджер
// (Документы.СписаниеСРасчётныйСчёт при документе «СписаниеСРасчётногоСчёта»)
// вычисляется в Неопределено. Проверка конфигурации его не видела: ошибка
// всплывала «Метод Создать вызван у Неопределено» только при выполнении строки,
// а в редкой ветке — у пользователя. Так в торговой конфигурации загрузка
// выписки создавала поступления, но ни одного списания со счёта.
//
// Проверка идёт через RunFullWithOptions — путь `onebase check --lint`.
func TestLintUnknownMetadataObject(t *testing.T) {
	dir := t.TempDir()
	writeMetadataObjectsProject(t, dir)

	plain := RunFull(dir)
	for _, w := range plain.Warnings {
		if w.Code == "dsl.unknown-metadata-object" {
			t.Fatalf("без --lint предупреждения быть не должно: %+v", w)
		}
	}

	res := RunFullWithOptions(dir, Options{Lint: true})
	if !res.OK {
		t.Fatalf("предупреждение не должно валить проверку: %+v", res.Issues)
	}
	var got []string
	for _, w := range res.Warnings {
		if w.Code == "dsl.unknown-metadata-object" {
			got = append(got, w.Kind+"|"+w.Message+"|"+w.SuggestedFix)
		}
	}
	sort.Strings(got)
	want := []struct{ kind, name, what, suggest string }{
		{"DSL обработка", "Документы.ПоступлениеНаРасчётныйСчт", "документ", "ПоступлениеНаРасчётныйСчёт"},
		{"DSL обработка", "Documents.Расход", "документ", ""},
		{"DSL объект", "Движения.Остатк", "регистр", "Остатки"},
		{"DSL форма обработки", "Справочники.Товр", "справочник", "Товар"},
	}
	if len(got) != len(want) {
		t.Fatalf("ожидалось %d предупреждения, получено %d:\n%s", len(want), len(got), strings.Join(got, "\n"))
	}
	for _, w := range want {
		found := false
		for _, g := range got {
			parts := strings.SplitN(g, "|", 3)
			if parts[0] != w.kind || !strings.HasPrefix(parts[1], w.name+":") {
				continue
			}
			found = true
			wantMessage := w.name + ": такого объекта (" + w.what + ") в конфигурации нет"
			if parts[1] != wantMessage {
				t.Errorf("%s: недостоверный текст предупреждения: %q, ожидалось %q", w.name, parts[1], wantMessage)
			}
			if w.suggest != "" && !strings.Contains(parts[2], w.suggest) {
				t.Errorf("%s: подсказка не называет %q: %q", w.name, w.suggest, parts[2])
			}
		}
		if !found {
			t.Errorf("нет предупреждения %s (%s) среди:\n%s", w.name, w.kind, strings.Join(got, "\n"))
		}
	}
}

func TestLintUnknownMetadataObjectMergedModuleScope(t *testing.T) {
	for _, tc := range []struct {
		name, objectModule, postingModule string
		wantWarnings                      int
	}{
		{
			name: "posting module shadows manager",
			objectModule: `Процедура ПриЗаписи()
КонецПроцедуры
`,
			postingModule: `Перем Документы;
Процедура Подготовить()
  Документы = Новый Структура("Произвольный", 42);
КонецПроцедуры
Функция Прочитать()
  Возврат Документы.Произвольный;
КонецФункции
Процедура ОбработкаПроведения()
  Подготовить();
  Значение = Прочитать();
КонецПроцедуры
`,
		},
		{
			name: "object module does not shadow posting manager",
			objectModule: `Перем Документы;
Процедура ПриЗаписи()
  Документы = Новый Структура("Произвольный", 42);
КонецПроцедуры
`,
			postingModule: `Процедура ОбработкаПроведения()
  Значение = Документы.НетТакого;
КонецПроцедуры
`,
			wantWarnings: 1,
		},
		{
			name: "posting module shadows default manager",
			objectModule: `Процедура ПриЗаписи()
КонецПроцедуры
`,
			postingModule: `Перем Документы;
Функция Прочитать(Параметр = Документы.Произвольный)
  Возврат Параметр;
КонецФункции
Процедура ОбработкаПроведения()
КонецПроцедуры
`,
		},
		{
			name: "object module does not shadow posting default",
			objectModule: `Перем Документы;
Процедура ПриЗаписи()
КонецПроцедуры
`,
			postingModule: `Функция Прочитать(Документы = Документы.НетТакого)
  Возврат Документы.Произвольный;
КонецФункции
Процедура ОбработкаПроведения()
КонецПроцедуры
`,
			wantWarnings: 1,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			writeMetadataObjectsProject(t, dir)
			mkFile(t, filepath.Join(dir, "src", "приход.os"), tc.objectModule)
			mkFile(t, filepath.Join(dir, "src", "приход.posting.os"), tc.postingModule)
			res := RunFullWithOptions(dir, Options{Lint: true})
			if !res.OK {
				t.Fatalf("проверка конфигурации: %+v", res.Issues)
			}
			var got []Issue
			for _, w := range res.Warnings {
				if w.Code == "dsl.unknown-metadata-object" && w.Object == "Приход" {
					got = append(got, w)
				}
			}
			if len(got) != tc.wantWarnings {
				t.Fatalf("ожидалось %d предупреждений для Приход, получено: %+v", tc.wantWarnings, got)
			}
		})
	}
}

func TestLintUnknownMetadataObjectMergedSourceLocations(t *testing.T) {
	for _, tc := range []struct {
		name, prefix string
		postingLine  int
	}{
		{"same coordinates", "", 2},
		{"posting line beyond object file", strings.Repeat("\n", 8), 10},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			writeMetadataObjectsProject(t, dir)
			mkFile(t, filepath.Join(dir, "src", "приход.os"), `Процедура ПриЗаписи()
  Значение = Документы.НетПервого;
КонецПроцедуры
`)
			mkFile(t, filepath.Join(dir, "src", "приход.posting.os"), tc.prefix+`Процедура ОбработкаПроведения()
  Значение = Документы.НетВторого;
КонецПроцедуры
`)
			res := RunFullWithOptions(dir, Options{Lint: true})
			if !res.OK {
				t.Fatalf("проверка конфигурации: %+v", res.Issues)
			}
			got := map[string]Issue{}
			count := 0
			for _, w := range res.Warnings {
				if w.Code == "dsl.unknown-metadata-object" && w.Object == "Приход" {
					got[w.File] = w
					count++
				}
			}
			if count != 2 || len(got) != 2 {
				t.Fatalf("ожидались два предупреждения в разных файлах, получено %d: %+v", count, got)
			}
			for _, want := range []struct {
				file, member string
				line         int
			}{
				{"src/приход.os", "Документы.НетПервого:", 2},
				{"src/приход.posting.os", "Документы.НетВторого:", tc.postingLine},
			} {
				w, ok := got[want.file]
				if !ok || w.Line != want.line || w.Column != 24 || !strings.HasPrefix(w.Message, want.member) {
					t.Errorf("неверный локатор %s:%d:24: %+v", want.file, want.line, w)
				}
			}
		})
	}
}

func writeMetadataObjectsProject(t *testing.T, dir string) {
	t.Helper()
	mkFile(t, filepath.Join(dir, "catalogs", "товар.yaml"), `name: Товар
fields:
  - {name: Наименование, type: string}
`)
	mkFile(t, filepath.Join(dir, "documents", "приход.yaml"), `name: Приход
posting: true
fields:
  - {name: Дата, type: date}
  - {name: Товар, type: "reference:Товар"}
`)
	mkFile(t, filepath.Join(dir, "documents", "поступление.yaml"), `name: ПоступлениеНаРасчётныйСчёт
fields:
  - {name: Дата, type: date}
`)
	mkFile(t, filepath.Join(dir, "registers", "остатки.yaml"), `name: Остатки
dimensions:
  - {name: Товар, type: "reference:Товар"}
resources:
  - {name: Количество, type: number}
`)
	mkFile(t, filepath.Join(dir, "inforegs", "цены.yaml"), `name: Цены
periodic: true
dimensions:
  - {name: Товар, type: "reference:Товар"}
resources:
  - {name: Цена, type: number}
`)
	mkFile(t, filepath.Join(dir, "enums", "вид.yaml"), `name: Вид
values: [Первый, Второй]
`)
	// Проведение: опечатка в имени регистра движений.
	mkFile(t, filepath.Join(dir, "src", "приход.posting.os"), `Процедура ОбработкаПроведения()
  Дв = Движения.Остатки.Добавить();
  Дв.Товар = this.Товар;
  Дв2 = Движения.Остатк.Добавить();
КонецПроцедуры
`)
	mkFile(t, filepath.Join(dir, "processors", "загрузка.yaml"), `name: Загрузка
params: []
`)
	// Известные имена молчат при любом регистре букв и через английский
	// синоним менеджера; опечатка и отсутствующий объект — предупреждение.
	mkFile(t, filepath.Join(dir, "src", "загрузка.proc.os"), `Процедура Выполнить() Экспорт
  Д = Документы.ПоступлениеНаРасчётныйСчт.Создать();
  П = документы.приход.Создать();
  Т = Справочники.Товар.Создать();
  Р = РегистрыНакопления.Остатки;
  ИР = РегистрыСведений.Цены;
  ВП = Перечисления.Вид.Первый;
  ДА = Documents.Приход;
  ДБ = Documents.Расход;
КонецПроцедуры
`)
	// Имя менеджера, занятое переменной модуля, — уже не менеджер.
	mkFile(t, filepath.Join(dir, "src", "обмен.module.os"), `Функция Сопоставление() Экспорт
  Документы = Новый Соответствие;
  Документы.Вставить("Реализация", 1);
  Возврат Документы.Реализация;
КонецФункции
`)
	// Форма обработки проверяется так же, как модуль обработки.
	mkFile(t, filepath.Join(dir, "forms", "загрузка", "формаобъекта.form.yaml"), `schema: onebase.form/v1
form:
  name: ФормаОбъекта
  kind: object
  entity: Загрузка
events:
  ПриОткрытии: ПриОткрытииФормы
elements: []
`)
	mkFile(t, filepath.Join(dir, "forms", "загрузка", "формаобъекта.form.os"), `Процедура ПриОткрытииФормы()
  Т = Справочники.Товр.Создать();
КонецПроцедуры
`)
}

// Хуки удаления объекта (ПередУдалением, ПослеУдаления) вызывает
// entityservice.Delete, но в списке точек входа проверки их не было: каждая
// конфигурация, закрывающая удаление в закрытом периоде, получала
// «процедура не достижима» на каждом документе.
func TestLintDeleteHooksAreEntryPoints(t *testing.T) {
	dir := t.TempDir()
	mkFile(t, filepath.Join(dir, "documents", "приход.yaml"), `name: Приход
posting: true
fields:
  - {name: Дата, type: date}
`)
	mkFile(t, filepath.Join(dir, "src", "приход.posting.os"), `Процедура ОбработкаПроведения()
КонецПроцедуры

Процедура ПередУдалением()
КонецПроцедуры

Процедура ПослеУдаления()
КонецПроцедуры
`)
	res := RunFullWithOptions(dir, Options{Lint: true})
	for _, w := range res.Warnings {
		if w.Code == "dsl.dead-procedure" {
			t.Errorf("хук удаления объявлен мёртвым: %+v", w)
		}
	}
}
