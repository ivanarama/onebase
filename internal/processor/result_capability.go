package processor

import (
	"fmt"
	"strings"

	"github.com/ivantit66/onebase/internal/metadata"
)

// ResultCapability binds an owner-index flag to the live version of one source
// catalog. Every name comes from trusted configuration, never event arguments.
type ResultCapability struct {
	Register           string `yaml:"register"`
	ReferenceField     string `yaml:"reference_field"`
	OwnerField         string `yaml:"owner_field"`
	EnabledField       string `yaml:"enabled_field"`
	SourceEntity       string `yaml:"source_entity"`
	SourceIDField      string `yaml:"source_id_field"`
	SourceVersionField string `yaml:"source_version_field"`
	ObservedAtField    string `yaml:"observed_at_field"`
	MaxAgeSeconds      int    `yaml:"max_age_seconds"`
}

func ResultCapabilityField(fields []metadata.Field, name string) *metadata.Field {
	for i := range fields {
		if fields[i].Name == name && name != "" {
			return &fields[i]
		}
	}
	return nil
}

func (c ResultCapability) Validate(ir *metadata.InfoRegister, source *metadata.Entity) error {
	if ir == nil || source == nil || source.Kind != metadata.KindCatalog || c.MaxAgeSeconds < 1 || c.MaxAgeSeconds > 300 {
		return fmt.Errorf("result_capabilities requires an existing register, a catalog source and max_age_seconds 1..300")
	}
	if ir.Periodic || ir.Recorder || len(ir.Dimensions) != 2 || c.ReferenceField == c.OwnerField {
		return fmt.Errorf("result_capabilities index must be independent/non-periodic with exactly reference and owner dimensions")
	}
	ref := ResultCapabilityField(ir.Dimensions, c.ReferenceField)
	owner := ResultCapabilityField(ir.Dimensions, c.OwnerField)
	if ref == nil || !metadata.IsReference(ref.Type) || owner == nil || owner.Type != metadata.FieldTypeString {
		return fmt.Errorf("result_capabilities requires typed reference and string owner dimensions")
	}
	seen := map[string]bool{}
	for _, f := range []struct {
		name     string
		typeName metadata.FieldType
	}{{c.EnabledField, metadata.FieldTypeBool}, {c.SourceIDField, metadata.FieldTypeString}, {c.SourceVersionField, metadata.FieldTypeNumber}, {c.ObservedAtField, metadata.FieldTypeDate}} {
		field := ResultCapabilityField(ir.Resources, f.name)
		if field == nil || field.Type != f.typeName || seen[strings.ToLower(f.name)] {
			return fmt.Errorf("result_capabilities resource %q must be distinct and have type %s", f.name, f.typeName)
		}
		seen[strings.ToLower(f.name)] = true
	}
	return nil
}
