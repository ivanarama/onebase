package launcher

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/ivantit66/onebase/internal/dsl/loader"
	"github.com/ivantit66/onebase/internal/metadata"
)

// Реальный редактор: выбор на холсте -> панель -> HTTP edit-op -> сохранение
// YAML и модуля -> повторное открытие. Проверка одного applicableEvents не
// обнаружила бы потерю привязки между панелью, сервером и файлом (#2194).
func TestFormsPagesEvents_BrowserRoundTrip(t *testing.T) {
	browser := designerLayoutTestBrowser(t)
	h, configDir := newFileBaseHandler(t)
	formDir := filepath.Join(configDir, "forms", "заказ")
	if err := os.MkdirAll(formDir, 0o755); err != nil {
		t.Fatal(err)
	}
	yamlPath := filepath.Join(formDir, "main.form.yaml")
	const sample = `schema: onebase.form/v1
form:
  name: main
  kind: object
  entity: Заказ
elements:
  - kind: СтраницыФормы
    name: Закладки
    children:
      - kind: Страница
        name: Первая
        title: {ru: Первая}
`
	for path, body := range map[string]string{
		yamlPath:                               sample,
		filepath.Join(formDir, "main.form.os"): "Процедура Существующий()\nКонецПроцедуры\n",
	} {
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	result := make(chan string, 1)
	router := chi.NewRouter()
	router.Get("/bases/{id}/configurator/forms/edit", func(w http.ResponseWriter, r *http.Request) {
		rec := httptest.NewRecorder()
		h.configuratorFormsEdit(rec, r)
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.WriteHeader(rec.Code)
		_, _ = w.Write([]byte(strings.Replace(rec.Body.String(), "</body>", pagesEventsBrowserDriver+"</body>", 1)))
	})
	router.Post("/bases/{id}/configurator/forms/edit-op", h.configuratorFormsEditOp)
	router.Post("/bases/{id}/configurator/forms/save", h.configuratorFormsSave)
	router.Post("/test-result", func(w http.ResponseWriter, r *http.Request) {
		var got string
		if err := json.NewDecoder(r.Body).Decode(&got); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		select {
		case result <- got:
		default:
		}
		w.WriteHeader(http.StatusNoContent)
	})
	server := httptest.NewServer(router)
	t.Cleanup(server.Close)
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()
	args := []string{"--headless=new", "--disable-gpu", "--disable-background-networking", "--disable-component-update", "--disable-extensions", "--disable-sync", "--no-first-run", "--no-default-browser-check", "--user-data-dir=" + t.TempDir(), server.URL + "/bases/test/configurator/forms/edit?entity=Заказ&name=main"}
	if runtime.GOOS == "linux" {
		args = append([]string{"--no-sandbox"}, args...)
	}
	cmd := exec.CommandContext(ctx, browser, args...) //nolint:gosec // test-only browser из закрытого allow-list
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	exited := make(chan struct{})
	var waitErr error
	go func() { waitErr = cmd.Wait(); close(exited) }()
	defer func() { cancel(); <-exited }()
	select {
	case got := <-result:
		if got != "ok" {
			t.Fatal(got)
		}
	case <-ctx.Done():
		cancel()
		<-exited
		t.Fatalf("браузер не завершил round-trip: %v\n%s", ctx.Err(), stderr.String())
	case <-exited:
		t.Fatalf("браузер завершился до round-trip: %v\n%s", waitErr, stderr.String())
	}
	fm, err := loader.NewManagedFormLoader().LoadFormFile(yamlPath, "Заказ")
	if err != nil {
		t.Fatal(err)
	}
	if len(fm.Elements) != 1 || fm.Elements[0].Handlers[metadata.FormEventOnPageChange] != "НовыйОбработчик" {
		t.Fatalf("привязка не сохранилась: %+v", fm.Elements)
	}
	assertFileContainsRv(t, filepath.Join(formDir, "main.form.os"), "Процедура Существующий()", "Процедура НовыйОбработчик()")
}

const pagesEventsBrowserDriver = `<script>
window.addEventListener('load', async function () {
  const check = (ok, message) => { if (!ok) throw new Error(message); };
  const wait = async (predicate) => {
    for (let i = 0; i < 100; i++) {
      if (predicate()) return;
      await new Promise(resolve => setTimeout(resolve, 20));
    }
    throw new Error('Таймаут обновления панели');
  };
  const eventSelect = () => {
    const row = Array.from(document.querySelectorAll('#prop-body .prop-row'))
      .find(row => row.querySelector('label')?.textContent === 'ПриСменеСтраницы');
    return row?.querySelector('select');
  };
  try {
    await wait(() => document.querySelector('#canvas-host [data-node-id="elements.0"]'));
    document.querySelector('#canvas-host [data-node-id="elements.0.children.0"]').click();
    check(!eventSelect(), 'Событие предложено отдельной Странице');
    document.querySelector('#canvas-host [data-node-id="elements.0"]').click();
    check(eventSelect(), 'В панели набора страниц нет ПриСменеСтраницы');
    const stage = sessionStorage.getItem('pages-event-stage') || 'choose';
    if (stage === 'choose') {
      check(Array.from(eventSelect().options).some(o => o.value === 'Существующий'), 'Нет существующей процедуры');
      eventSelect().value = 'Существующий';
      eventSelect().dispatchEvent(new Event('change', {bubbles:true}));
      await wait(() => eventSelect()?.value === 'Существующий' && getYAML().includes('ПриСменеСтраницы: Существующий'));
      sessionStorage.setItem('pages-event-stage', 'create');
    } else if (stage === 'create') {
      check(eventSelect().value === 'Существующий', 'Выбранная процедура потеряна после открытия');
      window.prompt = () => 'НовыйОбработчик';
      eventSelect().value = '@create';
      eventSelect().dispatchEvent(new Event('change', {bubbles:true}));
      await wait(() => eventSelect()?.value === 'НовыйОбработчик' && getYAML().includes('ПриСменеСтраницы: НовыйОбработчик'));
      check(getOS().includes('Процедура НовыйОбработчик()'), 'Процедура не создана в модуле');
      sessionStorage.setItem('pages-event-stage', 'verify');
    } else {
      check(eventSelect().value === 'НовыйОбработчик', 'Новая привязка потеряна после открытия');
      check(getOS().includes('Процедура НовыйОбработчик()'), 'Новая процедура потеряна после открытия');
      check(getOS().includes('Процедура Существующий()'), 'Существующая процедура потеряна');
      await fetch('/test-result', {method:'POST', body:JSON.stringify('ok')});
      return;
    }
    document.querySelector('button[onclick="saveForm()"]').click();
  } catch (error) {
    await fetch('/test-result', {method:'POST', body:JSON.stringify(String(error))});
  }
});
</script>`
