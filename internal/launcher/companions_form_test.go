package launcher

import (
	"io"
	"net/http"
	"net/url"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// «Запускать вместе с базой» в форме записи базы: строки «ключ | путь |
// аргументы» ложатся в ibases.yaml, форма изменения показывает их обратно, а
// ошибочная строка возвращает форму с сообщением и введённым текстом, не
// трогая сохранённую запись. Путь: маршруты лаунчера /bases и /bases/{id}.
func TestBaseFormCompanionsHTTP(t *testing.T) {
	savedBundle := launcherBundle
	t.Cleanup(func() { launcherBundle = savedBundle })
	store := &Store{path: filepath.Join(t.TempDir(), "ibases.yaml")}
	srv, err := NewServer(store, NewRunner())
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- srv.ListenAndServe() }()
	client := &http.Client{Timeout: 10 * time.Second}
	t.Cleanup(func() {
		client.CloseIdleConnections()
		srv.Close()
		if err := <-done; err != nil && err != http.ErrServerClosed {
			t.Error(err)
		}
	})
	send := func(method, path string, form url.Values) string {
		t.Helper()
		req, err := http.NewRequestWithContext(t.Context(), method, srv.URL()+path, strings.NewReader(form.Encode()))
		if err != nil {
			t.Fatal(err)
		}
		if method == http.MethodPost {
			req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
			req.Header.Set("Origin", srv.URL())
		}
		resp, err := client.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = resp.Body.Close() }()
		body, _ := io.ReadAll(resp.Body)
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("%s %s: status %d", method, path, resp.StatusCode)
		}
		return string(body)
	}

	send(http.MethodPost, "/bases", url.Values{
		"name": {"КЦ — рабочая"}, "base_kind": {baseKindClient}, "server_url": {"https://10.2.5.62:8443"},
		"companions": {`callista | C:\Callista\callista-operator.exe | --minimized`},
	})
	bases, err := store.List()
	if err != nil || len(bases) != 1 {
		t.Fatalf("записи: %v %v", bases, err)
	}
	b := bases[0]
	if len(b.Companions) != 1 || b.Companions[0].Key != "callista" || b.Companions[0].Path != `C:\Callista\callista-operator.exe` ||
		strings.Join(b.Companions[0].Args, " ") != "--minimized" {
		t.Fatalf("сопутствующие программы не сохранены: %+v", b.Companions)
	}
	if page := send(http.MethodGet, "/bases/"+b.ID+"/edit", nil); !strings.Contains(page, `callista | C:\Callista\callista-operator.exe | --minimized`) {
		t.Fatal("форма изменения не показывает сохранённые программы")
	}

	page := send(http.MethodPost, "/bases/"+b.ID, url.Values{
		"name": {"КЦ — рабочая"}, "base_kind": {baseKindClient}, "server_url": {"https://10.2.5.62:8443"},
		"companions": {"Callista Operator | C:\\c.exe"},
	})
	if !strings.Contains(page, "Callista Operator | C:\\c.exe") || !strings.Contains(page, "латиница") {
		t.Fatal("ошибочная строка: форма не вернула текст и сообщение")
	}
	again, err := store.Get(b.ID)
	if err != nil || len(again.Companions) != 1 || again.Companions[0].Key != "callista" {
		t.Fatalf("ошибочная форма изменила запись: %+v %v", again, err)
	}

	// Правильное изменение сохраняется, пустое поле убирает переопределение
	// станции — тогда работает манифест комплекта.
	send(http.MethodPost, "/bases/"+b.ID, url.Values{
		"name": {"КЦ — рабочая"}, "base_kind": {baseKindClient}, "server_url": {"https://10.2.5.62:8443"},
		"companions": {`callista | D:\Телефония\callista.exe`},
	})
	if again, err = store.Get(b.ID); err != nil || len(again.Companions) != 1 || again.Companions[0].Path != `D:\Телефония\callista.exe` {
		t.Fatalf("изменение программ не сохранено: %+v %v", again, err)
	}
	send(http.MethodPost, "/bases/"+b.ID, url.Values{
		"name": {"КЦ — рабочая"}, "base_kind": {baseKindClient}, "server_url": {"https://10.2.5.62:8443"}, "companions": {""},
	})
	if again, err = store.Get(b.ID); err != nil || len(again.Companions) != 0 {
		t.Fatalf("пустое поле не убрало программы: %+v %v", again, err)
	}
}
