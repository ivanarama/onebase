package ui

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/ivantit66/onebase/internal/auth"
	"github.com/ivantit66/onebase/internal/i18n"
	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/ivantit66/onebase/internal/storage"
)

func TestManagedFormCloseIntentHandlerWriteUnreadableError(t *testing.T) {
	for _, lang := range []string{"ru", "en"} {
		for _, existing := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/existing=%t", lang, existing), func(t *testing.T) {
				srv, entity := setupManagedEventsServer(t, `
Процедура ПроверитьЗакрытие(Отказ)
 Объект.Наименование = "hidden";
 Объект.Записать();
КонецПроцедуры
`, map[metadata.FormEventType]string{metadata.FormEventBeforeClose: "ПроверитьЗакрытие"}, nil)
				bundle, err := i18n.Load(i18n.EmbeddedLocales, "")
				if err != nil {
					t.Fatal(err)
				}
				srv.cfg.Bundle = bundle
				user := &auth.User{ID: "close-reader", Login: "close-reader", Lang: lang, Roles: []*auth.Role{{Permissions: auth.Permission{
					Catalogs: map[string][]string{entity.Name: {"read", "write"}},
					RowAccess: auth.RowAccess{Catalogs: map[string]auth.RowPolicies{
						entity.Name: {"read": {Field: "Наименование", Op: "eq", Value: auth.RowValue{Literal: "readable"}}},
					}},
				}}}}
				body := closeIntentBody(uuid.NewString(), "close", "readable")
				id := uuid.New()
				if existing {
					if err := srv.store.Upsert(context.Background(), entity.Name, id, map[string]any{"Наименование": "readable"}, entity); err != nil {
						t.Fatal(err)
					}
					body.Set("_id", id.String())
					body.Set("_version", "1")
				}
				rec := executeFormCloseIntentAsUser(t, srv, entity, body, user)
				response := decodeCloseIntentResponse(t, rec)
				if rec.Code != http.StatusOK || response.OK || response.Close == nil || response.Close.Allowed || response.Close.Saved {
					t.Fatalf("handler write did not fail closed: status=%d response=%+v", rec.Code, response)
				}
				want := "доступ запрещён"
				if lang == "en" {
					want = "access denied"
				}
				if response.Error != want || strings.Contains(rec.Body.String(), "close save result is unreadable") {
					t.Fatalf("error=%q, want %q; body=%s", response.Error, want, rec.Body.String())
				}
				if response.SavedID != "" || response.Version != 0 || len(response.Values) != 0 || len(response.TableParts) != 0 {
					t.Fatalf("failed preflight exposed saved state: %+v", response)
				}
				rows, err := srv.store.List(context.Background(), entity.Name, entity, storage.ListParams{})
				if err != nil {
					t.Fatal(err)
				}
				if existing {
					if len(rows) != 1 || rows[0]["Наименование"] != "readable" {
						t.Fatalf("failed write changed stored row: %v", rows)
					}
					version, exists, err := srv.store.EntityVersionExists(context.Background(), entity.Name, id)
					if err != nil || !exists || version != 1 {
						t.Fatalf("failed write changed stored version: version=%d exists=%t err=%v", version, exists, err)
					}
				} else if len(rows) != 0 {
					t.Fatalf("failed write created rows: %v", rows)
				}
			})
		}
	}
}
