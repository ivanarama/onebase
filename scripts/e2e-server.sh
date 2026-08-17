#!/usr/bin/env bash
# Поднимает onebase для браузерных smoke-тестов (#791): чистая база, известный
# пользователь, демо-данные. Запускается Playwright'ом как webServer.
#
# Пользователь заводится ОБЯЗАТЕЛЬНО, и это не формальность: пока в базе нет ни
# одного пользователя, аутентификация выключена целиком
# (internal/auth/middleware.go — ветка hasUsers == false отдаёт открытый
# доступ). На пустой базе сценарий входа был бы зелёным, ничего не проверив, —
# ровно тот случай, из-за которого в CLAUDE.md записано правило про #611.
set -euo pipefail

REPO="$(cd "$(dirname "$0")/.." && pwd)"
PROJ="${OB_PROJECT:-$REPO/examples/trade}"
WORK="${OB_WORK:-${RUNNER_TEMP:-/tmp}/ob-e2e}"
PORT="${OB_PORT:-18080}"
DB="$WORK/e2e.db"

export ONEBASE_NO_GUI=1
export ONEBASE_LOG_LEVEL="${ONEBASE_LOG_LEVEL:-warn}"

# Каталог чистим до подмены HOME. Иначе внутрь него уезжает кеш Go-модулей —
# он выкладывается только на чтение, и следующий прогон падает на rm -rf.
chmod -R u+w "$WORK" 2>/dev/null || true
rm -rf "$WORK"
mkdir -p "$WORK"

# Собираем с обычным HOME, чтобы переиспользовать общий кеш модулей.
OB="${OB_BIN:-$WORK/onebase}"
if [ ! -x "$OB" ]; then
  go build -o "$OB" "$REPO/cmd/onebase"
fi

# А вот сервер запускаем уже с изолированным HOME: туда он кладёт вложения
# (~/.onebase/files) и реестр баз, и прогоны не должны пачкать друг друга.
export HOME="$WORK/home"
mkdir -p "$HOME"

"$OB" migrate --project "$PROJ" --sqlite "$DB"

# Пароль только через stdin: аргументом он утёк бы в историю команд и в ps.
printf '%s' "${OB_ADMIN_PASSWORD:-Sm0ke-P@ssw0rd!}" |
  "$OB" user add "${OB_ADMIN_LOGIN:-admin}" --admin --name "Администратор" \
    --project "$PROJ" --sqlite "$DB" --password-stdin

# Второй пользователь — БЕЗ прав администратора. Нужен, чтобы проверять отказ:
# без него набор проверял бы только счастливый путь, а «доступ закрыт» — самая
# дорогая вещь, которую страшно сломать молча.
printf '%s' "${OB_USER_PASSWORD:-Us3r-P@ssw0rd!}" |
  "$OB" user add "${OB_USER_LOGIN:-user}" --name "Обычный пользователь" \
    --project "$PROJ" --sqlite "$DB" --password-stdin

# Роль назначается ДО старта сервера: права разбираются при загрузке, и
# назначение роли работающему серверу подхватывается только после перезапуска.
"$OB" user role assign "${OB_USER_LOGIN:-user}" "${OB_USER_ROLE:-Кладовщик}" \
  --project "$PROJ" --sqlite "$DB" >/dev/null

# Демо-данные: без них список справочника пуст и проводить нечего.
"$OB" procrun --project "$PROJ" --sqlite "$DB" --proc ЗаполнитьТестовуюБазу >/dev/null

exec "$OB" run --project "$PROJ" --sqlite "$DB" --port "$PORT" --host 127.0.0.1
