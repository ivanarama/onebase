package query

// generatedSourceColumns reads only the outer SELECT of compiler-generated
// virtual-table SQL. The emitted expressions, rather than predicate metadata,
// define the names visible to an outer star. Nested SELECTs and totals paths
// therefore keep their own output schema without a second list of VT suffixes.
// An unnamed expression or star leaves the schema unproved (nil).
func generatedSourceColumns(sql string) []string {
	tokens := tokenize(sql)
	if len(tokens) == 0 || upperFast(tokens[0].val) != "SELECT" {
		return nil
	}
	items := tokens[1:topLevelFrom(tokens, 0)]
	if len(items) > 0 && upperFast(items[0].val) == "DISTINCT" {
		items = items[1:]
		// PostgreSQL's information slices emit DISTINCT ON (dimensions).
		if len(items) > 0 && upperFast(items[0].val) == "ON" {
			if len(items) < 2 || items[1].kind != tLParen {
				return nil
			}
			depth, end := 0, -1
			for i := 1; i < len(items); i++ {
				switch items[i].kind {
				case tLParen:
					depth++
				case tRParen:
					depth--
				}
				if depth == 0 {
					end = i
					break
				}
			}
			if end < 0 {
				return nil
			}
			items = items[end+1:]
		}
	}
	var columns []string
	for _, item := range splitProjectionItems(items) {
		col, _ := parseProjectionItem(item)
		if col.Star || col.Output == "" {
			return nil
		}
		columns = append(columns, col.Output)
	}
	return columns
}
