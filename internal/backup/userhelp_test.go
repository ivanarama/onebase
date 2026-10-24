package backup

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"github.com/ivantit66/onebase/internal/configdb"
	"github.com/ivantit66/onebase/internal/project"
	"github.com/ivantit66/onebase/internal/userhelp"
)

func TestUniversalBackupPreservesHelpAndImages(t *testing.T) {
	for _, source := range []string{"file", "database"} {
		for _, destination := range []string{"file", "database"} {
			t.Run(source+"-to-"+destination, func(t *testing.T) {
				ctx := t.Context()
				dir := testConfigDir(t)
				if err := userhelp.Scaffold(dir); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(dir, "docs/help/introduction.md"), []byte("![Example](images/example.png)"), 0o644); err != nil {
					t.Fatal(err)
				}
				image := []byte{0, 255, 137, 80, 78, 71}
				if err := os.WriteFile(filepath.Join(dir, "docs/help/images/example.png"), image, 0o644); err != nil {
					t.Fatal(err)
				}
				src := newSQLite(t, "help-source")
				if source == "database" {
					repo := configdb.New(src)
					if err := repo.EnsureSchema(ctx); err != nil {
						t.Fatal(err)
					}
					if err := repo.ImportFromDir(ctx, dir); err != nil {
						t.Fatal(err)
					}
				}
				var buf bytes.Buffer
				if err := ExportUniversal(ctx, src, source, dir, "", "help", &buf); err != nil {
					t.Fatal(err)
				}
				dst := newSQLite(t, "help-destination")
				if destination == "database" {
					if err := configdb.New(dst).EnsureSchema(ctx); err != nil {
						t.Fatal(err)
					}
				}
				out := t.TempDir()
				if _, err := ImportUniversal(ctx, dst, destination, out, "", bytes.NewReader(buf.Bytes()), int64(buf.Len())); err != nil {
					t.Fatal(err)
				}
				var b *userhelp.Book
				if destination == "database" {
					p, err := project.LoadFromDB(ctx, configdb.New(dst))
					if err != nil {
						t.Fatal(err)
					}
					p.Close()
					b = p.Help
				} else {
					b = userhelp.Load(out)
				}
				if !b.Available() || !bytes.Equal(b.Assets["images/example.png"].Data, image) {
					t.Fatalf("restored help or image lost: %+v", b)
				}
			})
		}
	}
}
