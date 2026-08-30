package configdb_test

import (
	"bytes"
	"context"
	"errors"
	"sync"
	"testing"

	"github.com/ivantit66/onebase/internal/configdb"
	"github.com/ivantit66/onebase/internal/dbtest"
	"github.com/ivantit66/onebase/internal/storage"
)

// CreateFile обязан одинаково работать на SQLite и PostgreSQL: из двух
// конкурентных создателей ровно один записывает содержимое и создаёт версию.
func TestCreateFileConcurrentMatrix(t *testing.T) {
	dbtest.ForEachDialect(t, func(t *testing.T, db *storage.DB) {
		ctx := context.Background()
		repo := configdb.New(db)
		if err := repo.EnsureSchema(ctx); err != nil {
			t.Fatalf("EnsureSchema: %v", err)
		}

		start := make(chan struct{})
		errs := make(chan error, 2)
		contents := [][]byte{[]byte("creator: first\n"), []byte("creator: second\n")}
		var wg sync.WaitGroup
		for _, content := range contents {
			content := content
			wg.Add(1)
			go func() {
				defer wg.Done()
				<-start
				errs <- repo.CreateFile(ctx, "printforms/concurrent.layout.yaml", content)
			}()
		}
		close(start)
		wg.Wait()
		close(errs)

		var created, exists int
		for err := range errs {
			switch {
			case err == nil:
				created++
			case errors.Is(err, configdb.ErrFileExists):
				exists++
			default:
				t.Fatalf("CreateFile: unexpected error: %v", err)
			}
		}
		if created != 1 || exists != 1 {
			t.Fatalf("results: created=%d exists=%d, want 1/1", created, exists)
		}

		got, ok, err := repo.ReadFile(ctx, "printforms/concurrent.layout.yaml")
		if err != nil || !ok {
			t.Fatalf("ReadFile: ok=%v err=%v", ok, err)
		}
		if !bytes.Equal(got, contents[0]) && !bytes.Equal(got, contents[1]) {
			t.Fatalf("stored partial/foreign content: %q", got)
		}
		versions, err := repo.ListVersions(ctx, 10)
		if err != nil {
			t.Fatalf("ListVersions: %v", err)
		}
		if len(versions) != 1 || versions[0].Message != "create printforms/concurrent.layout.yaml" {
			t.Fatalf("versions = %+v, want one create version", versions)
		}
	})
}
