package storage

import (
	"context"
	"fmt"
	"strings"

	"github.com/ivantit66/onebase/internal/metadata"
)

// Slice B of plan 203 covers PostgreSQL entity headers only. Table parts,
// registers and SQLite evolution have separate migration scopes in that plan.
func headerUsersReferences(e *metadata.Entity) []UsersReference {
	header := *e
	header.TableParts = nil
	return UsersReferences([]*metadata.Entity{&header}, nil, nil, nil)
}

// Lock the target before the source. The surrounding per-entity transaction
// keeps both locks through validation, all column/map changes, and FK DDL.
// The migration advisory lock serializes cooperating migrations; these table
// locks also exclude ordinary writers and account deletion during preflight.
func (db *DB) lockHeaderUsersTarget(ctx context.Context, refs []UsersReference) error {
	if !db.IsPostgres() || len(refs) == 0 {
		return nil
	}
	exists, err := db.usersReferenceTableExists(ctx, metadata.SystemUsersEntity)
	if err != nil {
		return err
	}
	if !exists {
		return fmt.Errorf("ссылки на _users: таблица _users отсутствует; сначала подготовьте auth-схему")
	}
	schema, err := db.entityIndexSchema(ctx)
	if err != nil {
		return err
	}
	_, err = db.Exec(ctx, "LOCK TABLE "+quoteIdent(schema)+"."+quoteIdent(metadata.SystemUsersEntity)+" IN SHARE ROW EXCLUSIVE MODE")
	return err
}

func (db *DB) checkHeaderUsersReferences(ctx context.Context, refs []UsersReference) ([]UsersReferencePlan, error) {
	plans, err := db.PlanUsersReferenceConstraints(ctx, refs)
	if err != nil {
		return nil, err
	}
	for _, p := range plans {
		if p.BrokenCount > 0 {
			// Only source record IDs are exposed, never the invalid values or user data.
			return nil, fmt.Errorf("%s (%s.%s): битых ссылок на _users: %d; записи: %s; исправьте данные явно и повторите миграцию",
				p.Reference.Object, p.Reference.Table, p.ActualColumn, p.BrokenCount, strings.Join(p.RecordIDs, ", "))
		}
		if p.State == "incompatible-fk" {
			return nil, fmt.Errorf("%s", p.String())
		}
	}
	return plans, nil
}

func (db *DB) ensureHeaderUsersReferences(ctx context.Context, refs []UsersReference) error {
	plans, err := db.checkHeaderUsersReferences(ctx, refs)
	if err != nil {
		return err
	}
	for _, p := range plans {
		switch p.State {
		case "protected":
			// Preserve an equivalent validated/enabled key under any name.
			continue
		case "missing-fk":
			schema, err := db.entityIndexSchema(ctx)
			if err != nil {
				return err
			}
			ref := p.Reference
			qualified := quoteIdent(schema) + "." + quoteIdent(ref.Table)
			q := "ALTER TABLE " + qualified + " ADD CONSTRAINT " + quoteIdent(ForeignKeyName(ref.Table, ref.Column, metadata.SystemUsersEntity)) +
				" FOREIGN KEY (" + quoteIdent(ref.Column) + ") REFERENCES " + quoteIdent(schema) + "." + quoteIdent(metadata.SystemUsersEntity) + "(id)"
			if _, err := db.Exec(ctx, q); err != nil {
				return fmt.Errorf("%s: установка FK на _users: %w", ref.Object, err)
			}
		default:
			return fmt.Errorf("%s: колонка для FK не создана: %s", p.Reference.Object, p.String())
		}
	}
	verified, err := db.checkHeaderUsersReferences(ctx, refs)
	if err != nil {
		return err
	}
	for _, p := range verified {
		if p.State != "protected" {
			return fmt.Errorf("%s: действующий FK на _users не подтверждён", p.Reference.Object)
		}
	}
	return nil
}
