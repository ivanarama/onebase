package cli

import (
	"fmt"

	"github.com/ivantit66/onebase/internal/objstore"
	"github.com/ivantit66/onebase/internal/project"
	"github.com/ivantit66/onebase/internal/storage"
)

// applyFileStorageS3 attaches the S3-compatible blob store to db when app.yaml
// declares file_storage.s3. It is a no-op when the section is absent. The S3
// backend is used only when the base's storage mode (_settings ui.file_storage)
// is "s3"; wiring the client unconditionally lets `onebase settings` switch a
// base to S3 without a restart, and keeps disk/db bases unaffected.
//
// Returns an error only for an invalid S3 block (missing endpoint/bucket/keys),
// so misconfiguration surfaces at startup rather than on the first image upload.
func applyFileStorageS3(db *storage.DB, appCfg *project.AppConfig) error {
	if appCfg == nil || appCfg.FileStorage == nil || appCfg.FileStorage.S3 == nil {
		return nil
	}
	s3, err := appCfg.FileStorage.S3.ResolveSecrets()
	if err != nil {
		return fmt.Errorf("file_storage.s3: %w", err)
	}
	client, err := objstore.New(objstore.Config{
		Endpoint:  s3.Endpoint,
		Region:    s3.Region,
		Bucket:    s3.Bucket,
		AccessKey: s3.AccessKey,
		SecretKey: s3.SecretKey,
		UseSSL:    s3.UseSSL,
		PathStyle: s3.PathStyle,
	})
	if err != nil {
		return fmt.Errorf("file_storage.s3: %w", err)
	}
	db.SetBlobStore(client, s3.Prefix)
	if s3.Stream != nil && *s3.Stream {
		db.SetBlobStreaming(true)
	}
	// Подчищаем возможные протёкшие temp-материализации S3-вложений от прошлых
	// запусков (backstop к per-request context.AfterFunc, план 110, этап 2b).
	db.SweepAttachmentTemp()
	return nil
}
