package cli

import (
	"encoding/json"
	"regexp"
	"testing"

	"github.com/spf13/cobra"
)

func TestSchemaSemanticNavigation(t *testing.T) {
	for _, kind := range []string{"subsystem", "home-page"} {
		t.Run(kind, func(t *testing.T) {
			cmd := &cobra.Command{}
			cmd.Flags().Bool("list", false, "")
			cmd.Flags().String("kind", kind, "")
			out, err := captureStdout(t, func() error { return runSchema(cmd, nil) })
			if err != nil {
				t.Fatal(err)
			}
			var schema map[string]any
			if err := json.Unmarshal([]byte(out), &schema); err != nil {
				t.Fatal(err)
			}
			props := schema["properties"].(map[string]any)
			menu := props["menu"].(map[string]any)
			sections := menu["properties"].(map[string]any)["sections"].(map[string]any)
			section := sections["items"].(map[string]any)["properties"].(map[string]any)
			group := section["groups"].(map[string]any)["items"].(map[string]any)
			if group["additionalProperties"] != false {
				t.Fatal("groups must reject unknown nesting")
			}
			item := section["items"].(map[string]any)["items"].(map[string]any)["properties"].(map[string]any)
			pattern := regexp.MustCompile(item["target"].(map[string]any)["pattern"].(string))
			for _, v := range []string{"catalog:Classes", "register:Attendance:movements", "system:constants", "document:Приказ"} {
				if !pattern.MatchString(v) {
					t.Errorf("schema rejects %s", v)
				}
			}
			for _, v := range []string{"catalog:Class List", "register:X:other", "https://example.com", "catalog:X/Y", "catalog:X?foo=bar"} {
				if pattern.MatchString(v) {
					t.Errorf("schema accepts %s", v)
				}
			}
		})
	}
}
