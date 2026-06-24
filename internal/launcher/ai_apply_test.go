package launcher

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
)

func TestSafeConfigPath_Valid(t *testing.T) {
	for _, ok := range []string{
		"catalogs/клиент.yaml",
		"documents/заявка.yaml",
		"registers/продажи.yaml",
		"inforegs/курс.yaml",
		"enums/статус.yaml",
		"accounts/основной.yaml",
		"accountregs/хозрасчётный.yaml",
	} {
		if err := safeConfigPath(ok); err != nil {
			t.Errorf("ожидался валидный путь %q, получена ошибка: %v", ok, err)
		}
	}
}

func TestSafeConfigPath_Rejects(t *testing.T) {
	for _, bad := range []string{
		"",                      // пустой
		"клиент.yaml",           // нет подкаталога
		"src/модуль.os",         // не-whitelist подкаталог
		"secrets/x.yaml",        // не-whitelist подкаталог
		"catalogs/../evil.yaml", // обход через ..
		"../catalogs/x.yaml",    // обход через ..
		"/catalogs/x.yaml",      // абсолютный
		"catalogs/nul.yaml",     // зарезервированное имя Windows
		"catalogs/CON.yaml",     // зарезервированное имя Windows (регистр)
		"catalogs/com1.yaml",    // зарезервированное имя Windows
		"catalogs/x.txt",        // не .yaml
		"catalogs/sub/x.yaml",   // вложенность (>2 сегментов)
		"catalogs/",             // пустое имя файла
		"catalogs/a:b.yaml",     // недопустимый символ Windows
	} {
		if err := safeConfigPath(bad); err == nil {
			t.Errorf("ожидалась ошибка для пути %q", bad)
		}
	}
}

func TestWriteConfigFileRaw_File(t *testing.T) {
	h := &handler{}
	b := &Base{ConfigSource: "file", Path: t.TempDir()}
	ctx := context.Background()
	content := []byte("name: Клиент\nfields:\n  - {name: Наименование, type: string}\n")
	want := "name: Клиент\nfields:\n  - name: Наименование\n    type: string\n"

	// подкаталог ещё не существует — writer обязан его создать
	if err := h.writeConfigFileRaw(ctx, b, "catalogs/клиент.yaml", content); err != nil {
		t.Fatalf("writeConfigFileRaw: %v", err)
	}
	got, err := os.ReadFile(filepath.Join(b.Path, "catalogs", "клиент.yaml"))
	if err != nil {
		t.Fatalf("файл не создан под b.Path: %v", err)
	}
	if string(got) != want {
		t.Errorf("содержимое не совпало: %q", got)
	}

	// и должен читаться обратно симметричным readConfigFileRaw
	raw, ok := h.readConfigFileRaw(ctx, b, "catalogs/клиент.yaml")
	if !ok || string(raw) != want {
		t.Errorf("readConfigFileRaw вернул ok=%v raw=%q", ok, raw)
	}
}

// newFileBaseHandler возвращает handler с одной file-backed базой (id "test")
// и путь к её каталогу конфигурации.
func newFileBaseHandler(t *testing.T) (*handler, string) {
	t.Helper()
	cfgDir := t.TempDir()
	st := &Store{path: filepath.Join(t.TempDir(), "ibases.yaml")}
	if err := st.Add(&Base{ID: "test", ConfigSource: "file", Path: cfgDir}); err != nil {
		t.Fatalf("store.Add: %v", err)
	}
	return &handler{store: st}, cfgDir
}

// applyReq вызывает cfgAIApply с телом body для базы id и возвращает ответ.
func applyReq(t *testing.T, h *handler, id, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest("POST", "/bases/"+id+"/configurator/ai-apply", strings.NewReader(body))
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("id", id)
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))
	rec := httptest.NewRecorder()
	h.cfgAIApply(rec, req)
	return rec
}

func TestAIApply_WritesObjects(t *testing.T) {
	h, cfgDir := newFileBaseHandler(t)
	rec := applyReq(t, h, "test",
		`{"changes":[{"path":"catalogs/клиент.yaml","kind":"новый","newContent":"name: Клиент\n"}]}`)

	if rec.Code != 200 {
		t.Fatalf("код ответа %d", rec.Code)
	}
	var resp struct {
		OK      bool   `json:"ok"`
		Applied int    `json:"applied"`
		Error   string `json:"error"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("ответ не JSON: %v (%s)", err, rec.Body.String())
	}
	if !resp.OK || resp.Applied != 1 {
		t.Fatalf("ожидалось ok+applied=1, получено: %+v", resp)
	}
	got, err := os.ReadFile(filepath.Join(cfgDir, "catalogs", "клиент.yaml"))
	if err != nil || string(got) != "name: Клиент\n" {
		t.Fatalf("файл не записан верно: %q err=%v", got, err)
	}
}

func TestAIApply_RejectsBadPath(t *testing.T) {
	h, cfgDir := newFileBaseHandler(t)
	rec := applyReq(t, h, "test",
		`{"changes":[{"path":"../evil.yaml","newContent":"x"}]}`)

	var resp struct {
		OK    bool   `json:"ok"`
		Error string `json:"error"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("ответ не JSON: %v", err)
	}
	if resp.OK || resp.Error == "" {
		t.Fatalf("ожидалась ошибка для небезопасного пути, получено: %+v", resp)
	}
	// ничего не должно быть записано
	if entries, _ := os.ReadDir(cfgDir); len(entries) != 0 {
		t.Errorf("каталог конфигурации не должен содержать файлов, есть: %v", entries)
	}
}
