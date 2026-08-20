package launcher

import (
	"fmt"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/ivantit66/onebase/internal/selfupdate"
	"github.com/ivantit66/onebase/internal/version"
)

// stubUpdateTarget подменяет исход проверки каталога установки: воспроизводить
// настоящую общую установку в тесте нечем — каталог теста лежит в профиле.
func stubUpdateTarget(t *testing.T, err error) {
	t.Helper()
	old := validateUpdateTarget
	validateUpdateTarget = func(string) error { return err }
	t.Cleanup(func() { validateUpdateTarget = old })
}

func updatesPageHTML(t *testing.T) string {
	t.Helper()
	w := httptest.NewRecorder()
	(&handler{}).updatesPage(w, httptest.NewRequest("GET", "/updates", nil))
	if w.Code != 200 {
		t.Fatalf("код %d, тело %s", w.Code, w.Body.String())
	}
	return w.Body.String()
}

func setVersion(t *testing.T, v string) {
	t.Helper()
	old := version.Build
	version.Build = v
	t.Cleanup(func() { version.Build = old })
}

// Установка вне личного каталога отказывает не по правам, и звать
// администратора бесполезно: правило смотрит на расположение каталога, а под
// администратором профиль вообще другой. Прежний текст был один на оба отказа
// и посылал пользователя делать заведомо ненужное.
func TestUpdatesPage_SharedInstallDoesNotBlamePermissions(t *testing.T) {
	dir := isolatedUpdatableInstall(t)
	stubUpdateTarget(t, fmt.Errorf("%w: %s", selfupdate.ErrTargetShared, dir))

	page := updatesPageHTML(t)
	if !strings.Contains(page, "вне личного каталога пользователя") {
		t.Error("страница не называет настоящую причину отказа")
	}
	if strings.Contains(page, "только администратору") {
		t.Error("общей установке советуют администратора, хотя права здесь ни при чём")
	}
	if !strings.Contains(page, "переустановите платформу в каталог своего профиля") {
		t.Error("нет выполнимого совета: обновить вручную или переставить установку")
	}
	if !strings.Contains(page, dir) {
		t.Error("в объяснении должен быть путь установки")
	}
	if strings.Contains(page, `return updApply()`) {
		t.Error("кнопки применения на неподдерживаемой установке быть не должно")
	}
}

// Настоящее отсутствие прав по-прежнему формулируется как отсутствие прав.
func TestUpdatesPage_NotWritableKeepsPermissionWording(t *testing.T) {
	dir := isolatedUpdatableInstall(t)
	stubUpdateTarget(t, fmt.Errorf("%w: %s", selfupdate.ErrTargetNotWritable, dir))

	page := updatesPageHTML(t)
	if !strings.Contains(page, "Нет прав на запись") {
		t.Error("отказ по правам должен называться отказом по правам")
	}
	if strings.Contains(page, "вне личного каталога пользователя") {
		t.Error("причина подменена: права перепутаны с расположением каталога")
	}
}

// Версия, которую не с чем сравнить (сборка разработчика, ярлык вроде
// build-793fix из реального state.json), никогда не получит предложения
// обновиться. Говорить ей «установлена актуальная версия» — врать: свежий
// выпуск может быть намного новее.
func TestUpdatesPage_UnrecognizedVersionIsNotCalledUpToDate(t *testing.T) {
	isolatedUpdatableInstall(t)
	stubUpdateTarget(t, nil)
	setVersion(t, "build-793fix")
	if err := selfupdate.SaveState(selfupdate.State{
		Channel: selfupdate.ChannelBuild,
		Latest:  &selfupdate.RelInfo{Tag: "build-930"},
	}); err != nil {
		t.Fatal(err)
	}

	page := updatesPageHTML(t)
	if strings.Contains(page, "Установлена актуальная версия") {
		t.Error("нераспознанная версия выдана за актуальную")
	}
	if !strings.Contains(page, "не сопоставляется с выпусками") {
		t.Error("страница не объясняет, почему обновление не предлагается")
	}
	if !strings.Contains(page, "build-930") {
		t.Error("последний выпуск канала всё равно надо показать")
	}
}

// Обычная сборка на последней версии — прежняя формулировка сохраняется.
func TestUpdatesPage_KnownVersionUpToDate(t *testing.T) {
	isolatedUpdatableInstall(t)
	stubUpdateTarget(t, nil)
	setVersion(t, "build-930")
	if err := selfupdate.SaveState(selfupdate.State{
		Channel: selfupdate.ChannelBuild,
		Latest:  &selfupdate.RelInfo{Tag: "build-930"},
	}); err != nil {
		t.Fatal(err)
	}

	page := updatesPageHTML(t)
	if !strings.Contains(page, "Установлена актуальная версия") {
		t.Error("на актуальной версии формулировка должна остаться прежней")
	}
	if strings.Contains(page, "не сопоставляется с выпусками") {
		t.Error("нормальной версии показали предупреждение о несравнимости")
	}
}
