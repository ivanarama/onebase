package devserver

import (
	"context"
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"runtime/debug"
	"strings"
	"time"

	"github.com/fsnotify/fsnotify"
	oblog "github.com/ivantit66/onebase/internal/logging"
)

func watcherLog() *slog.Logger {
	return oblog.Component("devserver.watcher")
}

// watchOpts настраивает, что именно наблюдается.
//
//	accept  — пускать ли событие по этому пути к onChange (nil = пускать всё);
//	skipDir — не брать каталог в наблюдение и не спускаться в него вовсе
//	          (nil = брать всё дерево). Отличается от accept тем, что экономит
//	          дескрипторы наблюдения: у .git и node_modules подкаталогов больше,
//	          чем у самого проекта, а изменений, ради которых стоит перезагружаться,
//	          в них не бывает.
type watchOpts struct {
	accept  func(path string, isDir bool, op fsnotify.Op) bool
	skipDir func(path string) bool
	// watchDirs are additional directories watched non-recursively. They cover
	// manifest inputs in skipped subtrees plus go.work/local-replace modules
	// outside the primary source tree.
	watchDirs []string
	// debounce runs after an accepted event has settled. It can refresh dynamic
	// filters and add newly discovered external input directories before deciding
	// whether the public callback is necessary.
	debounce func() (notify bool, watchDirs []string)
}

// WatchContext watches dir and all its subdirectories, calling onChange after
// a debounce period. fsnotify is not recursive, so every subdirectory is added
// explicitly; directories created later are picked up on the fly.
//
// done is closed after the watcher goroutine and any running callback have
// stopped, so callers can safely release resources referenced by onChange.
func WatchContext(ctx context.Context, dir string, onChange func()) (<-chan struct{}, error) {
	return watchContext(ctx, dir, watchOpts{}, onChange)
}

// WatchProjectContext watches only source/config files that can affect a
// loaded project. Generated backups, databases and editor artifacts inside the
// project tree therefore do not cause pointless reloads.
func WatchProjectContext(ctx context.Context, dir string, onChange func()) (<-chan struct{}, error) {
	projectDirs := map[string]struct{}{
		"accountregs": {}, "accounts": {}, "catalogs": {}, "config": {},
		"constants": {}, "documents": {}, "enums": {}, "exchange": {},
		"inforegs": {}, "journals": {}, "locales": {}, "pages": {},
		"printforms": {}, "processors": {}, "registers": {}, "reports": {},
		"roles": {}, "scheduled": {}, "services": {}, "src": {},
		"subsystems": {}, "widgets": {},
	}
	return watchContext(ctx, dir, watchOpts{accept: func(path string, isDir bool, _ fsnotify.Op) bool {
		if isDir {
			rel, err := filepath.Rel(dir, path)
			if err != nil || rel == "." || strings.HasPrefix(rel, "..") {
				return false
			}
			top := strings.ToLower(strings.Split(filepath.ToSlash(rel), "/")[0])
			_, ok := projectDirs[top]
			return ok
		}
		lower := strings.ToLower(path)
		return strings.HasSuffix(lower, ".yaml") ||
			strings.HasSuffix(lower, ".yml") ||
			strings.HasSuffix(lower, ".os")
	}}, onChange)
}

// WatchGoContext наблюдает за входами сборки платформы, полученными из `go list`:
// Go/cgo/assembly sources, module/workspace files and assets matched by go:embed.
// Он нужен режиму пересборки (`onebase dev --reload-binary`), где изменение
// этих входов запускает `go build` и перезапуск процесса сервера.
//
// Каталоги, где входов сборки обычно не бывает (.git с его тысячами объектов,
// node_modules, служебные .idea/.vscode), обходятся стороной; отдельные файлы из
// них, если `go list` всё же назвал их входами, добавляются явно.
func WatchGoContext(ctx context.Context, dir string, onChange func()) (<-chan struct{}, error) {
	tracker := newGoBuildInputTracker(ctx, dir)
	return watchContext(ctx, dir, watchOpts{
		accept:    tracker.accept,
		skipDir:   skipGoDir,
		watchDirs: tracker.watchDirs(),
		debounce:  tracker.refresh,
	}, onChange)
}

// skipGoDir отсеивает каталоги, которые не содержат исходников платформы.
func skipGoDir(path string) bool {
	base := filepath.Base(path)
	if base == "node_modules" {
		return true
	}
	// Скрытые каталоги: .git, .idea, .vscode, .github/workflows тоже (yaml CI
	// на бинарь не влияет). Точка и «..» скрытыми не считаются.
	return len(base) > 1 && strings.HasPrefix(base, ".") && base != ".."
}

// debounceWindow — сколько тишины ждать после правки, прежде чем звать onChange.
// Переменная, а не константа, чтобы тест мог задать заведомо широкое окно:
// проверка схлопывания серии правок иначе зависит от планировщика раннера, и на
// нагруженном CI пауза между записями растягивалась за окно, разбивая серию на
// два вызова (флейк TestWatchContext_Debounces).
var debounceWindow = 300 * time.Millisecond

func watchContext(ctx context.Context, dir string, opts watchOpts, onChange func()) (<-chan struct{}, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if onChange == nil {
		return nil, fmt.Errorf("devserver: onChange callback is nil")
	}
	accept := opts.accept
	w, err := fsnotify.NewWatcher()
	if err != nil {
		return nil, err
	}

	watchedDirs := make(map[string]struct{})
	addDir := func(path string) error {
		path = filepath.Clean(path)
		if _, ok := watchedDirs[path]; ok {
			return nil
		}
		if err := w.Add(path); err != nil {
			return err
		}
		watchedDirs[path] = struct{}{}
		return nil
	}
	// addTree рекурсивно добавляет root и все его подкаталоги в наблюдение.
	addTree := func(root string) error {
		return filepath.WalkDir(root, func(path string, d fs.DirEntry, walkErr error) error {
			if walkErr != nil {
				return walkErr
			}
			if d.IsDir() {
				// Сам root пропускать нельзя: его выбрал вызывающий, а не обход.
				if opts.skipDir != nil && path != root && opts.skipDir(path) {
					return fs.SkipDir
				}
				if err := addDir(path); err != nil {
					return fmt.Errorf("watch %s: %w", path, err)
				}
			}
			return nil
		})
	}
	if err := addTree(dir); err != nil {
		_ = w.Close()
		return nil, fmt.Errorf("devserver: watch tree: %w", err)
	}
	addWatchDirs := func(dirs []string) {
		for _, extra := range dirs {
			if extra == "" {
				continue
			}
			if err := addDir(extra); err != nil {
				watcherLog().Warn("watch additional build-input directory failed", "path", extra, "err", err)
			}
		}
	}
	addWatchDirs(opts.watchDirs)

	debounce := time.NewTimer(0)
	<-debounce.C // drain initial tick
	resetDebounce := func() {
		if !debounce.Stop() {
			select {
			case <-debounce.C:
			default:
			}
		}
		debounce.Reset(debounceWindow)
	}

	done := make(chan struct{})
	go func() {
		defer close(done)
		defer oblog.CloseQuiet("devserver", "наблюдатель за файлами", w)
		defer debounce.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case event, ok := <-w.Events:
				if !ok {
					return
				}
				// Новый подкаталог — начинаем следить и за ним, иначе
				// файлы внутри него не отслеживались бы.
				eventPath := filepath.Clean(event.Name)
				_, wasDir := watchedDirs[eventPath]
				isDir := wasDir
				if event.Has(fsnotify.Create) {
					if fi, statErr := os.Stat(event.Name); statErr == nil && fi.IsDir() {
						isDir = true
						// Каталог из чёрного списка не берём и созданным
						// на ходу: правило одно и то же, откуда бы каталог
						// ни взялся (`git clone` подкаталога, распаковка).
						if opts.skipDir == nil || !opts.skipDir(event.Name) {
							if err := addTree(event.Name); err != nil {
								watcherLog().Warn("watch new directory failed", "path", event.Name, "err", err)
							}
						}
					}
				}
				if wasDir && (event.Has(fsnotify.Remove) || event.Has(fsnotify.Rename)) {
					prefix := eventPath + string(filepath.Separator)
					for watched := range watchedDirs {
						if watched == eventPath || strings.HasPrefix(watched, prefix) {
							delete(watchedDirs, watched)
						}
					}
				}
				if event.Has(fsnotify.Write) || event.Has(fsnotify.Create) ||
					event.Has(fsnotify.Remove) || event.Has(fsnotify.Rename) {
					if accept != nil && !accept(event.Name, isDir, event.Op) {
						continue
					}
					resetDebounce()
				}
			case err, ok := <-w.Errors:
				if !ok {
					return
				}
				watcherLog().Warn("watcher error", "err", err)
			case <-debounce.C:
				notify := true
				if opts.debounce != nil {
					var dirs []string
					notify, dirs = opts.debounce()
					addWatchDirs(dirs)
				}
				if !notify {
					continue
				}
				func() {
					defer func() {
						if recovered := recover(); recovered != nil {
							watcherLog().Error("reload callback panic",
								"panic", recovered,
								"stack", string(debug.Stack()),
							)
						}
					}()
					onChange()
				}()
			}
		}
	}()
	return done, nil
}
