package project

import (
	"testing"

	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/ivantit66/onebase/internal/processor"
)

func TestResultCapabilityMetadataValidation(t *testing.T) {
	for _, mode := range []string{"valid", "missing-source", "missing-index", "periodic", "recorder", "extra-dimension", "wrong-reference", "wrong-owner", "wrong-bool", "wrong-id", "wrong-version", "wrong-date", "unbounded-ttl", "empty-ttl", "no-param", "duplicate-name"} {
		t.Run(mode, func(t *testing.T) {
			parent := &metadata.Entity{Name: "Parent", Kind: metadata.KindCatalog}
			source := &metadata.Entity{Name: "Source", Kind: metadata.KindCatalog}
			ir := &metadata.InfoRegister{Name: "Index", Dimensions: []metadata.Field{{Name: "Parent", Type: "reference:Parent"}, {Name: "Owner", Type: metadata.FieldTypeString}}, Resources: []metadata.Field{{Name: "Enabled", Type: metadata.FieldTypeBool}, {Name: "SourceID", Type: metadata.FieldTypeString}, {Name: "Version", Type: metadata.FieldTypeNumber}, {Name: "Observed", Type: metadata.FieldTypeDate}}}
			binding := processor.ResultCapability{Register: ir.Name, ReferenceField: "Parent", OwnerField: "Owner", EnabledField: "Enabled", SourceEntity: source.Name, SourceIDField: "SourceID", SourceVersionField: "Version", ObservedAtField: "Observed", MaxAgeSeconds: 300}
			proc := &processor.Processor{Name: "Result", Params: []processor.Param{{Name: "Record", Type: "reference:Parent"}}}
			p := &Project{Entities: []*metadata.Entity{parent, source}, InfoRegisters: []*metadata.InfoRegister{ir}, Processors: []*processor.Processor{proc}}
			switch mode {
			case "missing-source":
				binding.SourceEntity = "Missing"
			case "missing-index":
				binding.Register = "Missing"
			case "periodic":
				ir.Periodic = true
			case "recorder":
				ir.Recorder = true
			case "extra-dimension":
				ir.Dimensions = append(ir.Dimensions, metadata.Field{Name: "Extra", Type: metadata.FieldTypeString})
			case "wrong-reference":
				ir.Dimensions[0].Type = metadata.FieldTypeString
			case "wrong-owner":
				ir.Dimensions[1].Type = metadata.FieldTypeNumber
			case "wrong-bool":
				ir.Resources[0].Type = metadata.FieldTypeString
			case "wrong-id":
				ir.Resources[1].Type = "reference:Source"
			case "wrong-version":
				ir.Resources[2].Type = metadata.FieldTypeString
			case "wrong-date":
				ir.Resources[3].Type = metadata.FieldTypeString
			case "unbounded-ttl":
				binding.MaxAgeSeconds = 301
			case "empty-ttl":
				binding.MaxAgeSeconds = 0
			case "no-param":
				proc.Params = nil
			}
			proc.ResultCapabilities = map[string]processor.ResultCapability{"Note": binding}
			if mode == "duplicate-name" {
				proc.ResultCapabilities["note"] = binding
			}
			err := p.validateResultCapabilities()
			if (err == nil) != (mode == "valid") {
				t.Fatalf("validation err=%v", err)
			}
		})
	}
}
