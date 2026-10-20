package cli

// Only the new value/source nodes are strict. Historical role sections keep
// their permissive schema; this is not a global KnownFields migration.
func roleRowAccessSchema() map[string]any {
	source := map[string]any{"oneOf": []any{
		map[string]any{"type": "string", "pattern": `^[^.]+\.[^.]+$`, "description": "Catalog.Field; exactly one reference:_users account field"},
		map[string]any{"type": "object", "additionalProperties": false, "required": []string{"catalog", "account_field", "field"}, "properties": map[string]any{
			"catalog": stringSchema("Catalog containing the employee card"), "account_field": stringSchema("reference:_users field"), "field": stringSchema("Ordinary scalar field")}},
	}}
	sources := map[string]any{"user": stringSchema("id|login"), "user_attr": stringSchema("Built-in user attribute"), "literal": map[string]any{}, "list": arrayOf(map[string]any{}), "user_card": source}
	alternatives := []any{}
	for _, name := range []string{"user", "user_attr", "literal", "list", "user_card"} {
		alternatives = append(alternatives, map[string]any{"required": []string{name}, "properties": map[string]any{name: sources[name]}, "type": "object", "additionalProperties": false})
	}
	policy := map[string]any{"type": "object", "properties": map[string]any{
		"field": stringSchema("Restricted field"), "op": stringSchema("Operator; scalar user_card requires eq|ne"), "value": map[string]any{"oneOf": alternatives}, "same_as": stringSchema("Operation to reuse"),
		"all": arrayOf(map[string]any{"$ref": "#/$defs/rowPolicy"}), "any": arrayOf(map[string]any{"$ref": "#/$defs/rowPolicy"}), "not": map[string]any{"$ref": "#/$defs/rowPolicy"}}}
	section := map[string]any{"type": "object", "additionalProperties": map[string]any{"type": "object", "additionalProperties": map[string]any{"$ref": "#/$defs/rowPolicy"}}}
	rows := map[string]any{"type": "object", "properties": map[string]any{"catalogs": section, "documents": section, "registers": section, "inforegs": section}}
	role := looseNamedSchema("OneBase RBAC role")
	role["$defs"] = map[string]any{"rowPolicy": policy}
	role["properties"].(map[string]any)["permissions"] = map[string]any{"type": "object", "properties": map[string]any{"row_access": rows}}
	return role
}
