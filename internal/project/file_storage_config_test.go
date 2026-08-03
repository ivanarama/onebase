package project

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadConfig_FileStorageS3(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "config"), 0o755); err != nil {
		t.Fatal(err)
	}
	raw := []byte(`name: Test
file_storage:
  s3:
    endpoint: minio.local:9000
    region: us-east-1
    bucket: files
    prefix: base1/
    access_key: ${env:FS_KEY}
    secret_key: ${env:FS_SECRET}
    use_ssl: false
    path_style: true
    stream: true
`)
	if err := os.WriteFile(filepath.Join(dir, "config", "app.yaml"), raw, 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("FS_KEY", "AKIA_FS")
	t.Setenv("FS_SECRET", "fssecret")

	cfg, err := LoadConfig(dir)
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	if cfg.FileStorage == nil || cfg.FileStorage.S3 == nil {
		t.Fatalf("file_storage.s3 missing: %+v", cfg.FileStorage)
	}
	s3 := cfg.FileStorage.S3
	if s3.Bucket != "files" || s3.Prefix != "base1/" || s3.Endpoint != "minio.local:9000" {
		t.Fatalf("parsed incorrectly: %+v", s3)
	}
	// Ссылки остаются в конфигурации; значение подставляется при создании
	// клиента — ResolveSecrets (план 83).
	if s3.AccessKey != "${env:FS_KEY}" || s3.SecretKey != "${env:FS_SECRET}" {
		t.Errorf("ссылки должны остаться как есть: %+v", s3)
	}
	resolved, err := s3.ResolveSecrets()
	if err != nil {
		t.Fatalf("ResolveSecrets: %v", err)
	}
	if resolved.AccessKey != "AKIA_FS" || resolved.SecretKey != "fssecret" {
		t.Errorf("secrets not resolved from env: %+v", resolved)
	}
	if s3.UseSSL == nil || *s3.UseSSL != false {
		t.Errorf("use_ssl want false, got %v", s3.UseSSL)
	}
	if s3.PathStyle == nil || *s3.PathStyle != true {
		t.Errorf("path_style want true, got %v", s3.PathStyle)
	}
	if s3.Stream == nil || *s3.Stream != true {
		t.Errorf("stream want true, got %v", s3.Stream)
	}
}
