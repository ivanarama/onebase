package metadata

import "fmt"

func Validate(entities []*Entity) error {
	names := make(map[string]bool, len(entities))
	for _, e := range entities {
		names[e.Name] = true
	}
	for _, e := range entities {
		for _, f := range e.Fields {
			if f.RefEntity != "" && !names[f.RefEntity] {
				return fmt.Errorf("entity %s: field %s references unknown entity %s", e.Name, f.Name, f.RefEntity)
			}
		}
	}
	return nil
}
