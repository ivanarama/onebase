package query

import "strings"

// rewriteStrftime handles strftime('fmt', expr) calls:
// SQLite → keep as-is; PG → to_char(expr, 'converted_fmt').
// Format conversions: %Y→YYYY, %m→MM, %d→DD, %H→HH24, %M→MI, %S→SS, %%→%.
func rewriteStrftime(tokens []tok, dialect string) []tok {
	if dialect == "sqlite" {
		return tokens
	}
	var out []tok
	for i := 0; i < len(tokens); i++ {
		t := tokens[i]
		if t.kind == tIdent && lowerFast(t.val) == "strftime" &&
			i+1 < len(tokens) && tokens[i+1].kind == tLParen {
			depth, end := 0, -1
			for j := i + 1; j < len(tokens); j++ {
				switch tokens[j].kind {
				case tLParen:
					depth++
				case tRParen:
					depth--
					if depth == 0 {
						end = j
					}
				}
				if end >= 0 {
					break
				}
			}
			if end < 0 {
				out = append(out, t)
				continue
			}
			args := tokens[i+2 : end]
			if len(args) < 3 || args[0].kind != tStr {
				out = append(out, t)
				continue
			}
			fmtPG := convertStrftimeToToChar(args[0].val)
			expr := args[2:]
			expr = rewriteStrftime(expr, dialect)
			out = append(out, tok{kind: tIdent, val: "to_char"})
			out = append(out, tok{kind: tLParen, val: "("})
			out = append(out, expr...)
			out = append(out, tok{kind: tComma, val: ","})
			out = append(out, tok{kind: tStr, val: "'" + fmtPG + "'"})
			out = append(out, tok{kind: tRParen, val: ")"})
			i = end
			continue
		}
		out = append(out, t)
	}
	return out
}

// convertStrftimeToToChar translates strftime format to PostgreSQL to_char format.
func convertStrftimeToToChar(format string) string {
	s := strings.Trim(format, "'\"")
	r := strings.NewReplacer(
		"%Y", "YYYY",
		"%m", "MM",
		"%d", "DD",
		"%H", "HH24",
		"%M", "MI",
		"%S", "SS",
		"%%", "%",
	)
	return r.Replace(s)
}
