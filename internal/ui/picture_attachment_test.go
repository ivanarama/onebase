package ui

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"golang.org/x/net/html"

	"github.com/ivantit66/onebase/internal/i18n"
	"github.com/ivantit66/onebase/internal/metadata"
)

// ПолеКартинки с data_path показывает вложение, идентификатор которого лежит в
// реквизите (фото мастера в заявке), и открывает его в полном размере по
// щелчку — как «Поле картинки … Нажатие» в 1С. Публичный путь — GET формы
// объекта. Значение, не являющееся UUID, в src/href не попадает.
func TestManagedFormPictureFieldShowsAttachment(t *testing.T) {
	srv, ent := setupManagedEventsServer(t, ``, nil, []*metadata.FormElement{
		{Kind: metadata.FormElementPicture, Name: "ПолеФото", DataPath: "Объект.Наименование", Width: 240, Height: 180},
	})
	open := func(t *testing.T, value string) string {
		t.Helper()
		id := uuid.New()
		if err := srv.store.Upsert(context.Background(), ent.Name, id,
			map[string]any{"Наименование": value}, ent); err != nil {
			t.Fatal(err)
		}
		req := httptest.NewRequest(http.MethodGet, "/ui/catalog/"+ent.Name+"/"+id.String(), nil)
		rctx := chi.NewRouteContext()
		rctx.URLParams.Add("kind", "catalog")
		rctx.URLParams.Add("entity", ent.Name)
		rctx.URLParams.Add("id", id.String())
		req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))
		rec := httptest.NewRecorder()
		srv.formEdit(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("GET формы: %d %s", rec.Code, rec.Body.String())
		}
		return rec.Body.String()
	}

	t.Run("идентификатор вложения — картинка и ссылка", func(t *testing.T) {
		aid := uuid.New().String()
		stored := strings.ToUpper(aid)
		body := open(t, stored)
		url := "/ui/attachments/" + aid + "/download"
		for _, want := range []string{
			`<img src="` + url + `"`,
			`data-ob-picture-link="ПолеФото" href="` + url + `" target="_blank" rel="noopener"`,
			`max-width:240px;max-height:180px`,
			// Скрытое поле несёт значение как есть — нормализуется только адрес.
			`name="Наименование" value="` + stored + `" data-ob-picture-for="ПолеФото" data-ob-picture-field="Наименование"`,
			`data-ob-picture-empty="ПолеФото" style="color:#94a3b8" hidden>`,
		} {
			if !strings.Contains(body, want) {
				t.Errorf("в разметке нет %s", want)
			}
		}
	})

	t.Run("не идентификатор — пусто, адрес не подставлен", func(t *testing.T) {
		body := open(t, "javascript:alert(1)")
		// Наименование записи есть и в заголовке формы — смотрим только блок картинки.
		start := strings.Index(body, `data-ob-picture-link="ПолеФото"`)
		end := strings.Index(body, `data-ob-picture-empty="ПолеФото"`)
		if start < 0 || end < start {
			t.Fatalf("нет блока картинки")
		}
		if strings.Contains(body[start:end], "javascript:alert") {
			t.Fatalf("значение реквизита попало в src/href картинки")
		}
		for _, want := range []string{
			// Значение реквизита сохраняется как есть (экранированным) — для
			// отправки формы, не для адреса.
			`value="javascript:alert(1)" data-ob-picture-for="ПолеФото"`,
			`data-ob-picture-link="ПолеФото" href="" target="_blank" rel="noopener" title="Открыть в полном размере" hidden>`,
			`data-ob-picture-empty="ПолеФото" style="color:#94a3b8">Нет изображения`,
		} {
			if !strings.Contains(body, want) {
				t.Errorf("в разметке нет %s", want)
			}
		}
	})
}

// pictureFormControls — то, что отправит браузер: именованные контролы формы
// записи (#main-form) в порядке разметки, без disabled и неотмеченных флажков.
func pictureFormControls(t *testing.T, rendered string) url.Values {
	t.Helper()
	doc, err := html.Parse(strings.NewReader(rendered))
	if err != nil {
		t.Fatal(err)
	}
	var form *html.Node
	var find func(*html.Node)
	find = func(n *html.Node) {
		if form != nil {
			return
		}
		if n.Type == html.ElementNode && n.Data == "form" {
			if id, _ := managedHTMLAttr(n, "id"); id == "main-form" {
				form = n
				return
			}
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			find(c)
		}
	}
	find(doc)
	if form == nil {
		t.Fatal("нет формы #main-form")
	}
	values := url.Values{}
	var walk func(*html.Node)
	walk = func(n *html.Node) {
		if n.Type == html.ElementNode && (n.Data == "input" || n.Data == "textarea" || n.Data == "select") {
			name, _ := managedHTMLAttr(n, "name")
			_, disabled := managedHTMLAttr(n, "disabled")
			typ, _ := managedHTMLAttr(n, "type")
			_, checked := managedHTMLAttr(n, "checked")
			toggle := typ == "checkbox" || typ == "radio"
			if name != "" && !disabled && (!toggle || checked) && typ != "file" {
				value, _ := managedHTMLAttr(n, "value")
				if n.Data == "textarea" && n.FirstChild != nil {
					value = n.FirstChild.Data
				}
				values.Add(name, value)
			}
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	walk(form)
	return values
}

func pictureSaveRoundTrip(t *testing.T, elements []*metadata.FormElement, stored string, edit func(url.Values)) string {
	t.Helper()
	srv, ent := setupManagedEventsServer(t, ``, nil, elements)
	id := uuid.New()
	if err := srv.store.Upsert(context.Background(), ent.Name, id, map[string]any{"Наименование": stored}, ent); err != nil {
		t.Fatal(err)
	}
	params := map[string]string{"kind": "catalog", "entity": ent.Name, "id": id.String()}
	get := httptest.NewRecorder()
	srv.formEdit(get, reqWithChi(http.MethodGet, "/ui/catalog/"+ent.Name+"/"+id.String(), nil, params))
	if get.Code != http.StatusOK {
		t.Fatalf("GET формы: %d", get.Code)
	}
	body := pictureFormControls(t, get.Body.String())
	if edit != nil {
		edit(body)
	}
	post := httptest.NewRecorder()
	srv.submitEdit(post, reqWithChi(http.MethodPost, "/ui/catalog/"+ent.Name+"/"+id.String(), body, params))
	if post.Code != http.StatusSeeOther {
		t.Fatalf("запись: %d %s", post.Code, post.Body.String())
	}
	row, err := srv.store.GetByID(context.Background(), ent.Name, id, ent)
	if err != nil {
		t.Fatal(err)
	}
	return fmt.Sprint(row["Наименование"])
}

// Ревью #2177, п. 1: ПолеКартинки стоит перед полем ввода того же реквизита.
// Новое значение из редактора сохраняется — скрытое поле превью его не
// перекрывает (оно не отправляется, когда реквизит редактирует другой элемент).
func TestManagedFormPictureFieldDoesNotShadowEditor(t *testing.T) {
	oldID, newID := uuid.New().String(), uuid.New().String()
	got := pictureSaveRoundTrip(t, []*metadata.FormElement{
		{Kind: metadata.FormElementPicture, Name: "ПолеФото", DataPath: "Объект.Наименование"},
		{Kind: metadata.FormElementField, Name: "ПолеИд", DataPath: "Объект.Наименование"},
	}, oldID, func(body url.Values) {
		if got := body["Наименование"]; len(got) != 1 {
			t.Fatalf("реквизит отправляется %d контролами: %v", len(got), got)
		}
		body.Set("Наименование", newID) // пользователь ввёл новый идентификатор
	})
	if got != newID {
		t.Fatalf("сохранено %q, want новый %q", got, newID)
	}
}

// Ревью #2177, п. 2: значение, не являющееся идентификатором вложения, при
// сохранении без правки остаётся как было — проверка UUID касается только
// адреса картинки, а не отправляемого значения.
func TestManagedFormPictureFieldKeepsNonUUIDValueOnSave(t *testing.T) {
	got := pictureSaveRoundTrip(t, []*metadata.FormElement{
		{Kind: metadata.FormElementPicture, Name: "ПолеФото", DataPath: "Объект.Наименование"},
	}, "legacy-value", nil)
	if got != "legacy-value" {
		t.Fatalf("сохранение без правки изменило значение: %q", got)
	}
}

// Подписи превью переводятся: при английском интерфейсе — английские.
func TestManagedFormPictureFieldEnglishLabels(t *testing.T) {
	srv, ent := setupManagedEventsServer(t, ``, nil, []*metadata.FormElement{
		{Kind: metadata.FormElementPicture, Name: "ПолеФото", DataPath: "Объект.Наименование"},
	})
	bundle, err := i18n.Load(i18n.EmbeddedLocales, "")
	if err != nil {
		t.Fatal(err)
	}
	srv.cfg.Bundle = bundle
	// Переводчик шаблона связывается со словарём при сборке шаблона.
	if srv.tmpl, err = newTemplate(bundle); err != nil {
		t.Fatal(err)
	}
	id := uuid.New()
	if err := srv.store.Upsert(context.Background(), ent.Name, id, map[string]any{"Наименование": "нет"}, ent); err != nil {
		t.Fatal(err)
	}
	req := reqWithChi(http.MethodGet, "/ui/catalog/"+ent.Name+"/"+id.String(), nil,
		map[string]string{"kind": "catalog", "entity": ent.Name, "id": id.String()})
	req.Header.Set("Accept-Language", "en")
	rec := httptest.NewRecorder()
	srv.formEdit(rec, req)
	body := rec.Body.String()
	for _, want := range []string{`title="Open full size"`, `>No image</span>`} {
		if !strings.Contains(body, want) {
			t.Errorf("в английской форме нет %s", want)
		}
	}
	if strings.Contains(body, "Нет изображения") || strings.Contains(body, "Открыть в полном размере") {
		t.Error("подписи превью остались русскими")
	}
}
