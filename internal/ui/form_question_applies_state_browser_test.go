package ui

import (
	"bytes"
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

	"github.com/go-chi/chi/v5"

	"github.com/ivantit66/onebase/internal/auth"
	"github.com/ivantit66/onebase/internal/dsl/interpreter"
	"github.com/ivantit66/onebase/internal/dsl/loader"
	"github.com/ivantit66/onebase/internal/metadata"
	orun "github.com/ivantit66/onebase/internal/runtime"
	"github.com/ivantit66/onebase/internal/storage"
)

// Ответ события с вопросом (ПоказатьВопрос в ПриОткрытии — «Принять
// задачу?») применяется целиком ДО открытия вопроса: обработчик уже скрыл
// кнопку (hidden_when по реквизиту формы) и заполнил реквизит, как в 1С.
// Раньше браузер открывал вопрос и выбрасывал остальной ответ: на форме
// оставалось видно скрытое, а ответ на вопрос уходил со снимком без этих
// значений и терял их. Форма — из YAML и .form.os штатным маршрутом,
// поведение — в браузере с managed.js.
func TestQuestionResponseAppliesStateAndValues_Browser(t *testing.T) {
	browser := managedLayoutTestBrowser(t)
	db, err := storage.ConnectSQLite(t.Context(), filepath.Join(t.TempDir(), "q.db"))
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	yaml := "schema: onebase.form/v1\nform:\n  name: ФормаОбъекта\n  kind: object\n  entity: Задача\n" +
		"events:\n  ПриОткрытии: ПриОткрытии\n  Ответ: ОтветНаВопрос\n" +
		"attributes:\n  - name: Объект\n    type: CatalogRef.Задача\n    main: true\n    save: true\n  - name: СкрытьПродлить\n    type: boolean\n" +
		"elements:\n  - kind: Кнопка\n    name: КнПродлить\n    hidden_when: СкрытьПродлить\n    title:\n      ru: Продлить\n" +
		"  - kind: ПолеВвода\n    name: ПолеНаим\n    data_path: Объект.Наименование\n"
	if err := os.WriteFile(filepath.Join(dir, "ФормаОбъекта.form.yaml"), []byte(yaml), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "ФормаОбъекта.form.os"), []byte(`
Процедура ПриОткрытии()
	Объект.СкрытьПродлить = Истина;
	Объект.Наименование = "из ПриОткрытии";
	ПоказатьВопрос("Принять задачу?", "ДаНет");
КонецПроцедуры

Процедура ОтветНаВопрос()
	Сообщить("Ответ: " + ВопросОтвет);
КонецПроцедуры
`), 0o600); err != nil {
		t.Fatal(err)
	}
	fm, err := loader.NewManagedFormLoader().LoadFormFile(filepath.Join(dir, "ФормаОбъекта.form.yaml"), "Задача")
	if err != nil {
		t.Fatal(err)
	}
	ent := &metadata.Entity{Name: "Задача", Kind: metadata.KindCatalog, Fields: []metadata.Field{{Name: "Наименование", Type: metadata.FieldTypeString}}, Forms: []*metadata.FormModule{fm}}
	if err := db.Migrate(t.Context(), []*metadata.Entity{ent}); err != nil {
		t.Fatal(err)
	}
	reg := orun.NewRegistry()
	reg.Load(orun.LoadOptions{Entities: []*metadata.Entity{ent}})
	interp := interpreter.New()
	interp.LookupProc = reg.GetModuleProc
	srv := New(reg, db, interp, auth.NewRepo(db), Config{}, nil)
	defer func() { srv.BeginShutdown(); db.Close() }()
	router := chi.NewRouter()
	srv.MountStatic(router)
	srv.Mount(router)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, httptest.NewRequest("GET", "/ui/catalog/задача/new", nil))
	page := rec.Body.String()
	script := `<script>
var st=function(){var b=document.querySelector('[data-ob-el="КнПродлить"]');var i=document.querySelector('[name="Наименование"]');return {btn:b?getComputedStyle(b).display:'-',val:i?i.value:'-',q:!!document.evaluate("//button[text()='Да']",document,null,9,null).singleNodeValue};};
var out={answerBody:''};var of=window.fetch;window.fetch=function(u,o){if(String(u).indexOf('form-event')>=0&&o&&o.body&&String(o.body.get?o.body.get('_event'):'')==='Ответ'){out.answerBody=JSON.stringify({skr:o.body.get('СкрытьПродлить'),name:o.body.get('Наименование')});}return of.apply(this,arguments);};
setTimeout(function(){out.afterOpen=st();var yes=document.evaluate("//button[text()='Да']",document,null,9,null).singleNodeValue;if(yes)yes.click();
setTimeout(function(){out.afterAnswer=st();var p=document.createElement('pre');p.id='probe';p.textContent=JSON.stringify(out);document.body.appendChild(p);},1500);},1500);
</script>`
	i := strings.LastIndex(page, "</body>")
	page = page[:i] + script + page[i:]
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/":
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			_, _ = w.Write([]byte(page))
		case strings.HasPrefix(r.URL.Path, "/static/"), strings.HasPrefix(r.URL.Path, "/vendor/"), strings.HasSuffix(r.URL.Path, "/form-event"):
			router.ServeHTTP(w, r)
		default:
			http.NotFound(w, r)
		}
	}))
	defer ts.Close()
	args := []string{"--headless=new", "--disable-gpu", "--no-first-run", "--window-size=1440,784", "--virtual-time-budget=6000", "--user-data-dir=" + t.TempDir(), "--dump-dom", ts.URL}
	if runtime.GOOS == "linux" {
		args = append([]string{"--no-sandbox"}, args...)
	}
	out, err := exec.CommandContext(t.Context(), browser, args...).Output() //nolint:gosec // browser selected from existing test allow-list
	if err != nil {
		t.Fatal(err)
	}
	j := bytes.Index(out, []byte(`<pre id="probe">`))
	if j < 0 {
		t.Fatalf("no probe")
	}
	k := bytes.Index(out[j:], []byte("</pre>"))
	var r struct {
		AfterOpen, AfterAnswer struct {
			Btn, Val string
			Q        bool
		}
		AnswerBody string
	}
	if err := json.Unmarshal([]byte(html.UnescapeString(string(out[j+16:j+k]))), &r); err != nil {
		t.Fatal(err)
	}
	if !r.AfterOpen.Q {
		t.Fatalf("вопрос из ПриОткрытии не открылся: %+v", r)
	}
	if r.AfterOpen.Btn != "none" || r.AfterOpen.Val != "из ПриОткрытии" {
		t.Errorf("ответ с вопросом не применён: кнопка display=%q, реквизит=%q", r.AfterOpen.Btn, r.AfterOpen.Val)
	}
	if !strings.Contains(r.AnswerBody, `"skr":"true"`) || !strings.Contains(r.AnswerBody, `"name":"из ПриОткрытии"`) {
		t.Errorf("ответ на вопрос ушёл без реквизитов из ПриОткрытии: %s", r.AnswerBody)
	}
	if r.AfterAnswer.Btn != "none" || r.AfterAnswer.Val != "из ПриОткрытии" || r.AfterAnswer.Q {
		t.Errorf("после ответа: кнопка display=%q, реквизит=%q, вопрос открыт=%v", r.AfterAnswer.Btn, r.AfterAnswer.Val, r.AfterAnswer.Q)
	}

}
