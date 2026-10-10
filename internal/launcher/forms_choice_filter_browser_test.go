package launcher

import (
	"bytes"
	"context"
	"encoding/json"
	"html"
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
)

// Страница и модель загружаются через HTTP, элементы выбираются кликом по
// настоящему холсту. Тест ловит дубли панели, которых не видно в YAML/model.
func TestConfiguratorChoiceFilterEditor_Browser(t *testing.T) {
	browser := designerLayoutTestBrowser(t)
	projectDir := t.TempDir()
	formDir := filepath.Join(projectDir, "forms", "контрагент")
	if err := os.MkdirAll(formDir, 0755); err != nil {
		t.Fatal(err)
	}
	const formYAML = `schema: onebase.form/v1
form:
  name: ФормаОбъекта
  kind: object
  entity: Контрагент
elements:
  - kind: ПолеВвода
    name: Выбор
    id: выбор
    data_path: Объект.Владелец
    input_mask: "00.00.00"
    choice_filter:
      - field: is_folder
        value: false
  - kind: Кнопка
    name: Выполнить
  - kind: Флажок
    name: Активен
    data_path: Объект.Активен
  - kind: ГруппаФормы
    name: Группа
  - kind: ПолеКартинки
    name: Картинка
`
	if err := os.WriteFile(filepath.Join(formDir, "формаобъекта.form.yaml"), []byte(formYAML), 0600); err != nil {
		t.Fatal(err)
	}
	store := &Store{path: filepath.Join(t.TempDir(), "ibases.yaml")}
	base := &Base{Path: projectDir, ConfigSource: "file"}
	if err := store.Add(base); err != nil {
		t.Fatal(err)
	}
	h := &handler{store: store}
	router := chi.NewRouter()
	router.Get("/bases/{id}/configurator/forms/edit", func(w http.ResponseWriter, r *http.Request) {
		rec := httptest.NewRecorder()
		h.configuratorFormsEdit(rec, r)
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.WriteHeader(rec.Code)
		_, _ = w.Write([]byte(strings.Replace(rec.Body.String(), "</body>", choiceFilterPanelBrowserScript+"</body>", 1)))
	})
	router.Post("/bases/{id}/configurator/forms/edit-op", h.configuratorFormsEditOp)
	// Monaco отсутствует: используется штатный textarea-fallback. Подбор и
	// загрузка модели остаются настоящими; ответа edit-op тест не подменяет.
	server := httptest.NewServer(router)
	t.Cleanup(server.Close)

	args := []string{"--headless=new", "--disable-gpu", "--disable-background-networking", "--disable-component-update", "--disable-extensions", "--disable-sync", "--no-first-run", "--no-default-browser-check", "--user-data-dir=" + t.TempDir(), "--virtual-time-budget=5000", "--dump-dom", server.URL + "/bases/" + base.ID + "/configurator/forms/edit?entity=Контрагент&name=ФормаОбъекта&lang=ru"}
	if runtime.GOOS == "linux" {
		args = append([]string{"--no-sandbox"}, args...)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, browser, args...) //nolint:gosec // test-only browser из закрытого allow-list
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	dumped, err := cmd.Output()
	if err != nil {
		t.Fatalf("browser: %v\n%s", err, stderr.String())
	}
	const marker = `<pre id="choice-filter-panel-result">`
	_, result, ok := strings.Cut(string(dumped), marker)
	if !ok {
		t.Fatalf("браузер не проверил панель: %s\n%s", stderr.String(), dumped)
	}
	result, _, ok = strings.Cut(result, "</pre>")
	if !ok {
		t.Fatal("результат браузера не закрыт")
	}
	var rows []struct {
		Node     int      `json:"node"`
		Editors  int      `json:"editors"`
		Fields   []string `json:"fields"`
		ID       string   `json:"id"`
		Mask     string   `json:"mask"`
		Readonly int      `json:"readonly"`
	}
	if err := json.Unmarshal([]byte(html.UnescapeString(result)), &rows); err != nil {
		t.Fatalf("результат браузера: %v: %s", err, result)
	}
	if len(rows) != 6 {
		t.Fatalf("проверено %d выборов вместо 6: %s", len(rows), result)
	}
	for index, row := range rows {
		wantNode := index
		if index == 5 {
			wantNode = 0 // Повторный выбор не накапливает редакторы.
		}
		if row.Node != wantNode {
			t.Fatalf("выбор %d: узел %d вместо %d", index, row.Node, wantNode)
		}
		wantEditors := 0
		if row.Node == 0 {
			wantEditors = 1
			if len(row.Fields) != 1 || row.Fields[0] != "is_folder" || row.ID != "выбор" || row.Mask != "00.00.00" {
				t.Errorf("содержимое подбора/соседние свойства потеряны: %+v", row)
			}
		}
		if row.Editors != wantEditors {
			t.Errorf("узел %d: редакторов choice_filter %d вместо %d", row.Node, row.Editors, wantEditors)
		}
		if row.Node <= 2 && row.Readonly != 1 {
			t.Errorf("узел %d: соседнее свойство readonly потеряно/дублировано: %d", row.Node, row.Readonly)
		}
	}
}

const choiceFilterPanelBrowserScript = `<script>
window.addEventListener('load', function () {
  var timer = setInterval(function () {
    var first = document.querySelector('#canvas-host [data-node-id="elements.0"]');
    if (!first) return;
    clearInterval(timer);
    var rows = [0, 1, 2, 3, 4, 0].map(function (node) {
      document.querySelector('#canvas-host [data-node-id="elements.' + node + '"]').click();
      var panel = document.getElementById('prop-body');
      var labels = Array.from(panel.querySelectorAll('.prop-row > label'));
      function inputValue(label) {
        var found = labels.find(function (el) { return el.textContent === label; });
        var input = found && found.parentElement.querySelector('input');
        return input ? input.value : '';
      }
      return {
        node: node,
        editors: Array.from(panel.querySelectorAll('.prop-section')).filter(function (el) { return el.textContent === 'Зависимый подбор'; }).length,
        fields: Array.from(panel.querySelectorAll('.prop-choice-card')).map(function (el) { return el.querySelector('input').value; }),
        id: inputValue('Устойчивый id элемента'),
        mask: inputValue('Шаблон ввода (00.00.00)'),
        readonly: labels.filter(function (el) { return el.textContent === 'Только чтение'; }).length
      };
    });
    var out = document.createElement('pre');
    out.id = 'choice-filter-panel-result';
    out.textContent = JSON.stringify(rows);
    document.body.appendChild(out);
  }, 10);
});
</script>`
