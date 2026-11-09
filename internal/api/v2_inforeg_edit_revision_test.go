package api

import (
	"net/http"
	"strings"
	"testing"

	"github.com/ivantit66/onebase/internal/dbtest"
	"github.com/ivantit66/onebase/internal/storage"
)

func TestInfoRegV2RevisionRemainsPrivateMatrix(t *testing.T) {
	dbtest.ForEachDialect(t, func(t *testing.T, db *storage.DB) {
		ir := matrixTariffs()
		router := matrixInfoRegAPI(t, db, ir)
		seedTariffs(t, db, ir, [][3]any{{"P", "A", "12.50"}})
		w, resp := getInfoReg(t, router, "/api/v2/inforeg/ТарифыСтатей", nil)
		if w.Code != http.StatusOK || len(resp.Data) != 1 {
			t.Fatalf("HTTP %d %s", w.Code, w.Body.String())
		}
		if strings.Contains(w.Body.String(), storage.InfoRegEditRevisionColumn) || len(resp.Data[0]) != 3 {
			t.Fatalf("public schema changed: %s", w.Body.String())
		}
	})
}
