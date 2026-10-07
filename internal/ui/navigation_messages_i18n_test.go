package ui

import (
	"bytes"
	"strings"
	"testing"

	"github.com/ivantit66/onebase/internal/i18n"
	"github.com/ivantit66/onebase/internal/metadata"
)

// Сообщения отмены перехода ОткрытьФорму приходят в managed.js из шаблона, а не
// лежат в нём строкой: иначе они остаются русскими во всех языках интерфейса.
//
// Проверяется именно английская форма — её и просил владелец. Заодно тест
// закрывает пробел инструмента: i18ncheck ищет `{{t … }}` с двойными скобками и
// словарь closeMessages, где вызовы идут как `(t $.Lang "…")` внутри одного
// блока, не видит вовсе. Поэтому отсутствие перевода здесь обязательный CI не
// поймает — ловит этот тест.
func TestManagedFormNavigationMessagesLocalized(t *testing.T) {
	form := &metadata.FormModule{
		Name: "Объекта", Kind: "object", EntityName: "Контрагент",
		LayoutKind: metadata.FormLayoutManaged,
		Elements: []*metadata.FormElement{
			{Kind: metadata.FormElementField, Name: "Поле", DataPath: "Объект.Наименование"},
		},
	}
	ent := &metadata.Entity{
		Name: "Контрагент", Kind: metadata.KindCatalog,
		Fields: []metadata.Field{{Name: "Наименование", Type: metadata.FieldTypeString}},
		Forms:  []*metadata.FormModule{form},
	}
	// Локализованный шаблон: без бандла функция t отдаёт ключ как есть, и
	// английская ветка проверки была бы бессмысленной.
	bundle, err := i18n.Load(i18n.EmbeddedLocales, "")
	if err != nil {
		t.Fatalf("load i18n bundle: %v", err)
	}
	localized, err := newTemplate(bundle)
	if err != nil {
		t.Fatalf("build localized template: %v", err)
	}

	render := func(lang string) string {
		t.Helper()
		data := map[string]any{
			"Entity": ent, "Form": form, "IsNew": true, "CanWrite": true,
			"Values":       map[string]string{"Наименование": ""},
			"RefOptions":   map[string]any{},
			"EnumOptions":  map[string]any{},
			"TPRefOptions": map[string]any{},
			"User":         nil,
			"Lang":         lang,
		}
		var buf bytes.Buffer
		if err := localized.ExecuteTemplate(&buf, "page-managed-form", data); err != nil {
			t.Fatalf("ExecuteTemplate(%s): %v", lang, err)
		}
		return buf.String()
	}

	ru := render("ru")
	for _, want := range []string{"navigationFormChanged", "navigationDirty"} {
		if !strings.Contains(ru, want) {
			t.Errorf("в конфиге формы нет ключа %q — текст остался в managed.js", want)
		}
	}
	for _, want := range []string{
		"Форма изменилась во время выполнения команды",
		"Форма содержит несохранённые изменения",
	} {
		if !strings.Contains(ru, want) {
			t.Errorf("русский текст %q не попал в конфиг формы", want)
		}
	}

	en := render("en")
	for _, want := range []string{
		"The form changed while the command was running",
		"The form has unsaved changes",
	} {
		if !strings.Contains(en, want) {
			t.Errorf("английский текст %q не попал в конфиг формы", want)
		}
	}
	// Главное: под английским языком русского текста этих сообщений быть не должно.
	for _, unwanted := range []string{
		"Форма изменилась во время выполнения команды",
		"Форма содержит несохранённые изменения",
	} {
		if strings.Contains(en, unwanted) {
			t.Errorf("под en в конфиге остался русский текст %q", unwanted)
		}
	}
}

// Фолбэк в managed.js обязан остаться: при пустом или отсутствующем ключе
// пользователь должен увидеть сообщение, а не пустую строку.
func TestManagedJSKeepsNavigationFallback(t *testing.T) {
	js := string(managedJS)
	for _, want := range []string{
		"closeMessage('navigationFormChanged', 'Форма изменилась во время выполнения команды",
		"closeMessage('navigationDirty', 'Форма содержит несохранённые изменения",
	} {
		if !strings.Contains(js, want) {
			t.Errorf("в managed.js нет вызова с фолбэком: %q", want)
		}
	}
}
