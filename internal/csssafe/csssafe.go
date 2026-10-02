package csssafe

import (
	"regexp"
	"strings"
)

var (
	hexColorRe    = regexp.MustCompile(`(?i)^#(?:[0-9a-f]{3}|[0-9a-f]{4}|[0-9a-f]{6}|[0-9a-f]{8})$`)
	rgbFunctionRe = regexp.MustCompile(`(?is)^(rgb|rgba)\((.*)\)$`)
	cssNumberRe   = regexp.MustCompile(`^[+-]?(?:[0-9]+(?:\.[0-9]+)?|\.[0-9]+)(?:[eE][+-]?[0-9]+)?$`)
	namedColorRe  = regexp.MustCompile(`^[A-Za-z]+$`)
	lengthRe      = regexp.MustCompile(`^[0-9]+(?:\.[0-9]+)?(?:px|pt|mm|cm|in|em|rem|%)$`)
)

var namedColors = map[string]bool{
	"aliceblue": true, "antiquewhite": true, "aquamarine": true, "azure": true,
	"beige": true, "bisque": true, "blanchedalmond": true,
	"black": true, "white": true, "red": true, "green": true, "blue": true,
	"blueviolet": true, "burlywood": true, "cadetblue": true, "chartreuse": true,
	"yellow": true, "orange": true, "purple": true, "gray": true, "grey": true,
	"silver": true, "maroon": true, "olive": true, "lime": true, "aqua": true,
	"teal": true, "navy": true, "fuchsia": true, "magenta": true, "cyan": true,
	"pink": true, "brown": true, "gold": true, "transparent": true,
	"darkred": true, "darkgreen": true, "darkblue": true, "lightgray": true,
	"lightgrey": true, "lightblue": true, "lightgreen": true, "lightyellow": true,
	"chocolate": true, "coral": true, "cornflowerblue": true, "cornsilk": true,
	"crimson": true, "darkcyan": true, "darkgoldenrod": true, "darkgray": true,
	"darkgrey": true, "darkkhaki": true, "darkmagenta": true, "darkolivegreen": true,
	"darkorange": true, "darkorchid": true, "darksalmon": true, "darkseagreen": true,
	"darkslateblue": true, "darkslategray": true, "darkslategrey": true,
	"darkturquoise": true, "darkviolet": true, "deeppink": true, "deepskyblue": true,
	"dimgray": true, "dimgrey": true, "dodgerblue": true, "firebrick": true,
	"floralwhite": true, "forestgreen": true, "gainsboro": true, "ghostwhite": true,
	"goldenrod": true, "greenyellow": true, "honeydew": true, "hotpink": true,
	"indianred": true, "indigo": true, "ivory": true, "khaki": true, "lavender": true,
	"lavenderblush": true, "lawngreen": true, "lemonchiffon": true, "lightcoral": true,
	"lightcyan": true, "lightgoldenrodyellow": true, "lightpink": true,
	"lightsalmon": true, "lightseagreen": true, "lightskyblue": true,
	"lightslategray": true, "lightslategrey": true, "lightsteelblue": true,
	"limegreen": true, "linen": true, "mediumaquamarine": true, "mediumblue": true,
	"mediumorchid": true, "mediumpurple": true, "mediumseagreen": true,
	"mediumslateblue": true, "mediumspringgreen": true, "mediumturquoise": true,
	"mediumvioletred": true, "midnightblue": true, "mintcream": true,
	"mistyrose": true, "moccasin": true, "navajowhite": true, "oldlace": true,
	"olivedrab": true, "orangered": true, "orchid": true, "palegoldenrod": true,
	"palegreen": true, "paleturquoise": true, "palevioletred": true,
	"papayawhip": true, "peachpuff": true, "peru": true, "plum": true,
	"powderblue": true, "rebeccapurple": true, "rosybrown": true, "royalblue": true,
	"saddlebrown": true, "salmon": true, "sandybrown": true, "seagreen": true,
	"seashell": true, "sienna": true, "skyblue": true, "slateblue": true,
	"slategray": true, "slategrey": true, "snow": true, "springgreen": true,
	"steelblue": true, "tan": true, "thistle": true, "tomato": true,
	"turquoise": true, "violet": true, "wheat": true, "whitesmoke": true,
	"yellowgreen": true,
}

// Color returns v only when it is a constrained CSS color value suitable for
// inline styles.
func Color(v string) string {
	v = strings.TrimSpace(v)
	if v == "" {
		return ""
	}
	switch {
	case hexColorRe.MatchString(v):
		return v
	case validRGBColor(v):
		return v
	case namedColorRe.MatchString(v) && namedColors[strings.ToLower(v)]:
		return v
	}
	return ""
}

// validRGBColor принимает безопасный поднабор синтаксиса CSS Color 4:
// legacy-форму с запятыми и современную форму с пробелами и optional "/ alpha".
// rgb() и rgba() — полные алиасы. Значения не ограничиваются диапазоном здесь:
// CSS считает выходящие за диапазон компоненты валидными и ограничивает их при
// вычислении цвета.
func validRGBColor(v string) bool {
	m := rgbFunctionRe.FindStringSubmatch(v)
	if m == nil {
		return false
	}
	body := trimCSSWhitespace(m[2])
	if body == "" {
		return false
	}
	if strings.Contains(body, ",") {
		return validLegacyRGBBody(body)
	}
	return validModernRGBBody(body)
}

func validLegacyRGBBody(body string) bool {
	parts := strings.Split(body, ",")
	if len(parts) != 3 && len(parts) != 4 {
		return false
	}
	firstKind := cssColorTokenInvalid
	for i := 0; i < 3; i++ {
		kind := cssColorToken(trimCSSWhitespace(parts[i]))
		if kind == cssColorTokenInvalid {
			return false
		}
		if i == 0 {
			firstKind = kind
		} else if kind != firstKind {
			// В legacy-синтаксисе все три канала должны быть либо числами,
			// либо процентами; смешение единиц браузер отвергает.
			return false
		}
	}
	return len(parts) == 3 || cssColorToken(trimCSSWhitespace(parts[3])) != cssColorTokenInvalid
}

func validModernRGBBody(body string) bool {
	if strings.Count(body, "/") > 1 {
		return false
	}
	channels, alpha, hasAlpha := body, "", false
	if before, after, ok := strings.Cut(body, "/"); ok {
		channels, alpha, hasAlpha = before, trimCSSWhitespace(after), true
		if cssColorToken(alpha) == cssColorTokenInvalid {
			return false
		}
	}
	parts := strings.FieldsFunc(channels, isCSSWhitespace)
	if len(parts) != 3 {
		return false
	}
	for _, part := range parts {
		if cssColorToken(part) == cssColorTokenInvalid {
			return false
		}
	}
	return !hasAlpha || alpha != ""
}

type cssColorTokenKind uint8

const (
	cssColorTokenInvalid cssColorTokenKind = iota
	cssColorTokenNumber
	cssColorTokenPercentage
)

// cssColorToken проверяет ровно один CSS <number> или <percentage>. Процент
// обязан примыкать к числу: "1 %" не является одним percentage-token.
func cssColorToken(s string) cssColorTokenKind {
	if s == "" || trimCSSWhitespace(s) != s {
		return cssColorTokenInvalid
	}
	if strings.HasSuffix(s, "%") {
		if cssNumberRe.MatchString(strings.TrimSuffix(s, "%")) {
			return cssColorTokenPercentage
		}
		return cssColorTokenInvalid
	}
	if cssNumberRe.MatchString(s) {
		return cssColorTokenNumber
	}
	return cssColorTokenInvalid
}

func trimCSSWhitespace(s string) string {
	return strings.TrimFunc(s, isCSSWhitespace)
}

func isCSSWhitespace(r rune) bool {
	switch r {
	case ' ', '\t', '\n', '\f', '\r':
		return true
	default:
		return false
	}
}

// Length returns v only when it is a simple CSS length used by layout previews.
func Length(v string) string {
	v = strings.TrimSpace(v)
	if v == "" {
		return ""
	}
	if strings.EqualFold(v, "auto") {
		return "auto"
	}
	if v == "0" || lengthRe.MatchString(v) {
		return v
	}
	return ""
}

// FontFamily strips CSS-breaking characters from a font-family value.
func FontFamily(v string) string {
	v = strings.TrimSpace(v)
	if v == "" {
		return ""
	}
	v = strings.Map(func(r rune) rune {
		switch r {
		case '"', '\'', '<', '>', ';', '\\':
			return -1
		default:
			return r
		}
	}, v)
	return strings.TrimSpace(v)
}

// TextAlign returns one of the allowed text-align values.
func TextAlign(v string) string {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "left", "right", "center", "justify":
		return strings.ToLower(strings.TrimSpace(v))
	default:
		return ""
	}
}
