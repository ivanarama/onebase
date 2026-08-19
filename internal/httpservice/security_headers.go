package httpservice

import "strings"

// ExtraHeaderRejection объясняет, почему заголовок нельзя задавать через
// security_headers.extra. Пустая строка — можно.
//
// Список живёт здесь, рядом с типом конфигурации, а не в двух местах: раньше он
// был скопирован в internal/ui и internal/configcheck, и правка одной копии
// молча расходилась с другой — гейт и рантайм начинали думать по-разному.
//
// Заголовок с выделенным полем через extra запрещён потому, что extra обходит
// проверки этого поля: X-Frame-Options со значением, которое onebase check
// отвергает, или Strict-Transport-Security, уезжающий по чистому HTTP, где
// браузер запомнит домен как HTTPS-only (#1002). Тот, кто пишет extra, обычно
// не «обходит гейт», а просто не знает, что поле есть, — поэтому сообщение
// называет нужное поле.
func ExtraHeaderRejection(name string) string {
	switch n := strings.ToLower(strings.TrimSpace(name)); {
	case n == "x-content-type-options":
		return "X-Content-Type-Options ставится всегда: отключаемый nosniff нужен только чтобы навредить себе"
	case strings.HasPrefix(n, "access-control-"):
		return "Access-Control-* задаётся блоком cors: — ручная правка разъедется с preflight-ответом платформы"
	case n == "content-security-policy":
		return "используйте поле csp: два заголовка CSP браузер применяет как пересечение, политика вышла бы строже задуманной"
	case n == "x-frame-options":
		return "используйте поле frame_options: оно проверяется (DENY или SAMEORIGIN), а extra пропустит любое значение"
	case n == "referrer-policy":
		return "используйте поле referrer_policy"
	case n == "strict-transport-security":
		return "используйте поле hsts: оно ставится только поверх TLS, а заголовок из extra уедет и по HTTP, где браузер запомнит домен как HTTPS-only"
	}
	return ""
}

// ExtraHeaderForbidden — короткая форма для рантайма, где причина не нужна.
func ExtraHeaderForbidden(name string) bool {
	return ExtraHeaderRejection(name) != ""
}
