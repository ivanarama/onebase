package project

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadConfig_BackupSection(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "config"), 0o755); err != nil {
		t.Fatal(err)
	}
	raw := []byte(`name: Test
backup:
  enabled: true
  schedule: "0 3 * * *"
  keep_last: 14
  directory: /var/backups/onebase
`)
	if err := os.WriteFile(filepath.Join(dir, "config", "app.yaml"), raw, 0o644); err != nil {
		t.Fatal(err)
	}

	cfg, err := LoadConfig(dir)
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	if cfg.Backup == nil {
		t.Fatal("Backup is nil")
	}
	if !cfg.Backup.Enabled || cfg.Backup.Schedule != "0 3 * * *" || cfg.Backup.KeepLast != 14 {
		t.Fatalf("Backup parsed incorrectly: %+v", cfg.Backup)
	}
	if cfg.Backup.Directory != "/var/backups/onebase" {
		t.Fatalf("directory = %q", cfg.Backup.Directory)
	}
}

func TestLoadConfig_BackupS3EnvExpansion(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "config"), 0o755); err != nil {
		t.Fatal(err)
	}
	raw := []byte(`name: Test
backup:
  enabled: true
  s3:
    endpoint: s3.amazonaws.com
    region: eu-central-1
    bucket: my-backups
    prefix: prod/
    access_key: ${env:OB_S3_KEY}
    secret_key: ${env:OB_S3_SECRET}
    keep_last: 30
`)
	if err := os.WriteFile(filepath.Join(dir, "config", "app.yaml"), raw, 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("OB_S3_KEY", "AKIAEXAMPLE")
	t.Setenv("OB_S3_SECRET", "topsecret")

	cfg, err := LoadConfig(dir)
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	if cfg.Backup == nil || cfg.Backup.S3 == nil {
		t.Fatalf("S3 section missing: %+v", cfg.Backup)
	}
	s3 := cfg.Backup.S3
	if s3.Bucket != "my-backups" || s3.Region != "eu-central-1" || s3.Prefix != "prod/" || s3.KeepLast != 30 {
		t.Fatalf("S3 parsed incorrectly: %+v", s3)
	}
	// Ссылка на секрет при загрузке НЕ раскрывается (план 83) — креды не должны
	// жить в конфигурации приложения значением.
	if s3.AccessKey != "${env:OB_S3_KEY}" || s3.SecretKey != "${env:OB_S3_SECRET}" {
		t.Errorf("ссылки должны остаться как есть: access_key=%q secret_key=%q", s3.AccessKey, s3.SecretKey)
	}
	// Значение подставляется в момент создания S3-клиента.
	resolved, err := s3.ResolveSecrets()
	if err != nil {
		t.Fatalf("ResolveSecrets: %v", err)
	}
	if resolved.AccessKey != "AKIAEXAMPLE" {
		t.Errorf("access_key = %q, want resolved AKIAEXAMPLE", resolved.AccessKey)
	}
	if resolved.SecretKey != "topsecret" {
		t.Errorf("secret_key = %q, want resolved topsecret", resolved.SecretKey)
	}
}
