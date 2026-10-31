package backup

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/ivantit66/onebase/internal/dbtest"
	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/ivantit66/onebase/internal/project"
	"github.com/ivantit66/onebase/internal/storage"
	"github.com/stretchr/testify/require"
)

// Long-name inventory must coexist with transactional FK suspension, validation
// and rollback, including archives crossing dialects and cyclic references.
func TestUniversalLongIdentifiersForeignKeysMatrix(t *testing.T) {
	const first = "АВладелецОченьДлинногоАрхивногоИмениДляПроверки"
	const last = "ЯВладелецОченьДлинногоАрхивногоИмениДляПроверки"
	const field = "СвязаннаяЗаписьСОченьДлиннымИменемИзАрхива"
	ctx := context.Background()
	cfg := testConfigDir(t)
	require.NoError(t, os.MkdirAll(filepath.Join(cfg, "catalogs"), 0700))
	writeTestFile(t, filepath.Join(cfg, "catalogs", "a.yaml"), "name: "+first+"\nfields:\n  - name: Label\n    type: string\n  - name: "+field+"\n    type: reference:"+last+"\n")
	writeTestFile(t, filepath.Join(cfg, "catalogs", "z.yaml"), "name: "+last+"\nfields:\n  - name: Label\n    type: string\n  - name: "+field+"\n    type: reference:"+first+"\n")
	proj, err := project.Load(cfg)
	require.NoError(t, err)
	defer proj.Close()
	var a, z *metadata.Entity
	for _, e := range proj.Entities {
		if e.Name == first {
			a = e
		}
		if e.Name == last {
			z = e
		}
	}
	require.NotNil(t, a)
	require.NotNil(t, z)
	column := metadata.ColumnName(a.Fields[1])
	table := metadata.TableName(first)
	dbtest.ForEachDialect(t, func(t *testing.T, src *storage.DB) {
		require.NoError(t, src.Migrate(ctx, proj.Entities))
		aid, zid := uuid.New(), uuid.New()
		// Seed the FK graph directly, as the existing short-name FK matrix does.
		// This isolates archive identifiers/constraints from audit event payloads.
		ph := src.Dialect().Placeholder
		_, err := src.Exec(ctx, "INSERT INTO "+metadata.TableName(z.Name)+" (id,label) VALUES ("+ph(1)+",'Z')", zid.String())
		require.NoError(t, err)
		_, err = src.Exec(ctx, "INSERT INTO "+table+" (id,label,"+column+") VALUES ("+ph(1)+",'A',"+ph(2)+")", aid.String(), zid.String())
		require.NoError(t, err)
		_, err = src.Exec(ctx, "UPDATE "+metadata.TableName(z.Name)+" SET "+column+"="+ph(1), aid.String())
		require.NoError(t, err)
		var archive bytes.Buffer
		require.NoError(t, ExportUniversal(ctx, src, "file", cfg, "", "long-fk", &archive))
		invalid := archiveWithMissingFKTarget(t, archive.Bytes(), zid.String(), table)
		dbtest.ForEachDialect(t, func(t *testing.T, dst *storage.DB) {
			dst.SetFilesDir(t.TempDir())
			dest := t.TempDir()
			restore := func(data []byte) error {
				_, err := ImportUniversalWithOptions(ctx, dst, "file", dest, "", bytes.NewReader(data), int64(len(data)), ImportOptions{ExchangeMode: ExchangeRestoreClone})
				return err
			}
			assertFK := func() {
				_, err := dst.Exec(ctx, "UPDATE "+table+" SET "+column+"="+dst.Dialect().Placeholder(1), uuid.New().String())
				require.Error(t, err, "restored FK must reject a missing target")
			}
			for round := 0; round < 2; round++ {
				require.NoError(t, restore(archive.Bytes()))
				row, err := dst.GetByID(ctx, a.Name, aid, a)
				require.NoError(t, err)
				require.Equal(t, "A", row["Label"])
				var peer string
				require.NoError(t, dst.QueryRow(ctx, "SELECT "+column+" FROM "+table).Scan(&peer))
				require.Equal(t, zid.String(), peer)
				assertFK()
			}
			if dst.IsSQLite() {
				return
			} // SQLite's established bulk-import contract re-enables FK without validating old rows.
			before := restoredFKSnapshot(t, dst)
			_, err := dst.Exec(ctx, "UPDATE "+table+" SET label='before failure'")
			require.NoError(t, err)
			err = restore(invalid)
			require.Error(t, err)
			require.True(t, strings.Contains(err.Error(), "restore and validate FK constraints"), "%v", err)
			row, err := dst.GetByID(ctx, a.Name, aid, a)
			require.NoError(t, err)
			require.Equal(t, "before failure", row["Label"])
			require.Equal(t, before, restoredFKSnapshot(t, dst))
			assertFK()
			require.NoError(t, restore(archive.Bytes()))
		})
	})
}
