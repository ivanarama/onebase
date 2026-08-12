package ui

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"

	"github.com/google/uuid"
)

// CORE-01 / issue #775: проведение документа кнопкой из списка (postDocument)
// раньше теряло пост-эффекты — в частности веб-хук document.post, — которые
// срабатывают при проведении через форму, REST и DSL. Проверяем, что событие
// теперь публикуется и документ действительно проведён.
func TestPostDocumentFromList_DispatchesPostWebhook(t *testing.T) {
	sink := &dslHookSink{}
	srv := httptest.NewServer(sink.handler())
	defer srv.Close()

	doc := dslWebhookDoc()
	s, d, db, ctx := dslWebhookServer(t, srv.URL, doc, nil)

	id := uuid.New()
	if err := db.Upsert(ctx, doc.Name, id, map[string]any{"Номер": "ПОС-1"}, doc); err != nil {
		t.Fatal(err)
	}

	r := reqWithChi("POST", "/ui/document/поступлениетоваров/"+id.String()+"/post", url.Values{},
		map[string]string{"kind": "document", "entity": "поступлениетоваров", "id": id.String()})
	rec := httptest.NewRecorder()
	s.postDocument(rec, r)
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("ожидался 303, получен %d: %s", rec.Code, rec.Body.String())
	}
	d.Wait()

	found := false
	for _, e := range sink.sorted() {
		if e == "document.post" {
			found = true
		}
	}
	if !found {
		t.Fatalf("проведение из списка не отправило document.post; события: %v", sink.sorted())
	}

	row, err := db.GetByID(ctx, doc.Name, id, doc)
	if err != nil {
		t.Fatal(err)
	}
	if !asBool(row["posted"]) {
		t.Fatalf("документ не отмечен проведённым: %#v", row)
	}
}
