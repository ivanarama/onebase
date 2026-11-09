package launcher

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// Фиктивный exe: его содержимое — версия; companionUpdater.version читает её
// так же, как FileVersion настоящего exe.
func fileVersionFromContent(path string) string {
	b, err := os.ReadFile(path) //nolint:gosec // G304: путь из временного каталога теста
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(b))
}

func zipArchive(t *testing.T, files map[string]string) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for name, body := range files {
		w, err := zw.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := w.Write([]byte(body)); err != nil {
			t.Fatal(err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

type updateServer struct {
	*httptest.Server
	manifest string
	archive  []byte
	fail     bool
}

func newUpdateServer(t *testing.T) *updateServer {
	t.Helper()
	s := &updateServer{}
	s.Server = httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if s.fail {
			http.Error(w, "down", http.StatusServiceUnavailable)
			return
		}
		switch r.URL.Path {
		case "/updates/callista/operator-desktop.json":
			_, _ = w.Write([]byte(s.manifest))
		case "/updates/callista/downloads/op.zip":
			_, _ = w.Write(s.archive)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(s.Close)
	return s
}

func (s *updateServer) publish(version string, archive []byte, sum string) {
	if sum == "" {
		h := sha256.Sum256(archive)
		sum = hex.EncodeToString(h[:])
	}
	s.archive = archive
	s.manifest = fmt.Sprintf(`{"version":%q,"downloads":{"win32":{"url":"/updates/callista/downloads/op.zip","size":%d,"sha256":%q,"releaseNotes":["x"]}}}`,
		version, len(archive), sum)
}

// installed — комплект с Callista версии v: companions/callista-operator/…
func installed(t *testing.T, v string) (Companion, string) {
	t.Helper()
	root := t.TempDir()
	dir := filepath.Join(root, "companions", "callista-operator")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	exe := filepath.Join(dir, "callista-operator.exe")
	if err := os.WriteFile(exe, []byte(v), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "old.dll"), []byte("old"), 0o600); err != nil {
		t.Fatal(err)
	}
	return Companion{Key: "callista", Path: exe, Dir: dir}, dir
}

func testUpdater(s *updateServer) *companionUpdater {
	return &companionUpdater{client: s.Client(), version: fileVersionFromContent, now: time.Now}
}

func TestCompanionUpdate_InstallsNewerVersionKeepsPrevious(t *testing.T) {
	s := newUpdateServer(t)
	s.publish("0.7.0-preview.6", zipArchive(t, map[string]string{
		"callista-operator-0.7.0-preview.6.exe": "0.7.0-preview.6",
		"resources/app.asar":                    "asar",
		"ffmpeg.dll":                            "dll",
	}), "")
	c, dir := installed(t, "0.7.0-preview.5")
	c.UpdateManifest = s.URL + "/updates/callista/operator-desktop.json"

	got, err := testUpdater(s).Update(context.Background(), c)
	if err != nil || got != "0.7.0-preview.6" {
		t.Fatalf("Update = %q, %v", got, err)
	}
	if v := fileVersionFromContent(c.Path); v != "0.7.0-preview.6" {
		t.Fatalf("на прежнем пути версия %q — exe новой версии не получил прежнее имя", v)
	}
	for _, rel := range []string{"resources/app.asar", "ffmpeg.dll"} {
		if _, err := os.Stat(filepath.Join(dir, filepath.FromSlash(rel))); err != nil {
			t.Fatalf("архив распакован не целиком: нет %s", rel)
		}
	}
	if v := fileVersionFromContent(filepath.Join(dir+companionUpdatePrevSuffix, "callista-operator.exe")); v != "0.7.0-preview.5" {
		t.Fatalf("прежняя версия не сохранена рядом: %q", v)
	}
	for _, leftover := range []string{dir + companionUpdateStageSuffix, dir + companionUpdateStageSuffix + ".zip", dir + companionUpdateStageSuffix + ".lock"} {
		if _, err := os.Stat(leftover); !os.IsNotExist(err) {
			t.Fatalf("после обновления остался %s", leftover)
		}
	}
}

func TestCompanionUpdate_NeverDowngradesOrRepeats(t *testing.T) {
	for _, tc := range []struct{ installed, published string }{
		{"0.7.0-preview.6", "0.7.0-preview.6"}, // та же
		{"0.7.0-preview.6", "0.6.8"},           // сейчас на сервере Callista
		{"0.7.0", "0.7.0-preview.9"},           // выпуск новее своего preview
	} {
		s := newUpdateServer(t)
		s.publish(tc.published, zipArchive(t, map[string]string{"callista-operator.exe": tc.published}), "")
		c, dir := installed(t, tc.installed)
		c.UpdateManifest = s.URL + "/updates/callista/operator-desktop.json"
		got, err := testUpdater(s).Update(context.Background(), c)
		if err != nil || got != "" {
			t.Fatalf("%s → %s: Update = %q, %v — ожидалось «нечего ставить»", tc.installed, tc.published, got, err)
		}
		if v := fileVersionFromContent(c.Path); v != tc.installed {
			t.Fatalf("%s → %s: установленная версия изменилась на %q", tc.installed, tc.published, v)
		}
		if _, err := os.Stat(dir + companionUpdatePrevSuffix); !os.IsNotExist(err) {
			t.Fatalf("%s → %s: папка переставлялась без обновления", tc.installed, tc.published)
		}
	}
}

func TestCompanionUpdate_FailuresKeepInstalledVersion(t *testing.T) {
	good := map[string]string{"callista-operator-0.7.0-preview.6.exe": "0.7.0-preview.6"}
	for _, tc := range []struct {
		name  string
		setup func(s *updateServer, t *testing.T)
		want  string
	}{
		{"битый sha256", func(s *updateServer, t *testing.T) {
			s.publish("0.7.0-preview.6", zipArchive(t, good), strings.Repeat("0", 64))
		}, "SHA-256"},
		{"сервер недоступен", func(s *updateServer, t *testing.T) { s.fail = true }, "ответ 503"},
		{"битый список", func(s *updateServer, t *testing.T) { s.manifest = "{не json" }, "список обновлений"},
		{"архив с чужого сервера", func(s *updateServer, t *testing.T) {
			s.publish("0.7.0-preview.6", zipArchive(t, good), "")
			s.manifest = strings.Replace(s.manifest, `"/updates/callista/downloads/op.zip"`, `"https://example.invalid/op.zip"`, 1)
		}, "не с того же сервера"},
		{"путь за пределы папки", func(s *updateServer, t *testing.T) {
			s.publish("0.7.0-preview.6", zipArchive(t, map[string]string{"../evil.exe": "x", "callista-operator.exe": "0.7.0-preview.6"}), "")
		}, "недопустимый путь"},
		{"версия в архиве не та", func(s *updateServer, t *testing.T) {
			s.publish("0.7.0-preview.6", zipArchive(t, map[string]string{"callista-operator.exe": "0.7.0-preview.4"}), "")
		}, "в архиве версия"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := newUpdateServer(t)
			tc.setup(s, t)
			c, dir := installed(t, "0.7.0-preview.5")
			c.UpdateManifest = s.URL + "/updates/callista/operator-desktop.json"
			got, err := testUpdater(s).Update(context.Background(), c)
			if err == nil || got != "" || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("Update = %q, %v — ожидалась ошибка «%s»", got, err, tc.want)
			}
			if v := fileVersionFromContent(c.Path); v != "0.7.0-preview.5" {
				t.Fatalf("после сбоя версия %q, ожидалась прежняя", v)
			}
			if _, err := os.Stat(dir + companionUpdateStageSuffix); !os.IsNotExist(err) {
				t.Fatal("после сбоя осталась папка обновления")
			}
		})
	}
}

func TestSemverNewer(t *testing.T) {
	for _, tc := range []struct {
		a, b string
		want bool
	}{
		{"0.7.0-preview.5", "0.7.0-preview.4", true},
		{"0.7.0-preview.10", "0.7.0-preview.9", true},
		{"0.7.0-preview.4", "0.6.8", true},
		{"0.7.0", "0.7.0-preview.9", true},
		{"0.6.8", "0.7.0-preview.5", false},
		{"0.7.0-preview.5", "0.7.0-preview.5", false},
		{"0.7.0-preview.5", "0.7.0", false},
		{"мусор", "0.6.8", false},
		{"0.7.1", "мусор", false},
	} {
		if got := semverNewer(tc.a, tc.b); got != tc.want {
			t.Errorf("semverNewer(%q, %q) = %v, want %v", tc.a, tc.b, got, tc.want)
		}
	}
}

// Лаунчер: работающую программу не обновляет и не запускает; обновление идёт
// в фоне и заканчивается запуском и сообщением; новая версия, которая не
// стартовала, откатывается к прежней.
func TestCompanionLauncher_UpdateBeforeStart(t *testing.T) {
	type call struct{ what, key string }
	for _, tc := range []struct {
		name      string
		running   bool
		version   string
		updateErr error
		failStart int // сколько первых запусков падает
		wantCalls []call
		wantMsg   string
	}{
		{name: "работает — не трогаем", running: true, wantCalls: nil},
		{name: "обновили и запустили", version: "0.7.0-preview.6",
			wantCalls: []call{{"update", "callista"}, {"start", "callista"}}, wantMsg: "Callista обновлена до версии 0.7.0-preview.6."},
		{name: "сбой обновления — запуск установленной", updateErr: errors.New("нет сети"),
			wantCalls: []call{{"update", "callista"}, {"start", "callista"}}},
		{name: "новая не стартовала — прежняя", version: "0.7.0-preview.6", failStart: 1,
			wantCalls: []call{{"update", "callista"}, {"start", "callista"}, {"rollback", "callista"}, {"start", "callista"}},
			wantMsg:   "Callista 0.7.0-preview.6 не запустилась — оставлена прежняя версия."},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var mu sync.Mutex
			var calls []call
			var msgs []string
			starts := 0
			rec := func(what, key string) { mu.Lock(); calls = append(calls, call{what, key}); mu.Unlock() }
			l := newCompanionLauncher(companionRuntime{
				stat:    func(string) error { return nil },
				running: func(string) bool { return tc.running },
				start: func(c Companion) error {
					rec("start", c.Key)
					starts++
					if starts <= tc.failStart {
						return errors.New("не стартовала")
					}
					return nil
				},
			})
			l.update = func(_ context.Context, c Companion) (string, error) {
				rec("update", c.Key)
				return tc.version, tc.updateErr
			}
			l.rollback = func(c Companion) error { rec("rollback", c.Key); return nil }
			l.notify = func(msg string) { mu.Lock(); msgs = append(msgs, msg); mu.Unlock() }
			got := l.Start([]Companion{{Key: "callista", Path: `C:\kit\companions\callista-operator\callista-operator.exe`,
				UpdateManifest: "https://cc.example/updates/callista/operator-desktop.json"}}, []string{"callista"})
			l.bg.Wait()
			if len(got) != 0 {
				t.Fatalf("синхронные сообщения: %v — фоновое обновление не должно держать окно", got)
			}
			if fmt.Sprint(calls) != fmt.Sprint(tc.wantCalls) {
				t.Fatalf("вызовы %v, ожидались %v", calls, tc.wantCalls)
			}
			if tc.wantMsg == "" && len(msgs) != 0 || tc.wantMsg != "" && (len(msgs) != 1 || msgs[0] != tc.wantMsg) {
				t.Fatalf("сообщения %v, ожидалось %q", msgs, tc.wantMsg)
			}
		})
	}
}

func TestCompanionManifest_UpdateManifestHTTPSOnly(t *testing.T) {
	dir := t.TempDir()
	write := func(manifest string) error {
		body := "companions:\n  callista:\n    exec: companions/callista-operator/callista-operator.exe\n    update:\n      manifest: " + manifest + "\n"
		if err := os.WriteFile(filepath.Join(dir, companionManifestName), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
		_, err := loadCompanionManifest(dir)
		return err
	}
	if err := write("http://cc01.icecorp.ru/updates/callista/operator-desktop.json"); err == nil {
		t.Fatal("адрес списка обновлений по http принят")
	}
	if err := write("https://cc01.icecorp.ru/updates/callista/operator-desktop.json"); err != nil {
		t.Fatalf("https-адрес отвергнут: %v", err)
	}
	list, _ := loadCompanionManifest(dir)
	if len(list) != 1 || list[0].UpdateManifest != "https://cc01.icecorp.ru/updates/callista/operator-desktop.json" {
		t.Fatalf("адрес не дошёл до программы: %+v", list)
	}
	// Путь станции из ibases.yaml главнее, и обновления по нему не ставятся.
	merged := mergeCompanions(list, []Companion{{Key: "callista", Path: `D:\Callista\callista-operator.exe`}})
	if merged[0].UpdateManifest != "" {
		t.Fatal("обновление перенесено на путь станции вне комплекта")
	}
}

func TestCompanionNoticeJS_EscapesText(t *testing.T) {
	js := companionNoticeJS(`Callista "x" </script><img onerror=alert(1)>`)
	if !strings.Contains(js, `textContent=t`) || strings.Contains(js, `"x" </script>`) {
		t.Fatalf("текст уведомления не экранирован: %s", js)
	}
}

// С 0.7.1 Callista публикует установщик setup.exe вместо zip: обновление не
// ставится, установленная версия остаётся, и та же версия не скачивается на
// каждом запуске — до следующей.
func TestCompanionUpdate_InstallerNotZipSkippedUntilNextVersion(t *testing.T) {
	s := newUpdateServer(t)
	setup := []byte("MZ это установщик, а не zip")
	s.publish("0.7.1", setup, "")
	c, dir := installed(t, "0.7.0-preview.6")
	c.UpdateManifest = s.URL + "/updates/callista/operator-desktop.json"
	downloads := 0
	inner := s.Config.Handler
	s.Config.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, ".zip") {
			downloads++
		}
		inner.ServeHTTP(w, r)
	})
	u := testUpdater(s)
	if _, err := u.Update(context.Background(), c); !errors.Is(err, errCompanionNotZip) {
		t.Fatalf("установщик вместо zip: ошибка %v", err)
	}
	if v := fileVersionFromContent(c.Path); v != "0.7.0-preview.6" {
		t.Fatalf("версия изменилась: %q", v)
	}
	if got, err := u.Update(context.Background(), c); got != "" || err != nil || downloads != 1 {
		t.Fatalf("повтор: %q, %v, скачиваний %d — та же версия скачана снова", got, err, downloads)
	}
	s.publish("0.7.2", zipArchive(t, map[string]string{"callista-operator.exe": "0.7.2"}), "")
	if got, err := u.Update(context.Background(), c); got != "0.7.2" || err != nil {
		t.Fatalf("следующая версия (zip) не встала: %q, %v", got, err)
	}
	_ = dir
}

// Ссылка в списке на установщик (.exe) — не скачивается вовсе.
func TestCompanionUpdate_InstallerURLNotDownloaded(t *testing.T) {
	s := newUpdateServer(t)
	s.publish("0.7.1", []byte("MZ"), "")
	s.manifest = strings.Replace(s.manifest, "/updates/callista/downloads/op.zip", "/updates/callista/downloads/callista-operator-windows-0.7.1-x64-setup.exe", 1)
	c, _ := installed(t, "0.7.0-preview.6")
	c.UpdateManifest = s.URL + "/updates/callista/operator-desktop.json"
	if _, err := testUpdater(s).Update(context.Background(), c); !errors.Is(err, errCompanionNotZip) {
		t.Fatalf("ссылка на установщик: %v", err)
	}
	if got, err := testUpdater(s).Update(context.Background(), c); got != "" || err != nil {
		t.Fatalf("повтор той же версии: %q, %v", got, err)
	}
}
