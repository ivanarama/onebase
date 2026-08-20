package selfupdate

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
)

// ghRelJSON собирает ответ GitHub API так, как его отдаёт настоящий сервер:
// имена ассетов совпадают с тем, что кладёт в релиз release.yml.
func ghRelJSON(tag string, prerelease bool, assets ...string) map[string]any {
	list := make([]map[string]any, 0, len(assets))
	for _, name := range assets {
		list = append(list, map[string]any{
			"name":                 name,
			"browser_download_url": "https://example.invalid/" + tag + "/" + name,
			"size":                 1024,
		})
	}
	return map[string]any{
		"tag_name":     tag,
		"body":         "## Изменения\n- что-то поменялось",
		"draft":        false,
		"prerelease":   prerelease,
		"published_at": "2026-08-04T21:54:21Z",
		"html_url":     "https://example.invalid/releases/" + tag,
		"assets":       list,
	}
}

func githubMock(t *testing.T, latest map[string]any, list []map[string]any) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/repos/acme/onebase/releases/latest", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("User-Agent") == "" {
			// GitHub такие запросы отклоняет — ловим регресс здесь, а не в бою.
			http.Error(w, "no user agent", http.StatusForbidden)
			return
		}
		if latest == nil {
			http.Error(w, "not found", http.StatusNotFound)
			return
		}
		_ = json.NewEncoder(w).Encode(latest)
	})
	mux.HandleFunc("/repos/acme/onebase/releases", func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(list)
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

func TestLatestRelease_StableTakesTaggedRelease(t *testing.T) {
	asset := AssetBaseName()
	srv := githubMock(t,
		ghRelJSON("v0.9.8", false, asset, asset+".sha256"),
		[]map[string]any{ghRelJSON("build-672", true, asset, asset+".sha256")},
	)

	rel, err := latestReleaseFrom(context.Background(), srv.URL, "acme/onebase", ChannelStable)
	if err != nil {
		t.Fatalf("LatestRelease: %v", err)
	}
	if rel.Tag != "v0.9.8" {
		t.Fatalf("тег %q, ждали v0.9.8", rel.Tag)
	}
	if rel.AssetName != asset || rel.AssetURL == "" || rel.SHAURL == "" {
		t.Fatalf("ассеты разобраны неверно: %+v", rel)
	}
	if rel.PublishedAt.IsZero() {
		t.Fatal("published_at не разобран")
	}
}

func TestLatestRelease_BuildTakesFirstPrerelease(t *testing.T) {
	asset := AssetBaseName()
	srv := githubMock(t, nil, []map[string]any{
		// Черновик и обычный релиз в канале build игнорируются.
		{"tag_name": "build-673", "draft": true, "prerelease": true},
		ghRelJSON("v0.9.8", false, asset, asset+".sha256"),
		ghRelJSON("build-672", true, asset, asset+".sha256"),
		ghRelJSON("build-671", true, asset, asset+".sha256"),
	})

	rel, err := latestReleaseFrom(context.Background(), srv.URL, "acme/onebase", ChannelBuild)
	if err != nil {
		t.Fatalf("LatestRelease: %v", err)
	}
	if rel.Tag != "build-672" {
		t.Fatalf("тег %q, ждали build-672", rel.Tag)
	}
}

func TestLatestRelease_NoAssetForPlatform(t *testing.T) {
	// Релиз есть, но собран не под нашу платформу.
	srv := githubMock(t, ghRelJSON("v0.9.8", false, "onebase-plan9-mips.zip"), nil)

	_, err := latestReleaseFrom(context.Background(), srv.URL, "acme/onebase", ChannelStable)
	if err == nil {
		t.Fatal("ждали ошибку об отсутствии сборки под платформу")
	}
}

func TestLatestRelease_NoChecksumRefuses(t *testing.T) {
	// Архив есть, .sha256 нет — обновляться вслепую нельзя.
	srv := githubMock(t, ghRelJSON("v0.9.8", false, AssetBaseName()), nil)

	_, err := latestReleaseFrom(context.Background(), srv.URL, "acme/onebase", ChannelStable)
	if err == nil {
		t.Fatal("ждали отказ из-за отсутствия контрольной суммы")
	}
}

func TestLatestRelease_RateLimited(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("X-RateLimit-Remaining", "0")
		http.Error(w, "rate limit exceeded", http.StatusForbidden)
	}))
	t.Cleanup(srv.Close)

	_, err := latestReleaseFrom(context.Background(), srv.URL, "acme/onebase", ChannelStable)
	if !errors.Is(err, ErrRateLimited) {
		t.Fatalf("ждали ErrRateLimited, получили %v", err)
	}
}

func TestLatestRelease_UnknownChannel(t *testing.T) {
	srv := githubMock(t, nil, nil)
	if _, err := latestReleaseFrom(context.Background(), srv.URL, "acme/onebase", Channel("nightly")); err == nil {
		t.Fatal("ждали ошибку о неизвестном канале")
	}
}

func TestNewer(t *testing.T) {
	cases := []struct {
		name             string
		current, cand    string
		want, wantScheme bool
	}{
		{"сборка новее", "build-660", "build-672", true, true},
		{"сборка та же", "build-672", "build-672", false, true},
		{"сборка старее", "build-672", "build-660", false, true},
		{"релиз новее", "v0.9.7", "v0.9.8", true, true},
		{"релиз по минорной", "v0.9.8", "v0.10.0", true, true},
		{"релиз по мажорной", "v0.10.0", "v1.0.0", true, true},
		{"релиз тот же", "v0.9.8", "v0.9.8", false, true},
		{"rc старше релиза", "v0.9.8-rc1", "v0.9.8", true, true},
		{"релиз не откатывается на rc", "v0.9.8", "v0.9.8-rc1", false, true},
		// Смена канала: схемы разные, предлагаем всегда — но UI обязан
		// формулировать это как «канал предлагает», а не «новее».
		{"сборка → релиз", "build-672", "v0.9.8", true, false},
		{"релиз → сборка", "v0.9.8", "build-672", true, false},
		// Локальная сборка разработчика: не трогаем никогда.
		{"dev не обновляем", "dev-cb5276e", "build-672", false, false},
		{"пустая версия не обновляется", "", "build-672", false, false},
		{"мусорный кандидат", "build-660", "какая-то-версия", false, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := Newer(c.current, c.cand); got != c.want {
				t.Errorf("Newer(%q, %q) = %v, ждали %v", c.current, c.cand, got, c.want)
			}
			if got := SameScheme(c.current, c.cand); got != c.wantScheme {
				t.Errorf("SameScheme(%q, %q) = %v, ждали %v", c.current, c.cand, got, c.wantScheme)
			}
		})
	}
}

// KnownVersionScheme отвечает на вопрос «эту версию вообще есть с чем
// сравнивать». Ложь означает, что Newer откажет всегда, и интерфейс обязан
// сказать об этом вслух, а не показывать «установлена актуальная версия».
func TestKnownVersionScheme(t *testing.T) {
	cases := []struct {
		version string
		want    bool
	}{
		{"build-930", true},
		{"v0.10.0", true},
		{"v0.9.8-rc1", true},
		// Настоящий случай из ~/.onebase/updates/state.json: ярлык локальной
		// сборки с суффиксом молча выключил обновления навсегда.
		{"build-793fix", false},
		{"dev-cb5276e", false},
		{"dev", false},
		{"", false},
	}
	for _, c := range cases {
		if got := KnownVersionScheme(c.version); got != c.want {
			t.Errorf("KnownVersionScheme(%q) = %v, ждали %v", c.version, got, c.want)
		}
	}
}
