package cli

func navigationContainerSchema(home bool) map[string]any {
	names := arrayOf(stringSchema("Имя объекта"))
	contents := map[string]any{"type": "object", "additionalProperties": false, "properties": map[string]any{
		"catalogs": names, "documents": names, "registers": names, "inforegs": names,
		"reports": names, "processors": names, "journals": names, "pages": names,
	}}
	node := func() map[string]any {
		return map[string]any{
			"id":    map[string]any{"type": "string", "pattern": "^[a-z][a-z0-9-]{0,62}$", "not": map[string]any{"const": "other"}},
			"title": stringSchema("Заголовок"), "titles": stringMapSchema(), "icon": stringSchema("Имя иконки Lucide"),
		}
	}
	item := node()
	item["target"] = map[string]any{"type": "string", "pattern": `^(?:(?:catalog|document|inforeg|report|processor|journal|page):[^:/\s?#%<>&]+|register:[^:/\s?#%<>&]+:(?:movements|balances)|system:constants)$`}
	itemSchema := map[string]any{"type": "object", "additionalProperties": false, "required": []string{"id", "target"}, "properties": item}
	group := node()
	group["items"] = arrayOf(itemSchema)
	groupSchema := map[string]any{"type": "object", "additionalProperties": false, "required": []string{"id", "title"}, "properties": group}
	section := node()
	section["items"] = arrayOf(itemSchema)
	section["groups"] = arrayOf(groupSchema)
	roles := arrayOf(map[string]any{"type": "string", "minLength": 1})
	nodeRef := map[string]any{"$ref": "#/$defs/navigationNode"}
	command := node()
	command["target"], command["roles"] = item["target"], roles
	folder := node()
	folder["nodes"], folder["roles"] = arrayOf(nodeRef), roles
	recursiveNode := map[string]any{"oneOf": []any{
		map[string]any{"type": "object", "additionalProperties": false, "required": []string{"id", "target"}, "properties": command},
		map[string]any{"type": "object", "additionalProperties": false, "required": []string{"id", "title", "nodes"}, "properties": folder},
	}}
	section["nodes"], section["roles"] = arrayOf(nodeRef), roles
	sectionSchema := map[string]any{"type": "object", "additionalProperties": false, "required": []string{"id", "title"}, "properties": section}
	sectionSchema["not"] = map[string]any{"allOf": []any{map[string]any{"required": []string{"nodes"}}, map[string]any{"anyOf": []any{map[string]any{"required": []string{"items"}}, map[string]any{"required": []string{"groups"}}}}}}
	menu := map[string]any{"type": "object", "additionalProperties": false, "properties": map[string]any{"sections": arrayOf(sectionSchema)}}
	props := map[string]any{"title": stringSchema("Заголовок"), "titles": stringMapSchema(), "menu": menu}
	title := "OneBase subsystem"
	if home {
		title = "OneBase home page"
		props["nav"] = contents
		props["nav_by_role"] = map[string]any{"type": "object", "additionalProperties": contents}
		props["hidden"] = boolSchema("Скрыть глобальную Главную")
		props["layout"] = enumSchema("auto", "grid", "rows")
		props["rows"] = arrayOf(map[string]any{"type": "object", "properties": map[string]any{"widgets": names}})
		props["widgets"] = arrayOf(map[string]any{"type": "object", "properties": map[string]any{"name": stringSchema("Виджет"), "span": map[string]any{"type": "integer"}}})
	} else {
		props["name"] = stringSchema("Имя подсистемы")
		props["icon"] = stringSchema("Имя иконки Lucide")
		props["order"] = map[string]any{"type": "integer"}
		props["roles"] = names
		props["contents"] = contents
		subHome := navigationContainerSchema(true)
		delete(subHome["properties"].(map[string]any), "menu")
		props["home_page"] = subHome
	}
	return map[string]any{"$schema": "https://json-schema.org/draft/2020-12/schema", "$defs": map[string]any{"navigationNode": recursiveNode}, "title": title, "type": "object", "additionalProperties": false, "properties": props}
}
