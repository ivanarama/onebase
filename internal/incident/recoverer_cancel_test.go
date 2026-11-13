package incident_test

import (
	"bytes"
	"context"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/ivantit66/onebase/internal/dsl/interpreter"
	"github.com/ivantit66/onebase/internal/incident"
)

// Клиент ушёл посреди запроса (закрыли форму, удалили iframe), и обработчик
// оборвался паникой DSL-уровня на операции с отменённым контекстом — ровно так
// «Предзагрузка реквизитов …: context canceled» доходила до журнала как
// ERROR «паника в обработчике» с инцидентом. Отмена клиентом — не сбой.
func TestRecovererClientCancelIsNotIncident(t *testing.T) {
	var logs bytes.Buffer
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&logs, &slog.HandlerOptions{Level: slog.LevelDebug})))
	t.Cleanup(func() { slog.SetDefault(prev) })

	for _, tc := range []struct {
		name     string
		cancel   bool
		deadline bool
		incident bool
		status   int
	}{
		{name: "клиент ушёл — не инцидент", cancel: true, incident: false, status: incident.StatusClientClosedRequest},
		{name: "клиент на месте — инцидент как раньше", incident: true, status: http.StatusInternalServerError},
		{name: "таймаут операции — инцидент как раньше", deadline: true, incident: true, status: http.StatusInternalServerError},
	} {
		t.Run(tc.name, func(t *testing.T) {
			logs.Reset()
			store := incident.NewStore(16)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			if tc.deadline {
				var stop context.CancelFunc
				ctx, stop = context.WithTimeout(ctx, 0)
				defer stop()
			}
			handler := incident.Recoverer(store, nil)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if tc.cancel {
					cancel() // the connection drops while the handler is running
				}
				<-r.Context().Done()
				interpreter.RaiseUserError("Предзагрузка реквизитов А_Реклама: get fields by ids А_Реклама: " + r.Context().Err().Error())
			}))
			if !tc.cancel && !tc.deadline {
				handler = incident.Recoverer(store, nil)(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
					interpreter.RaiseUserError("настоящая ошибка обработчика")
				}))
			}
			rec := httptest.NewRecorder()
			handler.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/ui/document/x/form-event", nil).WithContext(ctx))

			if rec.Code != tc.status {
				t.Fatalf("status %d, want %d: %s", rec.Code, tc.status, rec.Body.String())
			}
			if got := len(store.Recent("", 16)); (got == 1) != tc.incident || got > 1 {
				t.Fatalf("incidents = %d, want incident=%t", got, tc.incident)
			}
			if gotErr := strings.Contains(logs.String(), "level=ERROR"); gotErr != tc.incident {
				t.Fatalf("ERROR logged = %t, want %t:\n%s", gotErr, tc.incident, logs.String())
			}
		})
	}
}

// http.ErrAbortHandler по-прежнему пробрасывается серверу, даже когда клиент
// уже ушёл: net/http ждёт эту панику обратно и сам молча закрывает ответ.
func TestRecovererAbortHandlerStillRepanics(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	handler := incident.Recoverer(incident.NewStore(4), nil)(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		panic(http.ErrAbortHandler)
	}))
	defer func() {
		if rec := recover(); rec != http.ErrAbortHandler {
			t.Fatalf("recovered %v, want http.ErrAbortHandler", rec)
		}
	}()
	handler.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/", nil).WithContext(ctx))
}
