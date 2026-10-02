package launcher

import (
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
)

// Клиентское подключение — запись, которой лаунчер не владеет. Тесты держат
// именно эту границу: адрес вместо порта, никаких операций жизненного цикла и
// никакого занятого порта в реестре.

func TestNormalizeServerURL(t *testing.T) {
	ok := []struct{ in, want string }{
		{"https://onebase.example.local:8443", "https://onebase.example.local:8443"},
		{"http://10.2.5.62:8080", "http://10.2.5.62:8080"},
		// Хвостовой слэш убираем: ниже адрес складывается с путями («/ui»).
		{"https://srv/", "https://srv"},
		{"  https://srv:443  ", "https://srv:443"},
	}
	for _, c := range ok {
		got, err := normalizeServerURL(c.in)
		if err != nil {
			t.Errorf("%q: неожиданная ошибка: %v", c.in, err)
			continue
		}
		if got != c.want {
			t.Errorf("%q: получили %q, ожидалось %q", c.in, got, c.want)
		}
	}

	bad := []struct{ in, why string }{
		{"", "пустой адрес"},
		{"onebase.example.local:8443", "без схемы: http и https различаются каналом, угадывать нельзя"},
		{"ftp://srv", "чужая схема"},
		{"https://", "нет хоста"},
		{"https://user:pass@srv", "логин в адресе"},
		// Путь молча терялся бы: лаунчер дописывает к адресу свои пути.
		{"https://srv/onebase", "путь в адресе"},
		{"https://srv?x=1", "параметры запроса"},
		{"https://srv#frag", "якорь"},
	}
	for _, c := range bad {
		if got, err := normalizeServerURL(c.in); err == nil {
			t.Errorf("%q (%s): ожидалась ошибка, получили %q", c.in, c.why, got)
		}
	}
}

func TestClientBaseIsNotOwnedByLauncher(t *testing.T) {
	b, err := NewClientBase("КЦ", "https://onebase.example.local:8443")
	if err != nil {
		t.Fatalf("NewClientBase: %v", err)
	}
	if !b.Client() {
		t.Fatal("запись с адресом сервера должна быть клиентской")
	}
	if b.Port != 0 || b.DB != "" || b.DBPath != "" || b.ConfigSource != "" {
		t.Errorf("поля запуска должны остаться пустыми: %+v", b)
	}

	r := NewRunner()
	// Запуск, остановка и миграция чужого сервера запрещены явной ошибкой, а не
	// молчаливым успехом: иначе «Стоп всё» отчитался бы, что всё остановлено.
	if err := r.Start(b); err == nil {
		t.Error("Start клиентской записи должен отказать")
	}
	if err := r.StopBase(b); err == nil {
		t.Error("StopBase клиентской записи должен отказать")
	}
	if _, err := r.MigrateBase(context.Background(), b); err == nil {
		t.Error("MigrateBase клиентской записи должен отказать")
	}

	st := r.RuntimeStatus(b)
	if !st.Client {
		t.Error("статус должен помечать запись клиентской")
	}
	if st.Running || st.Controllable || st.Occupied {
		t.Errorf("у клиентской записи не бывает процесса: %+v", st)
	}
	if got := r.BaseURL(b); got != "https://onebase.example.local:8443" {
		t.Errorf("BaseURL = %q, ожидался адрес сервера", got)
	}
}

// Клиентская запись не должна отбирать порт у базы, которую лаунчер поднимает
// сам: порт ей не нужен, а занятый впустую номер дал бы ложный конфликт.
func TestClientBaseTakesNoPort(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("HOME", dir)
	t.Setenv("USERPROFILE", dir)
	store := &Store{path: filepath.Join(dir, "ibases.yaml")}

	client, err := NewClientBase("Сервер КЦ", "https://srv:8443")
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Add(client); err != nil {
		t.Fatalf("Add клиентской: %v", err)
	}
	if client.Port != 0 {
		t.Errorf("клиентской записи выделен порт %d", client.Port)
	}

	local := &Base{Name: "Локальная", DB: "postgres://localhost/x", ConfigSource: "database"}
	if err := store.Add(local); err != nil {
		t.Fatalf("Add локальной: %v", err)
	}
	if local.Port == 0 {
		t.Fatal("локальной базе порт не выделен")
	}

	bases, err := store.List()
	if err != nil {
		t.Fatal(err)
	}
	if len(bases) != 2 {
		t.Fatalf("баз %d, ожидалось 2", len(bases))
	}
	for _, b := range bases {
		if b.ID == client.ID && !b.Client() {
			t.Error("клиентская запись перестала быть клиентской после чтения реестра")
		}
		if b.ID == client.ID && b.ServerURL != "https://srv:8443" {
			t.Errorf("адрес сервера не сохранился: %q", b.ServerURL)
		}
	}
}

// «Стоп всё» обязано пройти мимо клиентских записей, а не падать на них и не
// отчитываться, что остановило чужой сервер.
func TestStopAllSkipsClientBases(t *testing.T) {
	r := NewRunner()
	client, err := NewClientBase("Сервер", "https://srv:8443")
	if err != nil {
		t.Fatal(err)
	}
	skipped, err := r.StopAll([]*Base{client}, false)
	if err != nil {
		t.Fatalf("StopAll: %v", err)
	}
	if len(skipped) != 0 {
		t.Errorf("клиентская запись попала в пропущенные: %+v", skipped)
	}
}

func TestClientServerReachable(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/health" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	base, err := NewClientBase("Сервер", srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	if !clientServerReachable(context.Background(), base, srv.Client()) {
		t.Error("работающий сервер должен считаться доступным")
	}

	srv.Close()
	if clientServerReachable(context.Background(), base, srv.Client()) {
		t.Error("остановленный сервер не должен считаться доступным")
	}

	// Обычная база через эту проверку не ходит вовсе.
	if clientServerReachable(context.Background(), &Base{Name: "Локальная", Port: 8080}, nil) {
		t.Error("для не-клиентской записи проверка доступности неприменима")
	}
}

// Переключение вида записи в обе стороны не должно оставлять поля обоих видов:
// запись с одновременными server_url и DSN неоднозначна.
func TestUpdateSwitchesBaseKindCleanly(t *testing.T) {
	dir := t.TempDir()
	store := &Store{path: filepath.Join(dir, "ibases.yaml")}
	local := &Base{Name: "База", DB: "postgres://localhost/x", ConfigSource: "database", Port: 8080}
	if err := store.Add(local); err != nil {
		t.Fatal(err)
	}

	toClient := &Base{ID: local.ID, Name: local.Name, ServerURL: "https://srv:8443", Created: local.Created}
	if err := store.Update(toClient); err != nil {
		t.Fatalf("Update в клиентскую: %v", err)
	}
	got, err := store.Get(local.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !got.Client() {
		t.Fatal("запись не стала клиентской")
	}
	if got.DB != "" || got.Port != 0 {
		t.Errorf("поля запуска остались у клиентской записи: DB=%q Port=%d", got.DB, got.Port)
	}

	backToLocal := &Base{ID: local.ID, Name: local.Name, DB: "postgres://localhost/y",
		ConfigSource: "database", Port: 8081, Created: got.Created}
	if err := store.Update(backToLocal); err != nil {
		t.Fatalf("Update назад в локальную: %v", err)
	}
	got, err = store.Get(local.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Client() {
		t.Error("запись осталась клиентской после возврата")
	}
	if got.ServerURL != "" {
		t.Errorf("адрес сервера не затёрт: %q", got.ServerURL)
	}
}
