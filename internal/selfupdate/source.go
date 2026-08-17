package selfupdate

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"runtime"
	"strconv"
	"strings"
	"time"
)

// Channel — канал обновлений. Каналы уже разделены самим CI (release.yml):
// push в main публикует pre-release с тегом build-<run_number>, тег v* —
// обычный release. Поэтому здесь не нужен свой реестр версий: достаточно
// правильно прочитать GitHub Releases.
type Channel string

const (
	// ChannelStable — только теги vX.Y.Z (обычные release).
	ChannelStable Channel = "stable"
	// ChannelBuild — сборки из main (pre-release build-NNN). По умолчанию:
	// между релизами накапливаются десятки сборок, и тестирование идёт на них.
	ChannelBuild Channel = "build"
)

// DefaultChannel — канал для новой установки (план 92).
const DefaultChannel = ChannelBuild

// DefaultRepo — откуда берутся сборки. Именно upstream: форки публикуют свои
// build-NNN с независимой нумерацией, и обновляться на них нельзя.
const DefaultRepo = "ivanarama/onebase"

// defaultAPIBase — корень GitHub API. Отдельной переменной, чтобы тесты
// подставляли httptest-сервер и не ходили в сеть.
const defaultAPIBase = "https://api.github.com"

// ErrRateLimited возвращается, когда GitHub ответил 403/429 с исчерпанным
// лимитом. Для вызывающего это «проверить не удалось», а не «обновление
// сломано»: анонимный лимит 60 запросов в час на IP, при проверке раз в
// несколько часов он недостижим, но за NAT предприятия — вполне.
var ErrRateLimited = errors.New("selfupdate: лимит запросов GitHub API исчерпан")

// ErrNoRelease — в канале нет ни одного подходящего релиза.
var ErrNoRelease = errors.New("selfupdate: в канале нет доступных релизов")

// Release — то, что нужно знать об обновлении: чем оно называется, что в нём
// поменялось и откуда скачать сборку под текущую платформу.
type Release struct {
	Tag         string
	Notes       string
	PublishedAt time.Time
	HTMLURL     string
	AssetName   string
	AssetURL    string
	AssetSize   int64
	SHAURL      string
	// SigURL — ассет <архив>.sha256.sig, подпись файла контрольной суммы
	// (#783). Пусто — релиз не подписан; что с этим делать, решает Download по
	// вшитому ключу и режиму (мягкий переход или жёсткий).
	SigURL string
}

// ghRelease/ghAsset — подмножество ответа GitHub API, которое мы читаем.
type ghRelease struct {
	TagName     string    `json:"tag_name"`
	Body        string    `json:"body"`
	Draft       bool      `json:"draft"`
	Prerelease  bool      `json:"prerelease"`
	PublishedAt time.Time `json:"published_at"`
	HTMLURL     string    `json:"html_url"`
	Assets      []ghAsset `json:"assets"`
}

type ghAsset struct {
	Name string `json:"name"`
	URL  string `json:"browser_download_url"`
	Size int64  `json:"size"`
}

// AssetBaseName возвращает имя архива сборки для текущей платформы — ровно то,
// что кладёт в релиз release.yml («Package (Windows)» / «Package (Unix)»).
func AssetBaseName() string {
	name := fmt.Sprintf("onebase-%s-%s", runtime.GOOS, runtime.GOARCH)
	if runtime.GOOS == "windows" {
		return name + ".zip"
	}
	return name + ".tar.gz"
}

// LatestRelease возвращает последний релиз канала для репозитория repo
// (в формате «владелец/имя»).
func LatestRelease(ctx context.Context, repo string, ch Channel) (Release, error) {
	return latestReleaseFrom(ctx, defaultAPIBase, repo, ch)
}

// latestReleaseFrom — то же самое, но с явным корнем API: точка подмены в тестах.
func latestReleaseFrom(ctx context.Context, apiBase, repo string, ch Channel) (Release, error) {
	if strings.TrimSpace(repo) == "" {
		repo = DefaultRepo
	}
	var rel ghRelease
	var err error
	switch ch {
	case ChannelStable:
		// /releases/latest сам исключает draft и pre-release — ровно семантика
		// канала stable.
		rel, err = fetchRelease(ctx, apiBase+"/repos/"+repo+"/releases/latest")
	case ChannelBuild:
		rel, err = fetchLatestPrerelease(ctx, apiBase+"/repos/"+repo+"/releases?per_page=30")
	default:
		return Release{}, fmt.Errorf("selfupdate: неизвестный канал %q (ожидались %q или %q)", ch, ChannelStable, ChannelBuild)
	}
	if err != nil {
		return Release{}, err
	}
	return toRelease(rel)
}

func fetchRelease(ctx context.Context, url string) (ghRelease, error) {
	var rel ghRelease
	if err := getJSON(ctx, url, &rel); err != nil {
		return ghRelease{}, err
	}
	if rel.TagName == "" {
		return ghRelease{}, ErrNoRelease
	}
	return rel, nil
}

func fetchLatestPrerelease(ctx context.Context, url string) (ghRelease, error) {
	var list []ghRelease
	if err := getJSON(ctx, url, &list); err != nil {
		return ghRelease{}, err
	}
	// Список приходит от новых к старым, поэтому берём первый подходящий.
	// Черновики пропускаем: у них ещё нет опубликованных ассетов.
	for _, r := range list {
		if r.Prerelease && !r.Draft && r.TagName != "" {
			return r, nil
		}
	}
	return ghRelease{}, ErrNoRelease
}

// toRelease выбирает из ассетов релиза архив под текущую платформу и файл с
// контрольной суммой рядом с ним.
func toRelease(r ghRelease) (Release, error) {
	want := AssetBaseName()
	out := Release{
		Tag:         r.TagName,
		Notes:       r.Body,
		PublishedAt: r.PublishedAt,
		HTMLURL:     r.HTMLURL,
	}
	for _, a := range r.Assets {
		switch a.Name {
		case want:
			out.AssetName = a.Name
			out.AssetURL = a.URL
			out.AssetSize = a.Size
		case want + ".sha256":
			out.SHAURL = a.URL
		case want + ".sha256.sig":
			out.SigURL = a.URL
		}
	}
	if out.AssetURL == "" {
		return Release{}, fmt.Errorf("selfupdate: в релизе %s нет сборки %s для %s/%s", r.TagName, want, runtime.GOOS, runtime.GOARCH)
	}
	if out.SHAURL == "" {
		return Release{}, fmt.Errorf("selfupdate: в релизе %s нет %s.sha256 — обновление без проверки контрольной суммы запрещено", r.TagName, want)
	}
	return out, nil
}

func getJSON(ctx context.Context, url string, dst any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	req.Header.Set("User-Agent", userAgent())

	resp, err := apiClient().Do(req)
	if err != nil {
		return fmt.Errorf("selfupdate: запрос к %s: %w", url, err)
	}
	defer resp.Body.Close() //nolint:errcheck,gosec // G104: bodyclose распознаёт только прямой вызов; тело прочитано, закрытие вторично

	switch {
	case resp.StatusCode == http.StatusForbidden, resp.StatusCode == http.StatusTooManyRequests:
		// Remaining=0 отличает исчерпанный лимит от «доступ запрещён» (закрытый
		// репозиторий): второе лечится токеном, первое — ожиданием.
		if resp.Header.Get("X-RateLimit-Remaining") == "0" {
			return ErrRateLimited
		}
		return fmt.Errorf("selfupdate: %s вернул %d — нет доступа к репозиторию", url, resp.StatusCode)
	case resp.StatusCode == http.StatusNotFound:
		return fmt.Errorf("selfupdate: %s вернул 404 — проверьте имя репозитория", url)
	case resp.StatusCode != http.StatusOK:
		return fmt.Errorf("selfupdate: %s вернул %d", url, resp.StatusCode)
	}
	// Лимит на тело ответа: список релизов у GitHub небольшой, но полагаться на
	// доброту удалённой стороны в разборе JSON не стоит.
	return json.NewDecoder(io.LimitReader(resp.Body, maxJSONBytes)).Decode(dst)
}

const maxJSONBytes = 8 << 20

// Newer сообщает, стоит ли предлагать candidate тому, кто сейчас работает на
// current. Правила намеренно разные для разных схем версий:
//
//   - build-N против build-M — сравнение чисел (run_number монотонен);
//   - vX.Y.Z против vX.Y.Z — semver;
//   - схемы разные (переключение канала) — предлагаем всегда, но UI обязан
//     сказать «канал stable предлагает v0.9.8», а не «доступна более новая»;
//   - current — локальная dev-сборка: не предлагаем ничего. Пользователь собрал
//     бинарь сам, подменять его нельзя.
func Newer(current, candidate string) bool {
	cur := parseVersion(current)
	cand := parseVersion(candidate)
	if cand.kind == kindUnknown {
		return false
	}
	if cur.kind == kindUnknown {
		// Сюда попадают dev-* и пустая версия (`go build` без ldflags).
		return false
	}
	if cur.kind != cand.kind {
		return true
	}
	return cur.less(cand)
}

// SameScheme сообщает, что версии сравнимы напрямую (обе build-* или обе v*).
// UI выбирает по нему формулировку: «доступна более новая версия» против
// «канал stable предлагает …».
func SameScheme(a, b string) bool {
	va, vb := parseVersion(a), parseVersion(b)
	return va.kind == vb.kind && va.kind != kindUnknown
}

type versionKind int

const (
	kindUnknown versionKind = iota
	kindBuild
	kindSemver
)

type parsedVersion struct {
	kind versionKind
	num  int    // для build-N
	sem  [3]int // для vX.Y.Z
	pre  string // pre-release суффикс semver (-rc1): непустой = версия младше
}

func parseVersion(s string) parsedVersion {
	s = strings.TrimSpace(s)
	if rest, ok := strings.CutPrefix(s, "build-"); ok {
		if n, err := strconv.Atoi(rest); err == nil && n >= 0 {
			return parsedVersion{kind: kindBuild, num: n}
		}
		return parsedVersion{}
	}
	if rest, ok := strings.CutPrefix(s, "v"); ok {
		core, pre, _ := strings.Cut(rest, "-")
		parts := strings.SplitN(core, ".", 3)
		if len(parts) != 3 {
			return parsedVersion{}
		}
		var v parsedVersion
		v.kind = kindSemver
		v.pre = pre
		for i, p := range parts {
			n, err := strconv.Atoi(p)
			if err != nil || n < 0 {
				return parsedVersion{}
			}
			v.sem[i] = n
		}
		return v
	}
	return parsedVersion{}
}

// less сообщает, что v строго старше other (в пределах одной схемы).
func (v parsedVersion) less(other parsedVersion) bool {
	if v.kind == kindBuild {
		return v.num < other.num
	}
	for i := range v.sem {
		if v.sem[i] != other.sem[i] {
			return v.sem[i] < other.sem[i]
		}
	}
	// Числа равны: v0.9.8-rc1 старше v0.9.8, а два разных суффикса сравниваем
	// лексикографически — этого достаточно для rc1/rc2.
	switch {
	case v.pre == other.pre:
		return false
	case v.pre != "" && other.pre == "":
		return true
	case v.pre == "" && other.pre != "":
		return false
	default:
		return v.pre < other.pre
	}
}
