package launcher

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// postName шлёт форму сохранения объекта с заданным именем.
func postName(t *testing.T, field, name string, extra url.Values) *http.Request {
	t.Helper()
	form := url.Values{field: {name}}
	for k, v := range extra {
		form[k] = v
	}
	req := httptest.NewRequest(http.MethodPost, "/bases/b/configurator/save",
		strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	return requestWithBaseID(req, "b")
}

// Имя объекта из формы попадает в путь файла конфигурации, а nameToFilename
// только приводит его к нижнему регистру — разделители пути он не вычищает.
// Пять соседних обработчиков (модуль, обработка, страница, журнал, общий
// модуль) проверяют имя через validObjectName; у этих проверки не было, и
// os.WriteFile создавал файл за пределами каталога проекта — цели даже не
// требовалось существовать.
func TestSaveHandlersRejectTraversingObjectName(t *testing.T) {
	cases := []struct {
		name    string
		field   string
		extra   url.Values
		invoke  func(h *handler, w http.ResponseWriter, r *http.Request)
		outside string
	}{
		{
			name:  "перечисление",
			field: "enum_name",
			extra: url.Values{"v.0.name": {"Значение"}},
			invoke: func(h *handler, w http.ResponseWriter, r *http.Request) {
				h.configuratorSaveEnum(w, r)
			},
			outside: "снаружи-перечисление",
		},
		{
			name:  "регистр бухгалтерии",
			field: "accountreg",
			extra: url.Values{"title": {"Т"}, "accounts": {"Основной"}},
			invoke: func(h *handler, w http.ResponseWriter, r *http.Request) {
				h.configuratorSaveAccountRegister(w, r)
			},
			outside: "снаружи-регистр",
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			root := t.TempDir()
			cfgDir := filepath.Join(root, "проект")
			if err := os.MkdirAll(cfgDir, 0o700); err != nil {
				t.Fatal(err)
			}
			store := newTestStore(t)
			if err := store.Add(&Base{ID: "b", Name: "b", ConfigSource: "file", Path: cfgDir}); err != nil {
				t.Fatal(err)
			}

			req := postName(t, c.field, "../../"+c.outside, c.extra)
			rec := httptest.NewRecorder()
			c.invoke(&handler{store: store, runner: NewRunner()}, rec, req)

			// Файл не должен появиться нигде за пределами каталога проекта.
			var leaked []string
			_ = filepath.WalkDir(root, func(p string, d os.DirEntry, err error) error {
				if err != nil || d.IsDir() {
					return nil
				}
				if !strings.HasPrefix(p, cfgDir+string(os.PathSeparator)) {
					leaked = append(leaked, p)
				}
				return nil
			})
			if len(leaked) > 0 {
				t.Fatalf("запись ушла за пределы каталога проекта: %v", leaked)
			}
		})
	}
}
