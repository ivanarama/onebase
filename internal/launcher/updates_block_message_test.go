package launcher

// Выбор текста отказа по классу причины (#1065). Переносимо: класс приходит
// из selfupdate, а здесь проверяется только то, что каждому классу достаётся
// свой текст — и что советы в них разные.

import (
	"strings"
	"testing"

	"github.com/ivantit66/onebase/internal/selfupdate"
)

func TestBlockMessage_TextDiffersByReason(t *testing.T) {
	perm := updatesVM{Block: selfupdate.TargetBlockNotWritable}.blockMessage("ru")
	if !strings.Contains(perm, "Нет прав на запись") {
		t.Errorf("отказ по правам: %q", perm)
	}
	if !strings.Contains(perm, "администратору") {
		t.Errorf("совет обратиться к администратору уместен именно здесь: %q", perm)
	}

	loc := updatesVM{Block: selfupdate.TargetBlockNotPrivate}.blockMessage("ru")
	if strings.Contains(loc, "Нет прав на запись") {
		t.Errorf("расположение установки выдано за отказ по правам: %q", loc)
	}
	if !strings.Contains(loc, "вне личного каталога пользователя") {
		t.Errorf("причина не названа: %q", loc)
	}
	if !strings.Contains(loc, "администратора не поможет") {
		t.Errorf("ложная догадка про администратора не снята: %q", loc)
	}

	other := updatesVM{Block: selfupdate.TargetBlockOther, BlockDetail: "каталог исчез"}.blockMessage("ru")
	if !strings.Contains(other, "каталог исчез") {
		t.Errorf("техническая причина потеряна: %q", other)
	}
}
