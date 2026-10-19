package ui

import (
	"context"
	"fmt"

	"github.com/ivantit66/onebase/internal/auth"
	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/ivantit66/onebase/internal/storage"
)

// CMS orders reference the responsible login account. Match CLI startup: the
// auth table must exist before configuration migration, including on PostgreSQL.
func migrateCMSFixture(ctx context.Context, db *storage.DB, entities []*metadata.Entity) error {
	if err := auth.NewRepo(db).EnsureSchema(ctx); err != nil {
		return fmt.Errorf("CMS auth schema: %w", err)
	}
	return db.Migrate(ctx, entities)
}
