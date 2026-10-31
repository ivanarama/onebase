package ui

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/ivantit66/onebase/internal/auth"
	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/ivantit66/onebase/internal/runtime"
	"github.com/ivantit66/onebase/internal/storage"
	"github.com/ivantit66/onebase/internal/testutil/browseracceptance"
)

func runEntityListConcealBrowserAcceptance(t *testing.T) {
	browseracceptance.Require(t)
	db, err := storage.ConnectSQLite(context.Background(), filepath.Join(t.TempDir(), "conceal.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := db.EnsureServiceSchema(context.Background()); err != nil {
		t.Fatal(err)
	}
	c, id := concealFixture(t, db)
	form := c.entity.Forms[0]
	form.ProgramAST = mustParse(t, concealTestModule+`
Процедура РаскрытьСВопросом()
 Объект.Маской = Ложь;
 ПоказатьВопрос("Продолжить?", "ДаНет");
КонецПроцедуры
Процедура Ответить()
 Сообщить("Ответ принят");
КонецПроцедуры
`)
	form.Elements = append(form.Elements, &metadata.FormElement{Name: "Вопрос", Kind: metadata.FormElementButton, Handlers: map[metadata.FormEventType]string{metadata.FormEventOnClick: "РаскрытьСВопросом", metadata.FormEventOnAnswer: "Ответить"}})

	form.Elements = append(form.Elements, &metadata.FormElement{Name: "Причина", Kind: metadata.FormElementField, DataPath: "Форма.Причина"})
	target := &metadata.Entity{Name: "СвязанныеЗаписи", Kind: metadata.KindCatalog, Fields: []metadata.Field{{Name: "Наименование", Type: metadata.FieldTypeString}, {Name: "Ключ", Type: metadata.FieldTypeString}}}
	form.Elements = append(form.Elements, &metadata.FormElement{ID: "history", Name: "История", Kind: metadata.FormElementEntityList, EntityList: &metadata.FormEntityListSpec{Entity: target.Name, Columns: []string{"Наименование"}, Limit: 5, Filter: []metadata.FormEntityListCondition{{Field: "Ключ", Op: "eq", Source: "Объект.Секрет"}}}})
	c.server.reg.Load(runtime.LoadOptions{Entities: []*metadata.Entity{c.entity, target}})
	if err := db.Migrate(context.Background(), []*metadata.Entity{c.entity, target}); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"секрет", "другой"} {
		if err := db.Upsert(context.Background(), target.Name, uuid.New(), map[string]any{"Наименование": "строка-" + name, "Ключ": name}, target); err != nil {
			t.Fatal(err)
		}
	}
	c.server = New(c.server.reg, db, c.server.interp, nil, Config{}, nil)
	defer c.server.Close()
	router := chi.NewRouter()
	c.server.MountStatic(router)
	router.Group(func(r chi.Router) {
		r.Use(func(next http.Handler) http.Handler {
			return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				r.AddCookie(&http.Cookie{Name: "onebase_session", Value: c.session})
				next.ServeHTTP(w, r.WithContext(auth.ContextWithUser(r.Context(), c.user)))
			})
		})
		c.server.Mount(r)
	})
	server := httptest.NewServer(router)
	defer func() { c.server.CloseEventStreams(); server.Close() }()
	data, err := json.Marshal(map[string]string{"owner": id.String(), "entity": c.entity.Name})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, "node", "node_modules/@playwright/test/cli.js", "test", "--config", "acceptance.config.js", "entity-list-conceal.spec.js")
	cmd.Dir = filepath.Join(browseracceptance.Root(), "e2e")
	cmd.Env = append(os.Environ(), "OB_ACCEPTANCE_SUITE=entity-list-conceal", "OB_ACCEPTANCE_URL="+server.URL, "OB_LIST_IDS="+string(data))
	out, err := cmd.CombinedOutput()
	t.Logf("%s", out)
	if err != nil {
		t.Fatalf("concealed list browser: %v", err)
	}
}
