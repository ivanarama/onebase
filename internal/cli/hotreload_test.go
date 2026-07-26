package cli

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/ivantit66/onebase/internal/configdb"
	"github.com/ivantit66/onebase/internal/storage"
)

// TestLatestConfigVersionID проверяет сигнал, по которому database-режим решает,
// что конфигурация изменилась: ID самой свежей версии. Пусто → ""; после каждой
// новой версии — её ID (deploy/rollback создают новую версию).
func TestLatestConfigVersionID(t *testing.T) {
	ctx := context.Background()
	db, err := storage.ConnectSQLite(ctx, filepath.Join(t.TempDir(), "cfg.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	repo := configdb.New(db)
	if err := repo.EnsureSchema(ctx); err != nil {
		t.Fatal(err)
	}

	if id, err := latestConfigVersionID(ctx, repo); err != nil || id != "" {
		if err != nil {
			t.Fatal(err)
		}
		t.Fatalf("пустая история: ждали \"\", получили %q", id)
	}

	v1, err := repo.CreateVersion(ctx, configdb.VersionOptions{Message: "v1"})
	if err != nil {
		t.Fatal(err)
	}
	if id, err := latestConfigVersionID(ctx, repo); err != nil || id != v1.ID {
		if err != nil {
			t.Fatal(err)
		}
		t.Fatalf("после v1: ждали %s, получили %s", v1.ID, id)
	}

	v2, err := repo.CreateVersion(ctx, configdb.VersionOptions{Message: "v2"})
	if err != nil {
		t.Fatal(err)
	}
	if id, err := latestConfigVersionID(ctx, repo); err != nil || id != v2.ID {
		if err != nil {
			t.Fatal(err)
		}
		t.Fatalf("после v2: ждали новейшую %s, получили %s", v2.ID, id)
	}
}

// TestWatchConfigVersions_FiresOnNewVersion проверяет весь цикл поллинга: без
// изменений onChange молчит, а при появлении новой версии (эмуляция deploy) —
// срабатывает.
func TestWatchConfigVersions_FiresOnNewVersion(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())

	db, err := storage.ConnectSQLite(ctx, filepath.Join(t.TempDir(), "cfg.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(db.Close)
	repo := configdb.New(db)
	if err := repo.EnsureSchema(ctx); err != nil {
		t.Fatal(err)
	}
	// Стартовая версия — watcher запомнит её как «последнюю» и звать onChange не будет.
	if _, err := repo.CreateVersion(ctx, configdb.VersionOptions{Message: "v1"}); err != nil {
		t.Fatal(err)
	}

	fired := make(chan struct{}, 4)
	initial, err := latestConfigVersionID(ctx, repo)
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	go func() {
		defer close(done)
		watchConfigVersions(ctx, repo, initial, 10*time.Millisecond, func() error { fired <- struct{}{}; return nil })
	}()
	t.Cleanup(func() {
		cancel()
		<-done
	})

	// Без новой версии onChange не должен срабатывать.
	select {
	case <-fired:
		t.Fatal("onChange сработал без новой версии")
	case <-time.After(60 * time.Millisecond):
	}

	// Новая версия (эмуляция deploy/rollback) → onChange срабатывает.
	if _, err := repo.CreateVersion(ctx, configdb.VersionOptions{Message: "v2"}); err != nil {
		t.Fatal(err)
	}
	select {
	case <-fired:
	case <-time.After(2 * time.Second):
		t.Fatal("onChange не сработал после новой версии")
	}
}

func TestWatchConfigVersions_RetriesFailedReload(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	db, err := storage.ConnectSQLite(ctx, filepath.Join(t.TempDir(), "cfg.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(db.Close)
	repo := configdb.New(db)
	if err := repo.EnsureSchema(ctx); err != nil {
		t.Fatal(err)
	}
	_, _ = repo.CreateVersion(ctx, configdb.VersionOptions{Message: "v1"})
	failed := make(chan struct{}, 1)
	succeeded := make(chan struct{}, 1)
	attempts := 0
	initial, err := latestConfigVersionID(ctx, repo)
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	go func() {
		defer close(done)
		watchConfigVersions(ctx, repo, initial, 10*time.Millisecond, func() error {
			attempts++
			if attempts == 1 {
				failed <- struct{}{}
				return context.DeadlineExceeded
			}
			succeeded <- struct{}{}
			return nil
		})
	}()
	t.Cleanup(func() {
		cancel()
		<-done
	})
	_, _ = repo.CreateVersion(ctx, configdb.VersionOptions{Message: "v2"})
	select {
	case <-failed:
	case <-time.After(time.Second):
		t.Fatal("first reload attempt did not happen")
	}
	select {
	case <-succeeded:
	case <-time.After(time.Second):
		t.Fatal("failed reload was not retried")
	}
}
