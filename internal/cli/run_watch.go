package cli

import (
	"context"
	"fmt"
	"log/slog"
	"sync"

	"github.com/ivantit66/onebase/internal/api"
	"github.com/ivantit66/onebase/internal/configdb"
	"github.com/ivantit66/onebase/internal/devserver"
	"github.com/ivantit66/onebase/internal/project"
	"github.com/ivantit66/onebase/internal/runtime"
	"github.com/ivantit66/onebase/internal/scheduler"
)

func startServerWatch(ctx context.Context, enabled bool, configSource, dir string, cfgRepo *configdb.Repo, loadedVersion string, reg *runtime.Registry, sched *scheduler.Scheduler, srv *api.Server, log *slog.Logger) func() {
	if !enabled {
		return nil
	}
	var reloadMu sync.Mutex
	applyProject := func(newProj *project.Project) error {
		defer newProj.Close()
		reloadMu.Lock()
		defer reloadMu.Unlock()
		if _, err := project.LoadConfig(newProj.Dir); err != nil {
			return fmt.Errorf("validate app config: %w", err)
		}
		return reloadProjectRuntime(reg, sched, srv, newProj)
	}

	switch configSource {
	case "file":
		watchCtx, cancel := context.WithCancel(ctx)
		done, err := devserver.WatchProjectContext(watchCtx, dir, func() {
			newProj, err := project.Load(dir)
			if err != nil {
				log.Warn("watch reload failed", "err", err)
				return
			}
			if err := applyProject(newProj); err != nil {
				log.Warn("watch publish failed", "err", err)
				return
			}
			outln("[watch] метаданные и расписания перезагружены; app.yaml/roles/locales требуют рестарта")
		})
		if err != nil {
			cancel()
			log.Warn("watch init failed", "err", err)
			return nil
		}
		outf("[watch] отслеживаем %s — metadata/DSL/scheduled подхватятся без рестарта\n", dir)
		return onceStop(cancel, done)
	case "database":
		watchCtx, cancel := context.WithCancel(ctx)
		done := make(chan struct{})
		go func() {
			defer close(done)
			watchConfigVersions(watchCtx, cfgRepo, loadedVersion, configReloadInterval, func() error {
				newProj, err := project.LoadFromDB(watchCtx, cfgRepo)
				if err != nil {
					log.Warn("db watch reload failed", "err", err)
					return err
				}
				if err := applyProject(newProj); err != nil {
					log.Warn("db watch publish failed", "err", err)
					return err
				}
				outln("[watch] metadata/DSL/scheduled перезагружены из БД; app.yaml/roles/locales требуют рестарта")
				return nil
			})
		}()
		outln("[watch] отслеживаем версии конфигурации в БД — deploy подхватится без рестарта")
		return onceStop(cancel, done)
	default:
		return nil
	}
}

func onceStop(cancel context.CancelFunc, done <-chan struct{}) func() {
	var once sync.Once
	return func() {
		once.Do(func() {
			cancel()
			<-done
		})
	}
}
