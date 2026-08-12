package backup

import (
	"archive/zip"
	"bytes"
	"context"
	"errors"
	"testing"

	"github.com/ivantit66/onebase/internal/storage"
)

var errInjectedMetadata = errors.New("injected metadata failure")

type metadataFaultDB struct {
	sqlite      bool
	queryErr    error
	queryRows   storage.Rows
	queryRowErr error
	tableExists bool
}

func (db *metadataFaultDB) IsSQLite() bool { return db.sqlite }

func (db *metadataFaultDB) Query(context.Context, string, ...any) (storage.Rows, error) {
	if db.queryErr != nil {
		return nil, db.queryErr
	}
	if db.queryRows == nil {
		return &metadataFaultRows{}, nil
	}
	return db.queryRows, nil
}

func (db *metadataFaultDB) QueryRow(context.Context, string, ...any) storage.Row {
	return metadataFaultRow{exists: db.tableExists, err: db.queryRowErr}
}

type metadataFaultRow struct {
	exists bool
	err    error
}

func (row metadataFaultRow) Scan(dest ...any) error {
	if row.err != nil {
		return row.err
	}
	*dest[0].(*bool) = row.exists
	return nil
}

type metadataFaultRows struct {
	scanErr error
	rowsErr error
	yielded bool
}

func (rows *metadataFaultRows) Next() bool {
	if rows.scanErr != nil && !rows.yielded {
		rows.yielded = true
		return true
	}
	return false
}

func (rows *metadataFaultRows) Scan(...any) error { return rows.scanErr }
func (rows *metadataFaultRows) Err() error        { return rows.rowsErr }
func (rows *metadataFaultRows) Close()            {}
func (rows *metadataFaultRows) FieldNames() []string {
	return nil
}

func TestSchemaDetectorsPropagateMetadataErrors(t *testing.T) {
	type detector func(context.Context, schemaMetadataDB, string) (map[string]bool, error)
	detectors := []struct {
		name   string
		sqlite bool
		call   detector
	}{
		{name: "byte/postgres", call: detectByteCols},
		{name: "byte/sqlite", sqlite: true, call: detectByteCols},
		{name: "json/postgres", call: detectJSONCols},
		{name: "boolean/postgres", call: detectBoolCols},
		{name: "bytea/postgres", call: detectByteaCols},
		{name: "bytea/sqlite", sqlite: true, call: detectByteaCols},
	}
	faults := []struct {
		name string
		set  func(*metadataFaultDB)
	}{
		{name: "query", set: func(db *metadataFaultDB) { db.queryErr = errInjectedMetadata }},
		{name: "scan", set: func(db *metadataFaultDB) {
			db.queryRows = &metadataFaultRows{scanErr: errInjectedMetadata}
		}},
		{name: "rows", set: func(db *metadataFaultDB) {
			db.queryRows = &metadataFaultRows{rowsErr: errInjectedMetadata}
		}},
	}

	for _, detectorCase := range detectors {
		for _, fault := range faults {
			t.Run(detectorCase.name+"/"+fault.name, func(t *testing.T) {
				db := &metadataFaultDB{sqlite: detectorCase.sqlite}
				fault.set(db)
				_, err := detectorCase.call(context.Background(), db, "records")
				if !errors.Is(err, errInjectedMetadata) {
					t.Fatalf("detector error = %v, want injected metadata error", err)
				}
			})
		}
	}
}

func TestExportSafeSettingsPropagatesMetadataErrors(t *testing.T) {
	tests := []struct {
		name string
		set  func(*metadataFaultDB)
	}{
		{name: "table existence", set: func(db *metadataFaultDB) {
			db.queryRowErr = errInjectedMetadata
		}},
		{name: "query", set: func(db *metadataFaultDB) {
			db.tableExists = true
			db.queryErr = errInjectedMetadata
		}},
		{name: "scan", set: func(db *metadataFaultDB) {
			db.tableExists = true
			db.queryRows = &metadataFaultRows{scanErr: errInjectedMetadata}
		}},
		{name: "rows", set: func(db *metadataFaultDB) {
			db.tableExists = true
			db.queryRows = &metadataFaultRows{rowsErr: errInjectedMetadata}
		}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			db := &metadataFaultDB{}
			tt.set(db)
			var out bytes.Buffer
			zw := zip.NewWriter(&out)
			_, err := exportSafeSettings(context.Background(), db, zw)
			if closeErr := zw.Close(); closeErr != nil {
				t.Fatalf("close ZIP: %v", closeErr)
			}
			if !errors.Is(err, errInjectedMetadata) {
				t.Fatalf("exportSafeSettings error = %v, want injected metadata error", err)
			}
		})
	}
}
