//go:build !windows

package launcher

// Ответ на нажатую кнопку обновления должен называть настоящую причину отказа
// (#1065): «нет прав» и «установка вне личного каталога» — разные препятствия
// с разными выходами, а откат вдобавок ссылался на политику, которой нет.
//
// Только не-Windows: общая установка воспроизводится правами каталога. На
// Windows границей служит личный каталог из FOLDERID_Profile (его подменой
// переменной окружения не сдвинуть — это и есть смысл проверки), поэтому там
// тот же класс отказа проверяется в internal/selfupdate, а выбор текста по
// классу — переносимым TestBlockMessage ниже и тестами рендера страницы.

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
)

// sharedInstallDir — каталог установки, куда писать можно, но самообновление
// небезопасно: он общий. Ровно случай `C:\Projects\onebase` из заявки.
func sharedInstallDir(t *testing.T) string {
	t.Helper()
	isolatedUpdatesHome(t)
	dir, err := os.MkdirTemp(os.TempDir(), "onebase-shared-install-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	if err := os.Chmod(dir, 0o755); err != nil { //nolint:gosec // G302: общая установка воспроизводится намеренно
		t.Fatal(err)
	}
	old := updateBinaryDir
	updateBinaryDir = func() (string, error) { return dir, nil }
	t.Cleanup(func() { updateBinaryDir = old })
	return dir
}

func updateErrorText(t *testing.T, rec *httptest.ResponseRecorder) string {
	t.Helper()
	var body map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("ответ не JSON: %v (%s)", err, rec.Body.String())
	}
	text, _ := body["error"].(string)
	return text
}

func TestUpdatesApply_SharedInstallExplainsLocationNotPermissions(t *testing.T) {
	sharedInstallDir(t)
	h := &handler{store: newTestStore(t), runner: NewRunner()}

	rec := httptest.NewRecorder()
	h.updatesApply(rec, httptest.NewRequest(http.MethodPost, "/updates/apply", nil))

	if rec.Code != http.StatusForbidden {
		t.Fatalf("код ответа = %d, ожидался 403 (%s)", rec.Code, rec.Body.String())
	}
	text := updateErrorText(t, rec)
	if strings.Contains(text, "Нет прав на запись") {
		t.Errorf("общая установка выдана за отказ по правам: %s", text)
	}
	if !strings.Contains(text, "вне личного каталога пользователя") {
		t.Errorf("причина не названа: %s", text)
	}
	if !strings.Contains(text, "администратора не поможет") {
		t.Errorf("ответ не снимает ложную догадку про администратора: %s", text)
	}
}

// Откат отвечал «Обновление платформы запрещено политикой» — про политику,
// которой нет: причина та же самая, что у обновления.
func TestUpdatesRollback_SharedInstallDoesNotBlamePolicy(t *testing.T) {
	sharedInstallDir(t)
	h := &handler{store: newTestStore(t), runner: NewRunner()}

	rec := httptest.NewRecorder()
	h.updatesRollback(rec, httptest.NewRequest(http.MethodPost, "/updates/rollback", nil))

	if rec.Code != http.StatusForbidden {
		t.Fatalf("код ответа = %d, ожидался 403 (%s)", rec.Code, rec.Body.String())
	}
	text := updateErrorText(t, rec)
	if strings.Contains(text, "запрещено политикой") {
		t.Errorf("отказ по расположению установки назван политикой: %s", text)
	}
	if !strings.Contains(text, "вне личного каталога пользователя") {
		t.Errorf("причина не названа: %s", text)
	}
}
