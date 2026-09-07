package ui

import (
	"context"
	"fmt"
	"path/filepath"
	"testing"

	"github.com/google/uuid"
	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/ivantit66/onebase/internal/project"
	"github.com/ivantit66/onebase/internal/storage"
)

func TestCMSDemoSeed_PreservesClearedPage(t *testing.T) {
	ctx, proj, db, sites, pages := newCMSSeedFixture(t)
	runCMSDemoSeed(t, ctx, proj, db)

	demoSiteID := cmsSeedIDByField(t, ctx, db, sites, "Наименование", "Демо-сайт")
	homeID := cmsSeedPageID(t, ctx, db, pages, demoSiteID, "главная")
	contentColumn := cmsSeedColumn(t, pages, "Содержимое")
	titleColumn := cmsSeedColumn(t, pages, "Заголовок")
	seoTitleColumn := cmsSeedColumn(t, pages, "ЗаголовокSEO")
	query := fmt.Sprintf(
		`UPDATE "%s" SET "%s"=?, "%s"=?, "%s"=? WHERE id=?`,
		metadata.TableName(pages.Name), contentColumn, titleColumn, seoTitleColumn,
	)
	if _, err := db.Exec(ctx, query, "", "Пользовательский заголовок", "Пользовательский SEO", homeID.String()); err != nil {
		t.Fatalf("очистка демо-страницы: %v", err)
	}

	runCMSDemoSeed(t, ctx, proj, db)

	home, err := db.GetByID(ctx, pages.Name, homeID, pages)
	if err != nil {
		t.Fatalf("чтение очищенной страницы: %v", err)
	}
	assertCMSSeedString(t, home, "Содержимое", "")
	assertCMSSeedString(t, home, "Заголовок", "Пользовательский заголовок")
	assertCMSSeedString(t, home, "ЗаголовокSEO", "Пользовательский SEO")
}

func TestCMSDemoSeed_ScopesPageBySiteAndSlug(t *testing.T) {
	ctx, proj, db, sites, pages := newCMSSeedFixture(t)
	neighborContent := "<p>Это демонстрационный сайт на OneBase. Контент правится в разделе «Сайт».</p>"

	neighborSiteID := uuid.New()
	if err := db.Upsert(ctx, sites.Name, neighborSiteID, map[string]any{
		"Наименование": "Соседний сайт",
		"Домен":        "neighbor.example",
		"Активен":      true,
	}, sites); err != nil {
		t.Fatalf("создание соседнего сайта: %v", err)
	}
	neighborPageID := uuid.New()
	if err := db.Upsert(ctx, pages.Name, neighborPageID, map[string]any{
		"Наименование": "Главная",
		"Слаг":         "главная",
		"Заголовок":    "Чужой заголовок",
		"Содержимое":   neighborContent,
		"ЗаголовокSEO": "Чужой SEO",
		"Сайт":         neighborSiteID.String(),
	}, pages); err != nil {
		t.Fatalf("создание страницы соседнего сайта: %v", err)
	}

	runCMSDemoSeed(t, ctx, proj, db)

	neighborPage, err := db.GetByID(ctx, pages.Name, neighborPageID, pages)
	if err != nil {
		t.Fatalf("чтение страницы соседнего сайта: %v", err)
	}
	assertCMSSeedString(t, neighborPage, "Содержимое", neighborContent)
	assertCMSSeedString(t, neighborPage, "Заголовок", "Чужой заголовок")
	assertCMSSeedString(t, neighborPage, "ЗаголовокSEO", "Чужой SEO")

	demoSiteID := cmsSeedIDByField(t, ctx, db, sites, "Наименование", "Демо-сайт")
	demoHomeID := cmsSeedPageID(t, ctx, db, pages, demoSiteID, "главная")
	demoHome, err := db.GetByID(ctx, pages.Name, demoHomeID, pages)
	if err != nil {
		t.Fatalf("чтение главной страницы демо-сайта: %v", err)
	}
	if content, _ := demoHome["Содержимое"].(string); content == "" {
		t.Fatal("главная страница демо-сайта осталась без демо-содержимого")
	}
}

func newCMSSeedFixture(t *testing.T) (context.Context, *project.Project, *storage.DB, *metadata.Entity, *metadata.Entity) {
	t.Helper()
	ctx := context.Background()
	proj, err := project.Load("../../examples/cms")
	if err != nil {
		t.Fatalf("загрузка examples/cms: %v", err)
	}
	t.Cleanup(func() { proj.Close() })

	db, err := storage.ConnectSQLite(ctx, filepath.Join(t.TempDir(), "cms-seed.db"))
	if err != nil {
		t.Fatalf("ConnectSQLite: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	if err := db.Migrate(ctx, proj.Entities); err != nil {
		t.Fatalf("миграция: %v", err)
	}
	if err := db.EnsureAuditSchema(ctx); err != nil {
		t.Fatalf("audit schema: %v", err)
	}
	if err := db.EnsureAttachmentTable(ctx); err != nil {
		t.Fatalf("attachments schema: %v", err)
	}
	if err := db.EnsurePublicFilesSchema(ctx); err != nil {
		t.Fatalf("public files schema: %v", err)
	}
	if err := db.EnsureBlobTable(ctx); err != nil {
		t.Fatalf("blob schema: %v", err)
	}
	if err := db.SaveFileStorageMode(ctx, storage.FileStorageDB); err != nil {
		t.Fatalf("file storage mode: %v", err)
	}

	return ctx, proj, db, cmsSeedEntity(t, proj, "Сайты"), cmsSeedEntity(t, proj, "Страницы")
}

func cmsSeedEntity(t *testing.T, proj *project.Project, name string) *metadata.Entity {
	t.Helper()
	for _, entity := range proj.Entities {
		if entity.Name == name {
			return entity
		}
	}
	t.Fatalf("сущность %s не найдена", name)
	return nil
}

func cmsSeedColumn(t *testing.T, entity *metadata.Entity, fieldName string) string {
	t.Helper()
	for _, field := range entity.Fields {
		if field.Name == fieldName {
			return metadata.ColumnName(field)
		}
	}
	t.Fatalf("реквизит %s.%s не найден", entity.Name, fieldName)
	return ""
}

func cmsSeedIDByField(t *testing.T, ctx context.Context, db *storage.DB, entity *metadata.Entity, fieldName string, value any) uuid.UUID {
	t.Helper()
	query := fmt.Sprintf(
		`SELECT id FROM "%s" WHERE "%s"=?`,
		metadata.TableName(entity.Name), cmsSeedColumn(t, entity, fieldName),
	)
	return cmsSeedScanID(t, db.QueryRow(ctx, query, value), entity.Name)
}

func cmsSeedPageID(t *testing.T, ctx context.Context, db *storage.DB, pages *metadata.Entity, siteID uuid.UUID, slug string) uuid.UUID {
	t.Helper()
	query := fmt.Sprintf(
		`SELECT id FROM "%s" WHERE "%s"=? AND "%s"=?`,
		metadata.TableName(pages.Name), cmsSeedColumn(t, pages, "Сайт"), cmsSeedColumn(t, pages, "Слаг"),
	)
	return cmsSeedScanID(t, db.QueryRow(ctx, query, siteID.String(), slug), pages.Name)
}

type cmsSeedRowScanner interface {
	Scan(dest ...any) error
}

func cmsSeedScanID(t *testing.T, row cmsSeedRowScanner, entityName string) uuid.UUID {
	t.Helper()
	var rawID string
	if err := row.Scan(&rawID); err != nil {
		t.Fatalf("поиск записи %s: %v", entityName, err)
	}
	id, err := uuid.Parse(rawID)
	if err != nil {
		t.Fatalf("некорректный UUID записи %s %q: %v", entityName, rawID, err)
	}
	return id
}

func runCMSDemoSeed(t *testing.T, ctx context.Context, proj *project.Project, db *storage.DB) {
	t.Helper()
	_, runErr, err := RunProcessorOffline(ctx, proj, db, "ЗаполнитьТестовуюБазу", nil, nil)
	if err != nil {
		t.Fatalf("запуск обработки: %v", err)
	}
	if runErr != nil {
		t.Fatalf("выполнение обработки: %v", runErr)
	}
}

func assertCMSSeedString(t *testing.T, row map[string]any, fieldName, want string) {
	t.Helper()
	got, _ := row[fieldName].(string)
	if got != want {
		t.Fatalf("%s = %q, ожидалось %q", fieldName, got, want)
	}
}
