package cli

func reportContextSchema() map[string]any {
	card := map[string]any{"oneOf": []any{
		map[string]any{"type": "string", "description": "Catalog.Field; ровно один reference:_users в справочнике"},
		map[string]any{"type": "object", "additionalProperties": false, "required": []string{"catalog", "account_field", "field"}, "properties": map[string]any{
			"catalog": stringSchema("Справочник карточки пользователя"), "account_field": stringSchema("Реквизит reference:_users"), "field": stringSchema("Обычный скалярный реквизит карточки"),
		}},
	}}
	return map[string]any{"oneOf": []any{
		map[string]any{"type": "object", "additionalProperties": false, "required": []string{"user"}, "properties": map[string]any{"user": enumSchema("id", "login")}},
		map[string]any{"type": "object", "additionalProperties": false, "required": []string{"user_card"}, "properties": map[string]any{"user_card": card}},
		map[string]any{"type": "object", "additionalProperties": false, "required": []string{"catalog_name"}, "properties": map[string]any{
			"catalog_name": map[string]any{"type": "object", "additionalProperties": false, "required": []string{"catalog", "name"}, "properties": map[string]any{
				"catalog": stringSchema("Справочник"), "name": stringSchema("Точное наименование существующего элемента"),
			}},
		}},
	}}
}
