package storage

import (
	"context"
	"fmt"
	"strconv"
	"strings"
)

// Переименование учётки (onebase user rename, карточка пользователя в
// веб-админке) переносит на новый логин всё, что база хранит по логину
// строкой, а не по id пользователя: личные настройки в _settings и личные
// варианты отчётов. Пароль, второй фактор, роли, сессии и API-токены привязаны
// к id и переименования не замечают. Журналы (_audit, _stage_history,
// _ai_audit) — история: в них остаётся тот логин, под которым действовали.
//
// Заводишь новый личный ключ в _settings — добавь его семейство сюда, иначе
// после переименования настройка молча «потеряется». Сторожит
// TestRenameUserData_AllUserKeyFamilies.

// userLastKeyPrefixes — семейства «<префикс><длина>:<объект>.<длина>:<логин>»:
// логин — последняя часть ключа.
var userLastKeyPrefixes = []string{
	"formgrid.order.",    // порядок колонок таблицы формы (gridOrderKey)
	"formgrid.widths.",   // ширины колонок таблицы формы (#2357)
	"list.filterfields.", // поля отбора списка (listFilterFieldsKey)
	"report.settings.",   // настройки отчёта (reportSettingsKey)
	"journal.settings.",  // настройки журнала (journalSettingsKey)
	"listview.",          // вид списка: список/плитка (listViewSettingsKey)
}

// cutLenPart разбирает «<длина>:<значение>» в начале s.
func cutLenPart(s string) (value, rest string, ok bool) {
	colon := strings.IndexByte(s, ':')
	if colon <= 0 {
		return "", "", false
	}
	n, err := strconv.Atoi(s[:colon])
	if err != nil || n < 0 || strconv.Itoa(n) != s[:colon] || n > len(s)-colon-1 {
		return "", "", false
	}
	return s[colon+1 : colon+1+n], s[colon+1+n:], true
}

func lenPart(v string) string { return fmt.Sprintf("%d:%s", len(v), v) }

// renamedUserKey — ключ _settings после переименования login → newLogin, или
// "" — ключ не личный ключ этого логина.
func renamedUserKey(key, login, newLogin string) string {
	if v, ok := strings.CutPrefix(key, userFormModeKey("")); ok {
		if v == login {
			return userFormModeKey(newLogin)
		}
		return ""
	}
	if tail, ok := strings.CutPrefix(key, NavigationUserPrefix); ok {
		// ui.navigation.user.<длина>:<логин>.<длина>:<раздел>
		user, rest, ok := cutLenPart(tail)
		if !ok || user != login || !strings.HasPrefix(rest, ".") {
			return ""
		}
		return NavigationUserPrefix + lenPart(newLogin) + rest
	}
	for _, prefix := range userLastKeyPrefixes {
		tail, ok := strings.CutPrefix(key, prefix)
		if !ok {
			continue
		}
		object, rest, ok := cutLenPart(tail)
		if !ok || !strings.HasPrefix(rest, ".") {
			return ""
		}
		user, end, ok := cutLenPart(rest[1:])
		if !ok || end != "" || user != login {
			return ""
		}
		return prefix + lenPart(object) + "." + lenPart(newLogin)
	}
	return ""
}

// isUserKey — ключ k — личный ключ логина login.
func isUserKey(k, login string) bool { return renamedUserKey(k, login, "-") != "" }

// userKeyPrefixes — префиксы всех личных семейств, для выборки из _settings.
func userKeyPrefixes() []string {
	return append([]string{userFormModeKey(""), NavigationUserPrefix}, userLastKeyPrefixes...)
}

// RenameUserData переносит личные данные с логина login на newLogin. Вызывать
// в транзакции переименования. Записи, уже принадлежащие newLogin, — остатки
// удалённой учётки с тем же именем (живой учётки с этим логином нет, это
// проверяет переименование): они удаляются, иначе чужие настройки достались бы
// переименованному пользователю или столкнулись бы с его собственными.
func (db *DB) RenameUserData(ctx context.Context, login, newLogin string) error {
	if login == "" || newLogin == "" || login == newLogin {
		return fmt.Errorf("rename user data: пустой или тот же логин")
	}
	if err := db.EnsureSettingsSchema(ctx); err != nil {
		return err
	}
	d := db.dialect
	var keys []string
	for _, prefix := range userKeyPrefixes() {
		// LIKE лишь сужает выборку («_» в префиксе — тоже шаблон), точную
		// принадлежность ключа решает renamedUserKey.
		err := func() error {
			rows, err := db.Query(ctx, `SELECT key FROM _settings WHERE key LIKE `+d.Placeholder(1), prefix+"%")
			if err != nil {
				return err
			}
			defer rows.Close()
			for rows.Next() {
				var k string
				if err := rows.Scan(&k); err != nil {
					return err
				}
				keys = append(keys, k)
			}
			return rows.Err()
		}()
		if err != nil {
			return fmt.Errorf("rename user data: settings: %w", err)
		}
	}
	del := `DELETE FROM _settings WHERE key = ` + d.Placeholder(1)
	for _, k := range keys {
		if isUserKey(k, newLogin) {
			if _, err := db.Exec(ctx, del, k); err != nil {
				return fmt.Errorf("rename user data: delete %s: %w", k, err)
			}
		}
	}
	upd := fmt.Sprintf(`UPDATE _settings SET key = %s WHERE key = %s`, d.Placeholder(1), d.Placeholder(2))
	for _, k := range keys {
		if nk := renamedUserKey(k, login, newLogin); nk != "" {
			if _, err := db.Exec(ctx, upd, nk, k); err != nil {
				return fmt.Errorf("rename user data: %s: %w", k, err)
			}
		}
	}
	if err := db.EnsureReportPresetSchema(ctx); err != nil {
		return err
	}
	if _, err := db.Exec(ctx, `DELETE FROM _report_presets WHERE user_login = `+d.Placeholder(1), newLogin); err != nil {
		return fmt.Errorf("rename user data: report presets: %w", err)
	}
	if _, err := db.Exec(ctx, fmt.Sprintf(`UPDATE _report_presets SET user_login = %s WHERE user_login = %s`,
		d.Placeholder(1), d.Placeholder(2)), newLogin, login); err != nil {
		return fmt.Errorf("rename user data: report presets: %w", err)
	}
	return nil
}
