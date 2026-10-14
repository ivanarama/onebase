package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"

	"github.com/ivantit66/onebase/internal/backup"
	"github.com/ivantit66/onebase/internal/configdb"
	"github.com/ivantit66/onebase/internal/dblock"
	"github.com/ivantit66/onebase/internal/project"
	"github.com/ivantit66/onebase/internal/storage"
	"github.com/spf13/cobra"
)

func runMigrationPreview(cmd *cobra.Command) error {
	ctx := cmd.Context()
	if ctx == nil {
		ctx = context.Background()
	}
	var planText string
	dir, _ := cmd.Flags().GetString("project")
	sqlitePath, _ := cmd.Flags().GetString("sqlite")
	source, _ := cmd.Flags().GetString("config-source")
	callback := func(ctx context.Context, db *storage.DB) error {
		if err := backup.CheckNoPendingRestore(ctx, db); err != nil {
			return err
		}
		state, err := db.SchemaRevisionStateOf(ctx)
		if err != nil {
			return err
		}
		if err := guardProbedSchemaRevision(state); err != nil {
			return err
		}
		var proj *project.Project
		if source == "database" {
			exists, err := db.TableExists(ctx, "_onebase_config")
			if err != nil {
				return err
			}
			if !exists {
				return errors.New("database configuration is absent; dry-run does not create _onebase_config")
			}
			proj, err = project.LoadFromDB(ctx, configdb.New(db))
			if err != nil {
				return err
			}
		} else {
			proj, err = project.Load(dir)
			if err != nil {
				return err
			}
		}
		defer proj.Close()
		planText, err = migrationPlanText(ctx, db, proj)
		return err
	}

	var err error
	if sqlitePath == "" {
		err = storage.WithPostgresMigrationPreview(ctx, dsnFromFlags(cmd), callback)
	} else {
		lease, target, existed, leaseErr := dblock.AcquireSQLitePreviewTarget(sqlitePath)
		if leaseErr != nil {
			return leaseErr
		}
		err = storage.WithSQLiteMigrationPreview(ctx, target, existed, callback)
		err = errors.Join(err, lease.Close())
	}
	if err != nil {
		return err
	}
	outln(planText)
	return nil
}

func readIndexManifest(path string) ([]storage.IndexAdoption, error) {
	if path == "" {
		return nil, nil
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	var m storage.IndexManifest
	if err := dec.Decode(&m); err != nil {
		return nil, err
	}
	if err := dec.Decode(new(any)); err != io.EOF {
		return nil, fmt.Errorf("adoption manifest must contain one JSON object")
	}
	if m.Version != 1 || len(m.Indexes) == 0 {
		return nil, errors.New("adoption requires version 1 and explicit indexes")
	}
	return m.Indexes, nil
}
