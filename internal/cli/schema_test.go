package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"strings"
	"testing"
)

func TestRunSchema(t *testing.T) {
	if err := schemaCmd.Flags().Set("list", "false"); err != nil {
		t.Fatal(err)
	}

	var out bytes.Buffer
	old := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	os.Stdout = w
	defer func() { os.Stdout = old }()
	done := make(chan error, 1)
	go func() {
		_, err := out.ReadFrom(r)
		done <- err
	}()
	if err := runSchema(schemaCmd, []string{"document"}); err != nil {
		w.Close()
		t.Fatalf("runSchema: %v", err)
	}
	w.Close()
	if err := <-done; err != nil {
		t.Fatal(err)
	}

	var got map[string]any
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("schema output is not JSON: %v\n%s", err, out.String())
	}
	if got["$schema"] == "" || got["type"] != "object" {
		t.Fatalf("schema output looks wrong: %+v", got)
	}
}

func TestRunSchemaList(t *testing.T) {
	if err := schemaCmd.Flags().Set("list", "true"); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	old := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	os.Stdout = w
	defer func() {
		os.Stdout = old
		_ = schemaCmd.Flags().Set("list", "false")
	}()
	done := make(chan error, 1)
	go func() {
		_, err := out.ReadFrom(r)
		done <- err
	}()
	if err := runSchema(schemaCmd, nil); err != nil {
		w.Close()
		t.Fatalf("runSchema --list: %v", err)
	}
	w.Close()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "document") || !strings.Contains(out.String(), "form") {
		t.Fatalf("schema list looks wrong:\n%s", out.String())
	}
}
