package launcher

import (
	"archive/zip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// Обновление сопутствующей программы из списка обновлений её сервера
// (update.manifest в companions.yaml). Формат — как у Callista Operator:
//
//	{"version": "0.7.0-preview.6",
//	 "downloads": {"win32": {"url": "/updates/…zip", "size": 123, "sha256": "…"}}}
//
// Порядок (handoff Callista §2): новая версия распаковывается целиком в
// отдельную папку, проверенная по размеру и SHA-256; только потом папка
// программы переименовывается в «.прежняя», а новая встаёт на её место. Путь в
// companions.yaml не меняется: exe новой версии получает имя прежнего.
// Обновление никогда не понижает версию и не трогает работающую программу —
// может идти разговор. Любой сбой оставляет установленную версию как есть.

const (
	companionUpdatePrevSuffix    = ".прежняя"
	companionUpdateStageSuffix   = ".обновление"
	companionUpdateManifestLimit = 64 << 10
	companionUpdateMaxSize       = 1 << 30 // архив больше 1 ГиБ — не программа телефонии
	companionUpdateMaxFiles      = 20000
	companionUpdateLockTTL       = 30 * time.Minute
	// companionUpdateSkipSuffix — версия, которую не поставить в принципе
	// (на сервере не zip, а, например, установщик): её не скачивать снова.
	companionUpdateSkipSuffix = ".обновление-пропущено"
)

// errCompanionNotZip — на сервере не zip-архив программы. С 0.7.1 Callista
// публикует установщик setup.exe: распаковать его лаунчер не может.
var errCompanionNotZip = errors.New("архив обновления не zip (вероятно, установщик) — такой формат лаунчер не ставит")

type companionUpdateManifest struct {
	Version   string `json:"version"`
	Downloads map[string]struct {
		URL    string `json:"url"`
		Size   int64  `json:"size"`
		SHA256 string `json:"sha256"`
	} `json:"downloads"`
}

// companionUpdater — то, чем обновление трогает сеть и систему; тест подменяет.
type companionUpdater struct {
	client  *http.Client
	version func(path string) string // версия установленного exe; "" — неизвестна
	now     func() time.Time
}

var systemCompanionUpdater = &companionUpdater{
	client:  &http.Client{Timeout: 15 * time.Minute},
	version: companionFileVersion,
	now:     time.Now,
}

// Update ставит версию из списка обновлений, если она новее установленной.
// Возвращает установленную версию или "" — обновлять нечего.
func (u *companionUpdater) Update(ctx context.Context, c Companion) (string, error) {
	if c.UpdateManifest == "" {
		return "", nil
	}
	installed := strings.TrimSpace(u.version(c.Path))
	if installed == "" {
		// Версию не прочитать — сравнить не с чем, а понижать нельзя.
		return "", fmt.Errorf("версия установленной программы %s не определена", c.Path)
	}
	m, base, err := u.fetchManifest(ctx, c.UpdateManifest)
	if err != nil {
		return "", err
	}
	if !semverNewer(m.Version, installed) {
		return "", nil
	}
	skip := filepath.Join(filepath.Dir(filepath.Dir(c.Path)), filepath.Base(filepath.Dir(c.Path))+companionUpdateSkipSuffix)
	if b, err := os.ReadFile(skip); err == nil && strings.TrimSpace(string(b)) == m.Version { //nolint:gosec // G304: путь собран из каталога комплекта
		// Эта версия уже скачивалась и оказалась не того вида (не zip):
		// не качать её на каждом запуске. Следующая версия — снова пробуем.
		return "", nil
	}
	dl, ok := m.Downloads["win32"]
	if !ok || dl.URL == "" || dl.Size <= 0 || dl.Size > companionUpdateMaxSize || len(dl.SHA256) != 64 {
		return "", fmt.Errorf("список обновлений %s: нет корректной сборки win32 для %s", c.UpdateManifest, m.Version)
	}
	archiveURL, err := base.Parse(dl.URL)
	if err != nil || archiveURL.Scheme != "https" || !strings.EqualFold(archiveURL.Host, base.Host) {
		// Архив — только с того же сервера, что и список: иначе подменённый
		// список направил бы станцию за чужим файлом.
		return "", fmt.Errorf("список обновлений %s: архив не с того же сервера (%s)", c.UpdateManifest, dl.URL)
	}

	if strings.HasSuffix(strings.ToLower(archiveURL.Path), ".exe") {
		// Установщик, а не архив: качать его незачем — поставить не сможем.
		_ = os.WriteFile(skip, []byte(m.Version), 0o600)
		return "", errCompanionNotZip
	}
	dir := filepath.Dir(c.Path)
	parent, name := filepath.Dir(dir), filepath.Base(dir)
	unlock, err := u.lock(filepath.Join(parent, name+companionUpdateStageSuffix+".lock"))
	if err != nil {
		return "", err
	}
	defer unlock()

	stage := filepath.Join(parent, name+companionUpdateStageSuffix)
	archive := stage + ".zip"
	_ = os.RemoveAll(stage)
	defer os.Remove(archive) //nolint:errcheck // временный архив: остаток не мешает следующей попытке
	if err := u.download(ctx, archiveURL.String(), archive, dl.Size, dl.SHA256); err != nil {
		return "", err
	}
	if err := extractCompanionArchive(archive, stage); err != nil {
		_ = os.RemoveAll(stage)
		if errors.Is(err, errCompanionNotZip) {
			_ = os.WriteFile(skip, []byte(m.Version), 0o600)
		}
		return "", err
	}
	if err := placeCompanionExe(stage, filepath.Base(c.Path)); err != nil {
		_ = os.RemoveAll(stage)
		return "", err
	}
	if got := strings.TrimSpace(u.version(filepath.Join(stage, filepath.Base(c.Path)))); got != "" && got != m.Version {
		_ = os.RemoveAll(stage)
		return "", fmt.Errorf("в архиве версия %s, а список обновлений обещает %s", got, m.Version)
	}
	if err := swapCompanionDirs(dir, stage); err != nil {
		_ = os.RemoveAll(stage)
		return "", err
	}
	return m.Version, nil
}

func (u *companionUpdater) fetchManifest(ctx context.Context, raw string) (companionUpdateManifest, *url.URL, error) {
	var m companionUpdateManifest
	base, err := url.Parse(raw)
	if err != nil || base.Scheme != "https" {
		return m, nil, fmt.Errorf("адрес списка обновлений %q — не https", raw)
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, raw, nil)
	if err != nil {
		return m, nil, err
	}
	resp, err := u.client.Do(req)
	if err != nil {
		return m, nil, fmt.Errorf("список обновлений %s недоступен: %w", raw, err)
	}
	defer resp.Body.Close() //nolint:errcheck // тело прочитано ниже
	if resp.StatusCode != http.StatusOK {
		return m, nil, fmt.Errorf("список обновлений %s: ответ %d", raw, resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, companionUpdateManifestLimit+1))
	if err != nil {
		return m, nil, err
	}
	if len(body) > companionUpdateManifestLimit {
		return m, nil, fmt.Errorf("список обновлений %s больше %d КиБ", raw, companionUpdateManifestLimit>>10)
	}
	if err := json.Unmarshal(body, &m); err != nil {
		return m, nil, fmt.Errorf("список обновлений %s: %w", raw, err)
	}
	if _, ok := parseSemver(m.Version); !ok {
		return m, nil, fmt.Errorf("список обновлений %s: версия %q не в формате семантической версии", raw, m.Version)
	}
	return m, base, nil
}

// download скачивает архив во временный файл, сверяя размер и SHA-256 на лету:
// больше обещанного не читается.
func (u *companionUpdater) download(ctx context.Context, raw, path string, size int64, sum string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, raw, nil)
	if err != nil {
		return err
	}
	resp, err := u.client.Do(req)
	if err != nil {
		return fmt.Errorf("архив обновления %s недоступен: %w", raw, err)
	}
	defer resp.Body.Close() //nolint:errcheck // тело читается ниже
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("архив обновления %s: ответ %d", raw, resp.StatusCode)
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o600) //nolint:gosec // G304: путь собран из каталога комплекта
	if err != nil {
		return err
	}
	h := sha256.New()
	n, copyErr := io.Copy(io.MultiWriter(f, h), io.LimitReader(resp.Body, size+1))
	closeErr := f.Close()
	if copyErr != nil {
		return fmt.Errorf("архив обновления %s: %w", raw, copyErr)
	}
	if closeErr != nil {
		return closeErr
	}
	if n != size {
		return fmt.Errorf("архив обновления %s: размер %d, ожидался %d", raw, n, size)
	}
	if got := hex.EncodeToString(h.Sum(nil)); !strings.EqualFold(got, sum) {
		return fmt.Errorf("архив обновления %s: SHA-256 не совпал", raw)
	}
	return nil
}

// lock — одно обновление на папку: второе окно Предприятия не скачивает ту же
// версию параллельно. Брошенная блокировка (сбой посреди обновления) старше
// получаса снимается.
func (u *companionUpdater) lock(path string) (func(), error) {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600) //nolint:gosec // G304: путь собран из каталога комплекта
	if errors.Is(err, os.ErrExist) {
		if info, statErr := os.Stat(path); statErr == nil && u.now().Sub(info.ModTime()) > companionUpdateLockTTL {
			_ = os.Remove(path)
			f, err = os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600) //nolint:gosec // G304: см. выше
		}
	}
	if err != nil {
		return nil, fmt.Errorf("обновление уже идёт (%s): %w", path, err)
	}
	_ = f.Close()
	return func() { _ = os.Remove(path) }, nil
}

// extractCompanionArchive распаковывает zip целиком (exe, DLL, resources).
// Пути за пределы папки, абсолютные и с диском отвергаются.
func extractCompanionArchive(archive, dst string) error {
	r, err := zip.OpenReader(archive)
	if err != nil {
		if errors.Is(err, zip.ErrFormat) {
			return errCompanionNotZip
		}
		return fmt.Errorf("архив обновления: %w", err)
	}
	defer r.Close() //nolint:errcheck // только чтение
	if len(r.File) > companionUpdateMaxFiles {
		return fmt.Errorf("архив обновления: больше %d файлов", companionUpdateMaxFiles)
	}
	var total uint64
	for _, f := range r.File {
		rel := filepath.Clean(filepath.FromSlash(f.Name))
		if rel == "." || filepath.IsAbs(rel) || strings.Contains(rel, ":") || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			return fmt.Errorf("архив обновления: недопустимый путь %q", f.Name)
		}
		target := filepath.Join(dst, rel)
		if f.FileInfo().IsDir() {
			if err := os.MkdirAll(target, 0o755); err != nil {
				return err
			}
			continue
		}
		total += f.UncompressedSize64
		if total > 4*companionUpdateMaxSize {
			return errors.New("архив обновления: распакованный объём больше 4 ГиБ")
		}
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return err
		}
		if err := extractCompanionFile(f, target); err != nil {
			return err
		}
	}
	return nil
}

func extractCompanionFile(f *zip.File, target string) error {
	src, err := f.Open()
	if err != nil {
		return err
	}
	defer src.Close()                                                         //nolint:errcheck // только чтение
	out, err := os.OpenFile(target, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644) //nolint:gosec // G304: путь проверен в extractCompanionArchive
	if err != nil {
		return err
	}
	_, copyErr := io.Copy(out, io.LimitReader(src, int64(f.UncompressedSize64)+1)) //nolint:gosec // G115: размер ограничен суммой выше
	closeErr := out.Close()
	if copyErr != nil {
		return copyErr
	}
	return closeErr
}

// placeCompanionExe — exe новой версии получает имя прежнего (в архиве имя с
// версией: callista-operator-0.7.0-preview.6.exe). Ровно один кандидат в
// корне папки, иначе это не тот архив.
func placeCompanionExe(dir, exeName string) error {
	if _, err := os.Stat(filepath.Join(dir, exeName)); err == nil {
		return nil
	}
	prefix := strings.ToLower(strings.TrimSuffix(exeName, filepath.Ext(exeName)))
	entries, err := os.ReadDir(dir)
	if err != nil {
		return err
	}
	var found []string
	for _, e := range entries {
		n := strings.ToLower(e.Name())
		if !e.IsDir() && strings.HasSuffix(n, ".exe") && strings.HasPrefix(n, prefix) {
			found = append(found, e.Name())
		}
	}
	if len(found) != 1 {
		return fmt.Errorf("в архиве обновления %d программ %s*.exe, ожидалась одна", len(found), prefix)
	}
	return os.Rename(filepath.Join(dir, found[0]), filepath.Join(dir, exeName))
}

// swapCompanionDirs: папка программы → «.прежняя» (прежняя «.прежняя»
// удаляется), новая — на её место. Неудача второго шага возвращает первую.
func swapCompanionDirs(dir, stage string) error {
	prev := dir + companionUpdatePrevSuffix
	if err := os.RemoveAll(prev); err != nil {
		return fmt.Errorf("не удалось убрать %s: %w", prev, err)
	}
	if err := os.Rename(dir, prev); err != nil {
		return fmt.Errorf("не удалось отложить %s: %w", dir, err)
	}
	if err := os.Rename(stage, dir); err != nil {
		if backErr := os.Rename(prev, dir); backErr != nil {
			return fmt.Errorf("новая версия не встала (%v), и прежняя не вернулась: %w", err, backErr)
		}
		return fmt.Errorf("новая версия не встала: %w", err)
	}
	return nil
}

// rollbackCompanionUpdate возвращает «.прежнюю» на место, если новая версия не
// запустилась. Неудачная остаётся рядом — «.неудачная» — для разбора.
func rollbackCompanionUpdate(c Companion) error {
	dir := filepath.Dir(c.Path)
	prev := dir + companionUpdatePrevSuffix
	if _, err := os.Stat(prev); err != nil {
		return err
	}
	failed := dir + ".неудачная"
	_ = os.RemoveAll(failed)
	if err := os.Rename(dir, failed); err != nil {
		return err
	}
	return os.Rename(prev, dir)
}

// semverNewer — a новее b по правилам SemVer 2.0: числа по порядку, версия
// без суффикса новее своей предварительной (0.7.0 > 0.7.0-preview.9),
// числовые части суффикса сравниваются как числа (preview.10 > preview.9).
// Неразборчивая версия новее не считается — понижать и гадать нельзя.
func semverNewer(a, b string) bool {
	va, okA := parseSemver(a)
	vb, okB := parseSemver(b)
	if !okA || !okB {
		return false
	}
	return compareSemver(va, vb) > 0
}

type semver struct {
	core [3]int
	pre  []string
}

func parseSemver(s string) (semver, bool) {
	var v semver
	s = strings.TrimPrefix(strings.TrimSpace(s), "v")
	if i := strings.IndexByte(s, '+'); i >= 0 {
		s = s[:i]
	}
	core, pre, hasPre := strings.Cut(s, "-")
	parts := strings.Split(core, ".")
	if len(parts) != 3 {
		return v, false
	}
	for i, p := range parts {
		n, err := strconv.Atoi(p)
		if err != nil || n < 0 {
			return v, false
		}
		v.core[i] = n
	}
	if hasPre {
		if pre == "" {
			return v, false
		}
		v.pre = strings.Split(pre, ".")
		for _, id := range v.pre {
			if id == "" {
				return v, false
			}
		}
	}
	return v, true
}

func compareSemver(a, b semver) int {
	for i := 0; i < 3; i++ {
		if a.core[i] != b.core[i] {
			if a.core[i] > b.core[i] {
				return 1
			}
			return -1
		}
	}
	switch {
	case len(a.pre) == 0 && len(b.pre) == 0:
		return 0
	case len(a.pre) == 0:
		return 1
	case len(b.pre) == 0:
		return -1
	}
	for i := 0; i < len(a.pre) && i < len(b.pre); i++ {
		x, y := a.pre[i], b.pre[i]
		nx, errX := strconv.Atoi(x)
		ny, errY := strconv.Atoi(y)
		switch {
		case errX == nil && errY == nil:
			if nx != ny {
				if nx > ny {
					return 1
				}
				return -1
			}
		case errX == nil:
			return -1 // числовой идентификатор младше буквенного
		case errY == nil:
			return 1
		default:
			if x != y {
				if x > y {
					return 1
				}
				return -1
			}
		}
	}
	switch {
	case len(a.pre) > len(b.pre):
		return 1
	case len(a.pre) < len(b.pre):
		return -1
	}
	return 0
}

// companionNoticeJS — скрипт уведомления в окне Предприятия. Самодостаточный:
// страница может прийти с сервера любой версии, поэтому от её функций он не
// зависит. Текст передаётся как JSON-строка и вставляется через textContent.
func companionNoticeJS(msg string) string {
	text, _ := json.Marshal(msg)
	return `(function(t){if(!document.body)return;var b=document.createElement('div');` +
		`b.setAttribute('role','status');b.setAttribute('data-ob-launcher-notice','1');` +
		`b.style.cssText='position:fixed;right:16px;bottom:16px;z-index:2147483000;max-width:420px;background:#1e3a5f;color:#fff;padding:10px 32px 10px 12px;border-radius:8px;font-size:13px;box-shadow:0 6px 16px rgba(0,0,0,.3)';` +
		`b.textContent=t;var x=document.createElement('button');x.type='button';x.textContent='×';x.setAttribute('aria-label','Закрыть');` +
		`x.style.cssText='position:absolute;top:4px;right:8px;background:none;border:none;color:#fff;font-size:18px;cursor:pointer';` +
		`x.addEventListener('click',function(){b.remove();});b.appendChild(x);document.body.appendChild(b);` +
		`setTimeout(function(){b.remove();},15000);})(` + string(text) + `);`
}
