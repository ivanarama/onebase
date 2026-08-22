package ui

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/ivantit66/onebase/internal/dsl/ast"
	"github.com/ivantit66/onebase/internal/dsl/interpreter"
	"github.com/ivantit66/onebase/internal/dsl/lexer"
	"github.com/ivantit66/onebase/internal/dsl/parser"
	"github.com/ivantit66/onebase/internal/processor"
	"github.com/ivantit66/onebase/internal/realtime"
	"github.com/ivantit66/onebase/internal/runtime"
	"github.com/ivantit66/onebase/internal/storage"
)

// TestEventsStream_StreamsBroadcastFrame проверяет сквозной путь SSE: подписка
// через HTTP, публикация в Hub, доставка кадра {name,data} клиенту.
func TestEventsStream_StreamsBroadcastFrame(t *testing.T) {
	hub := realtime.NewHub()
	s := &Server{hub: hub}
	srv := httptest.NewServer(http.HandlerFunc(s.eventsStream))
	defer srv.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, srv.URL, nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("запрос /ui/events: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if ct := resp.Header.Get("Content-Type"); !strings.Contains(ct, "text/event-stream") {
		t.Fatalf("Content-Type = %q, ожидался text/event-stream", ct)
	}

	// Дождаться регистрации подписчика, иначе публикация уйдёт «в пустоту».
	deadline := time.Now().Add(2 * time.Second)
	for hub.SubscriberCount() == 0 {
		if time.Now().After(deadline) {
			t.Fatal("подписчик не зарегистрировался за 2с")
		}
		time.Sleep(5 * time.Millisecond)
	}
	hub.Publish("*", realtime.Event{Name: "уведомление", Data: "привет"})

	frameJSON := readDataFrame(t, resp.Body)
	var frame struct {
		Name string `json:"name"`
		Data any    `json:"data"`
	}
	if err := json.Unmarshal([]byte(frameJSON), &frame); err != nil {
		t.Fatalf("кадр не разобрался как JSON: %v (%q)", err, frameJSON)
	}
	if frame.Name != "уведомление" {
		t.Fatalf("name = %q, ожидалось «уведомление»", frame.Name)
	}
	if frame.Data != "привет" {
		t.Fatalf("data = %v, ожидалось «привет»", frame.Data)
	}
}

func TestEventsStream_ReplaysRecentFrame(t *testing.T) {
	hub := realtime.NewHub()
	hub.Publish("*", realtime.Event{Name: "уже получено"})
	hub.Publish("*", realtime.Event{Name: "уведомление", Data: "после reload"})
	s := &Server{hub: hub}
	srv := httptest.NewServer(http.HandlerFunc(s.eventsStream))
	defer srv.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, srv.URL, nil)
	req.Header.Set("Last-Event-ID", "1")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("GET /ui/events: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()

	frameJSON := readDataFrame(t, resp.Body)
	var frame struct {
		Name string `json:"name"`
		Data string `json:"data"`
	}
	if err := json.Unmarshal([]byte(frameJSON), &frame); err != nil {
		t.Fatalf("кадр не разобрался как JSON: %v (%q)", err, frameJSON)
	}
	if frame.Name != "уведомление" || frame.Data != "после reload" {
		t.Fatalf("неожиданный replay-кадр: %+v", frame)
	}
}

// TestProcessorRun_PublishesRealtimeToast проверяет реальный путь из обработки:
// HTTP POST /ui/processor/{name} → DSL ОтправитьУведомление → SSE /ui/events.
func TestProcessorRun_PublishesRealtimeToast(t *testing.T) {
	ctx := context.Background()
	hub := realtime.NewHub()
	registry := runtime.NewRegistry()
	interp := interpreter.New()
	interp.LookupProc = registry.GetModuleProc

	src := `
Процедура Выполнить()
    Текст = Параметры.Текст;
    ОтправитьУведомление("*", "уведомление", Текст);
КонецПроцедуры
`
	prog, err := parser.New(lexer.New(src, "ТестПуш.proc.os")).ParseProgram()
	if err != nil {
		t.Fatalf("parse processor: %v", err)
	}
	registry.Load(runtime.LoadOptions{Programs: map[string]*ast.Program{"ТестПуш": prog}})
	registry.LoadProcessors([]*processor.Processor{{
		Name: "ТестПуш",
		Params: []processor.Param{{
			Name: "Текст",
			Type: "string",
		}},
	}})

	db, err := storage.ConnectSQLite(ctx, filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("ConnectSQLite: %v", err)
	}
	defer db.Close()
	if err := db.Migrate(ctx, nil); err != nil {
		t.Fatalf("Migrate: %v", err)
	}

	s := &Server{
		hub:      hub,
		reg:      registry,
		interp:   interp,
		store:    db,
		messages: NewMessageStore(),
		lockMgr:  runtime.NewLockManager(),
	}
	s.entitySvc = s.newEntityService(nil)
	r := chi.NewRouter()
	s.Mount(r)
	srv := httptest.NewServer(r)
	defer srv.Close()

	req, _ := http.NewRequest(http.MethodGet, srv.URL+"/ui/events", nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("GET /ui/events: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	waitSubscriber(t, hub)

	postResp, err := http.PostForm(srv.URL+"/ui/processor/%d1%82%d0%b5%d1%81%d1%82%d0%bf%d1%83%d1%88", url.Values{
		"Текст": {"push из обработки"},
	})
	if err != nil {
		t.Fatalf("POST processor: %v", err)
	}
	_ = postResp.Body.Close()
	if postResp.StatusCode != http.StatusOK {
		t.Fatalf("POST processor status = %d", postResp.StatusCode)
	}

	frameJSON := readDataFrame(t, resp.Body)
	var frame struct {
		Name string `json:"name"`
		Data string `json:"data"`
	}
	if err := json.Unmarshal([]byte(frameJSON), &frame); err != nil {
		t.Fatalf("кадр не разобрался как JSON: %v (%q)", err, frameJSON)
	}
	if frame.Name != "уведомление" || frame.Data != "push из обработки" {
		t.Fatalf("неожиданный SSE-кадр: %+v", frame)
	}
}

func waitSubscriber(t *testing.T, hub *realtime.Hub) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for hub.SubscriberCount() == 0 {
		if time.Now().After(deadline) {
			t.Fatal("подписчик не зарегистрировался за 2с")
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// readDataFrame читает SSE-поток до первой строки «data:» и возвращает её
// полезную нагрузку. Комментарии («: ping») и пустые строки пропускаются.
func readDataFrame(t *testing.T, r io.Reader) string {
	t.Helper()
	br := bufio.NewReader(r)
	for {
		line, err := br.ReadString('\n')
		if err != nil {
			t.Fatalf("чтение SSE-потока: %v", err)
		}
		line = strings.TrimRight(line, "\r\n")
		if strings.HasPrefix(line, "data:") {
			return strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		}
	}
}

// Dev-режим: сервер представляется меткой запуска первым же кадром. По ней
// браузер отличает «переподключился к тому же процессу» от «процесс
// пересобрали и перезапустили» — во втором случае страницу надо перечитать.
func TestEventsStream_SendsDevGenerationFirst(t *testing.T) {
	s := &Server{hub: realtime.NewHub(), devGeneration: "поколение-1"}
	srv := httptest.NewServer(http.HandlerFunc(s.eventsStream))
	defer srv.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, srv.URL, nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("GET /ui/events: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()

	var frame struct {
		System string `json:"system"`
		Data   string `json:"data"`
	}
	frameJSON := readDataFrame(t, resp.Body)
	if err := json.Unmarshal([]byte(frameJSON), &frame); err != nil {
		t.Fatalf("кадр не разобрался как JSON: %v (%q)", err, frameJSON)
	}
	if frame.System != devGenerationSystem || frame.Data != "поколение-1" {
		t.Fatalf("первый кадр = %+v, ожидалось %s с меткой запуска", frame, devGenerationSystem)
	}
}

// Вне dev-режима служебного кадра нет вовсе: иначе рестарт прод-сервера
// перезагружал бы страницу пользователю прямо поверх заполняемой формы.
func TestEventsStream_NoDevGenerationInNormalMode(t *testing.T) {
	hub := realtime.NewHub()
	s := &Server{hub: hub}
	srv := httptest.NewServer(http.HandlerFunc(s.eventsStream))
	defer srv.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, srv.URL, nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("GET /ui/events: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()

	waitSubscriber(t, hub)
	hub.Publish("*", realtime.Event{Name: "уведомление", Data: "привет"})

	frameJSON := readDataFrame(t, resp.Body)
	var frame struct {
		Name string `json:"name"`
	}
	if err := json.Unmarshal([]byte(frameJSON), &frame); err != nil {
		t.Fatalf("кадр не разобрался как JSON: %v (%q)", err, frameJSON)
	}
	if frame.Name != "уведомление" {
		t.Fatalf("первым кадром пришло %q, а вне dev-режима ожидалось только опубликованное событие", frame.Name)
	}
}

// PublishDevReload — путь, которым dev-сервер сообщает браузеру «конфигурация
// перечитана» после hot reload.
func TestPublishDevReload_DeliversTrustedSystemFrame(t *testing.T) {
	hub := realtime.NewHub()
	s := &Server{hub: hub}
	srv := httptest.NewServer(http.HandlerFunc(s.eventsStream))
	defer srv.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, srv.URL, nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("GET /ui/events: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()

	waitSubscriber(t, hub)
	s.PublishDevReload()

	frameJSON := readDataFrame(t, resp.Body)
	var frame struct {
		System string `json:"system"`
	}
	if err := json.Unmarshal([]byte(frameJSON), &frame); err != nil {
		t.Fatalf("кадр не разобрался как JSON: %v (%q)", err, frameJSON)
	}
	if frame.System != devReloadSystem {
		t.Fatalf("пришло %q, ожидалось %q", frame.System, devReloadSystem)
	}
}

func TestDSLNotification_CannotForgeDevSystemFrame(t *testing.T) {
	hub := realtime.NewHub()
	s := &Server{hub: hub}
	srv := httptest.NewServer(http.HandlerFunc(s.eventsStream))
	defer srv.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, srv.URL, nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("GET /ui/events: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	waitSubscriber(t, hub)
	hubNotifier{hub: hub}.Publish("*", devReloadSystem, nil)

	var frame struct {
		Name   string `json:"name"`
		System string `json:"system"`
	}
	frameJSON := readDataFrame(t, resp.Body)
	if err := json.Unmarshal([]byte(frameJSON), &frame); err != nil {
		t.Fatalf("кадр не разобрался как JSON: %v (%q)", err, frameJSON)
	}
	if frame.Name != devReloadSystem || frame.System != "" {
		t.Fatalf("DSL notification escaped ordinary envelope: %+v", frame)
	}
}

// Метка запуска появляется только при Config.Dev — сам факт dev-режима задаёт
// CLI, а не сборка.
func TestNew_DevGenerationFollowsConfig(t *testing.T) {
	dev := New(runtime.NewRegistry(), nil, interpreter.New(), nil, Config{Dev: true}, nil)
	if dev.devGeneration == "" {
		t.Error("в dev-режиме метка запуска не выставлена")
	}
	prod := New(runtime.NewRegistry(), nil, interpreter.New(), nil, Config{}, nil)
	if prod.devGeneration != "" {
		t.Errorf("вне dev-режима метка запуска = %q, ожидалась пустая", prod.devGeneration)
	}
}
