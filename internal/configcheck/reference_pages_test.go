package configcheck

import (
	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/ivantit66/onebase/internal/page"
	"testing"
)

func TestCheckReferencePages(t *testing.T) {
	el := &metadata.FormElement{Kind: metadata.FormElementField, Name: "Клиент", DataPath: "Объект.Филиал", ReferencePage: "Переписка"}
	proj := formKeysProject(el)
	proj.Pages = []*page.Page{{Name: "Переписка"}}
	if got := CheckReferencePages(proj); len(got) != 0 {
		t.Fatal(got)
	}
	el.ReferencePage = "НетСтраницы"
	if len(CheckReferencePages(proj)) != 1 {
		t.Fatal("missing page accepted")
	}
	el.ReferencePage = "Переписка"
	el.DataPath = "Объект.Комментарий"
	if len(CheckReferencePages(proj)) != 1 {
		t.Fatal("non-reference field accepted")
	}
}
