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
	sectionSchema := map[string]any{"type": "object", "additionalProperties": false, "required": []string{"id", "title"}, "properties": section}
	menu := map[string]any{"type": "object", "additionalProperties": false, "properties": map[string]any{"sections": arrayOf(sectionSchema)}}
	props := map[string]any{"title": stringSchema("Заголовок"), "titles": stringMapSchema(), "menu": menu}
	title := "OneBase subsystem"
	if home {
		title = "OneBase home page"
		props["nav"] = contents
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
	return map[string]any{"$schema": "https://json-schema.org/draft/2020-12/schema", "title": title, "type": "object", "additionalProperties": false, "properties": props}
}
