package cli

func reportDrilldownSchema() map[string]any {
	target := map[string]any{"type": "object", "additionalProperties": false, "required": []string{"kind"}, "properties": map[string]any{
		"kind": enumSchema("entity", "image", "attachment"), "entity": stringSchema("Разрешённая сущность document/catalog; только kind:entity"),
	}}
	return arrayOf(map[string]any{"type": "object", "additionalProperties": false, "required": []string{"id_field"}, "properties": map[string]any{
		"field": stringSchema("Ячейка результата; пусто означает действие строки"), "title": stringSchema("Подпись действия строки"), "id_field": stringSchema("Колонка с UUID"),
		"type_field": stringSchema("Колонка ключа выбора из targets"), "target": target, "targets": map[string]any{"type": "object", "additionalProperties": target},
	}})
}
