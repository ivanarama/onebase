package ui

import (
	"net/http/httptest"
	"net/url"
	"testing"

	"github.com/ivantit66/onebase/internal/metadata"
)

// «Записать» в управляемой форме проведённого документа перепроводит его, как
// в 1С (ПараметрыЗаписи.РежимЗаписи = Проведение): документ остаётся
// проведённым, движения пересчитаны по новым данным. Без управляемой формы
// (автоформа) — прежняя запись со снятием проведения. Тест — штатный маршрут
// POST /ui/document/{entity}/{id} (submitEdit), действие «Записать» (_action="").
func TestManagedFormWriteRepostsPostedDocument(t *testing.T) {
	for _, tc := range []struct {
		name       string
		managed    bool
		wantPosted bool
		wantQty    float64
	}{
		{"управляемая форма — перепроведение", true, true, 150},
		{"автоформа — прежняя запись, проведение снято", false, false, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx, db, s, dp, doc := newPostingDoc(t)
			if tc.managed {
				doc.Forms = []*metadata.FormModule{managedObjectForm(fieldEl("ПолеНомер", "Объект.Номер"),
					&metadata.FormElement{Kind: metadata.FormElementTablePart, Name: "ЭлементТовары", DataPath: "Объект.Товары"})}
			}
			w := postOne(t, dp)
			post := url.Values{
				"Номер":          {"ПОС-001"},
				"_action":        {""},
				"tp_json.Товары": {`[{"Номенклатура":"Тумбочка","Количество":150}]`},
			}
			r := reqWithChi("POST", "/ui/document/поступлениетоваров/"+w.obj.ID.String(), post,
				map[string]string{"kind": "document", "entity": "поступлениетоваров", "id": w.obj.ID.String()})
			rec := httptest.NewRecorder()
			s.submitEdit(rec, r)
			if rec.Code >= 400 {
				t.Fatalf("submitEdit: %d %s", rec.Code, rec.Body.String())
			}
			var posted bool
			if err := db.QueryRow(ctx, "SELECT posted FROM поступлениетоваров LIMIT 1").Scan(&posted); err != nil {
				t.Fatal(err)
			}
			if posted != tc.wantPosted {
				t.Fatalf("posted=%t, want %t", posted, tc.wantPosted)
			}
			var qty float64
			if err := db.QueryRow(ctx, "SELECT COALESCE(SUM(CAST(количество AS REAL)), 0) FROM рег_остаткитоваров").Scan(&qty); err != nil {
				t.Fatal(err)
			}
			if qty != tc.wantQty {
				t.Fatalf("движения: количество=%v, want %v", qty, tc.wantQty)
			}
		})
	}
}
