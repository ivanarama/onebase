package project

import (
	"fmt"
	"strings"

	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/ivantit66/onebase/internal/processor"
)

func (p *Project) validateResultCapabilities() error {
	for _, proc := range p.Processors {
		seen := map[string]bool{}
		for name, binding := range proc.ResultCapabilities {
			if strings.TrimSpace(name) == "" || strings.TrimSpace(name) != name || seen[strings.ToLower(name)] {
				return fmt.Errorf("processor %s: result_capabilities name %q is empty or ambiguous", proc.Name, name)
			}
			seen[strings.ToLower(name)] = true
			var ir *metadata.InfoRegister
			var source *metadata.Entity
			for _, candidate := range p.InfoRegisters {
				if strings.EqualFold(candidate.Name, binding.Register) {
					ir = candidate
				}
			}
			for _, candidate := range p.Entities {
				if strings.EqualFold(candidate.Name, binding.SourceEntity) {
					source = candidate
				}
			}
			if err := binding.Validate(ir, source); err != nil {
				return fmt.Errorf("processor %s result_capabilities.%s: %w", proc.Name, name, err)
			}
			parent := metadata.RefName(processor.ResultCapabilityField(ir.Dimensions, binding.ReferenceField).Type)
			parentExists, boundParam := false, false
			for _, entity := range p.Entities {
				parentExists = parentExists || strings.EqualFold(entity.Name, parent)
			}
			for _, param := range proc.Params {
				boundParam = boundParam || strings.EqualFold(param.Type, "reference:"+parent)
			}
			if !parentExists || !boundParam {
				return fmt.Errorf("processor %s result_capabilities.%s: index reference needs an existing entity and a typed processor parameter", proc.Name, name)
			}
		}
	}
	return nil
}
