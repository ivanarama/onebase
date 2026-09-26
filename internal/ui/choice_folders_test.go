package ui

import (
	"testing"

	"github.com/ivantit66/onebase/internal/metadata"
)

// choice_folders разрешает выбирать группы иерархического справочника. Без
// него поле, значение которого по смыслу является группой (населённый пункт:
// у города есть улицы), невыбираемо — подбор пуст, а записанное значение
// платформа считает недопустимым и чистит, помечая форму изменённой.
func TestChoiceFoldersKeyReadFromElement(t *testing.T) {
	el := &metadata.FormElement{Name: "ПолеГород", ChoiceFolders: true}
	if !el.ChoiceFolders {
		t.Fatal("ключ choice_folders не прочитан моделью")
	}
	plain := &metadata.FormElement{Name: "ПолеБренд"}
	if plain.ChoiceFolders {
		t.Error("без ключа выбор групп должен оставаться выключенным")
	}
}
