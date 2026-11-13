package entityservice

import "github.com/ivantit66/onebase/internal/metadata"

// partialUpdateHeader supplies the complete business header to OnWrite without
// importing storage-only id/version/deletion/posted keys into the write map.
// SQLite stores booleans as integers; a loaded boolean must mean the same thing
// as the JSON boolean a form would submit. Explicit incoming values are overlaid
// afterwards and deliberately are not defaulted or coalesced here.
func partialUpdateHeader(entity *metadata.Entity, stored map[string]any) map[string]any {
	fields := make(map[string]any, len(entity.Fields)+2)
	for _, field := range entity.Fields {
		value := stored[field.Name]
		if field.Type == metadata.FieldTypeBool && value != nil {
			value = postedFlag(value)
		}
		fields[field.Name] = value
	}
	if entity.Hierarchical {
		fields["parent_id"] = stored["parent_id"]
		fields["is_folder"] = stored["is_folder"]
	}
	return fields
}
