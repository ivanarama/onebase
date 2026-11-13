package configcheck

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestResultCapabilityPublicCheck(t *testing.T) {
	for _, mode := range []string{"valid", "typo", "bad-type", "no-source", "no-param", "bad-ttl"} {
		t.Run(mode, func(t *testing.T) {
			dir := t.TempDir()
			mkFile(t, filepath.Join(dir, "catalogs", "parent.yaml"), "name: Parent\nfields: [{name: Name, type: string}]\n")
			mkFile(t, filepath.Join(dir, "catalogs", "source.yaml"), "name: Source\nfields: [{name: Name, type: string}]\n")
			typeName := "bool"
			if mode == "bad-type" {
				typeName = "string"
			}
			mkFile(t, filepath.Join(dir, "inforegs", "index.yaml"), `name: Index
dimensions:
  - {name: Parent, type: "reference:Parent"}
  - {name: Owner, type: string}
resources:
  - {name: Enabled, type: `+typeName+`}
  - {name: SourceID, type: string}
  - {name: Version, type: number}
  - {name: Observed, type: date}
`)
			definition := `name: Result
params: [{name: Record, type: "reference:Parent"}]
result_capabilities:
  Note:
    register: Index
    reference_field: Parent
    owner_field: Owner
    enabled_field: Enabled
    source_entity: Source
    source_id_field: SourceID
    source_version_field: Version
    observed_at_field: Observed
    max_age_seconds: 300
`
			switch mode {
			case "typo":
				definition = strings.Replace(definition, "observed_at_field:", "observed_at_filed:", 1)
			case "no-source":
				definition = strings.Replace(definition, "source_entity: Source", "source_entity: Missing", 1)
			case "no-param":
				definition = strings.Replace(definition, `params: [{name: Record, type: "reference:Parent"}]`, "params: []", 1)
			case "bad-ttl":
				definition = strings.Replace(definition, "max_age_seconds: 300", "max_age_seconds: 301", 1)
			}
			mkFile(t, filepath.Join(dir, "processors", "result.yaml"), definition)
			result := RunFull(dir)
			if result.OK != (mode == "valid") {
				t.Fatalf("check OK=%v issues=%+v", result.OK, result.Issues)
			}
			if mode == "typo" && len(result.Warnings) == 0 && len(result.Issues) == 0 {
				t.Fatal("unknown binding key was silently accepted")
			}
		})
	}
}
