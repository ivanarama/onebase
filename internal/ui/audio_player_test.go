package ui

import (
	"bytes"
	"github.com/ivantit66/onebase/internal/metadata"
	"net/url"
	"strings"
	"testing"
)

func TestAudioPlayerHTTPEventAndStop(t *testing.T) {
	srv, ent := setupManagedEventsServer(t, `
Процедура Подготовить()
 Объект.ИсточникЗаписи = "11111111-1111-4111-8111-111111111111";
 Объект.ПодписьЗаписи = "Call A";
 ОстановитьАудио("Player");
КонецПроцедуры
`, nil, []*metadata.FormElement{{Kind: metadata.FormElementAudio, Name: "Player", DataPath: "Объект.ИсточникЗаписи", TitleFrom: "Объект.ПодписьЗаписи", Handlers: map[metadata.FormEventType]string{metadata.FormEventAudioPlay: "Подготовить"}}})
	ent.Forms[0].Attributes = []*metadata.FormAttribute{{Name: "ИсточникЗаписи", TypeRef: "string"}, {Name: "ПодписьЗаписи", TypeRef: "string"}}
	body := url.Values{"_element": {"Player"}, "_event": {string(metadata.FormEventAudioPlay)}, "_kind": {"object"}, "Наименование": {"X"}}
	resp := decodeFormEventResponse(t, executeFormEvent(t, srv, ent, body).Body.Bytes())
	if !resp.OK || resp.Error != "" || resp.Values["ИсточникЗаписи"] != "11111111-1111-4111-8111-111111111111" || resp.Values["ПодписьЗаписи"] != "Call A" || len(resp.AudioStops) != 1 || resp.AudioStops[0] != "Player" {
		t.Fatalf("unexpected response: %#v", resp)
	}
	body.Set("_event", "Нажатие")
	denied := decodeFormEventResponse(t, executeFormEventRaw(t, srv, ent, body).Body.Bytes())
	if denied.OK || denied.Error == "" {
		t.Fatalf("audio accepted Click: %#v", denied)
	}
}

func TestAudioPlayerRenderIsLazyAndOptional(t *testing.T) {
	form := &metadata.FormModule{LayoutKind: metadata.FormLayoutManaged, Elements: []*metadata.FormElement{{Kind: metadata.FormElementAudio, Name: "Player", DataPath: "Объект.Source", TitleFrom: "Объект.Caption"}}}
	ctx := map[string]any{"Form": form, "Lang": "en", "Values": map[string]string{"Source": "https://secret.invalid/?token=never-render", "Caption": "Call <A>"}}
	var b bytes.Buffer
	if err := tmpl.ExecuteTemplate(&b, "managed-element", map[string]any{"El": form.Elements[0], "Ctx": ctx}); err != nil {
		t.Fatal(err)
	}
	html := b.String()
	for _, want := range []string{"data-ob-audio-play", "data-ob-audio-stop", "data-ob-audio-time", "data-ob-audio-seek", "data-ob-audio-volume", `height:30px`, `max-width:600px`, `preload="none"`, "Call &lt;A&gt;"} {
		if !strings.Contains(html, want) {
			t.Fatalf("missing %s in %s", want, html)
		}
	}
	for _, bad := range []string{"secret.invalid", "src=", "autoplay", "data-ob-audio-media controls", `<input`} {
		if strings.Contains(html, bad) {
			t.Fatalf("lazy player leaked %s", bad)
		}
	}
}

func TestAudioOnlyRendererExplicitRoleExclusion(t *testing.T) {
	ctx := map[string]any{"Entity": &metadata.Entity{Name: "Recordings", Title: "Recordings", Kind: metadata.KindCatalog, AudioPlayer: true}, "ID": "11111111-1111-4111-8111-111111111111", "Lang": "en"}
	render := func() string {
		var b bytes.Buffer
		if err := tmpl.ExecuteTemplate(&b, "ob-attachments", ctx); err != nil {
			t.Fatal(err)
		}
		return b.String()
	}
	html := render()
	if !strings.Contains(html, "data-audio-metadata-url") || strings.Contains(html, "data-ob-attachments ") || strings.Contains(html, "att-upload-form") || strings.Contains(html, "/download") {
		t.Fatalf("invalid audio-only renderer: %s", html)
	}
	ctx["AudioOnlyPlayer"] = false
	html = render()
	if strings.Contains(html, "data-ob-audio") || strings.Contains(html, "/static/audio.js") || !strings.Contains(html, "att-upload-form") {
		t.Fatalf("excluded role changed: %s", html)
	}
	ctx["Entity"].(*metadata.Entity).AudioPlayer = false
	if !strings.Contains(render(), "att-upload-form") {
		t.Fatal("default lost original attachments")
	}
}
