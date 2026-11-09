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

# Log only fixed phase names, status and duration; never commands or stdin.
phase=prepare
phase_started=$SECONDS
server_pid=""
start_phase() {
  phase="$1"
  phase_started=$SECONDS
  printf '[e2e] phase=%s start\n' "$phase" >&2
}
finish_phase() {
  printf '[e2e] phase=%s exit=0 elapsed=%ss\n' "$phase" "$((SECONDS - phase_started))" >&2
  phase=""
}
cleanup() {
  status=$?
  trap - EXIT INT TERM
  if [ -n "$server_pid" ]; then
    kill "$server_pid" 2>/dev/null || true
    wait "$server_pid" 2>/dev/null || true
  fi
  if [ -n "$phase" ]; then
    printf '[e2e] phase=%s exit=%s elapsed=%ss\n' "$phase" "$status" "$((SECONDS - phase_started))" >&2
  fi
  exit "$status"
}
trap cleanup EXIT
trap 'exit 130' INT
trap 'exit 143' TERM
start_phase prepare

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

# Exercise hidden_when in the shared trade suite without adding test-only
# documents to examples/trade. An explicit OB_PROJECT owns its fixtures:
# trade-only documents may reference entities absent from that project.
cp -R "$PROJ" "$WORK/project"
if [ -z "${OB_PROJECT:-}" ]; then
  cp -R "$REPO/e2e/fixtures/." "$WORK/project/"
fi
PROJ="$WORK/project"

finish_phase

# CI builds outside WORK before readiness. An explicit invalid binary must fail,
# rather than silently rebuilding it inside the webServer timeout.
start_phase build
OB="${OB_BIN:-$WORK/onebase}"
if [ -n "${OB_BIN:-}" ]; then
  if [ ! -x "$OB" ]; then
    printf '[e2e] supplied binary is not executable\n' >&2
    exit 1
  fi
else
  go build -o "$OB" "$REPO/cmd/onebase"
fi
finish_phase

# А вот сервер запускаем уже с изолированным HOME: туда он кладёт вложения
# (~/.onebase/files) и реестр баз, и прогоны не должны пачкать друг друга.
export HOME="$WORK/home"
mkdir -p "$HOME"

start_phase migrate
"$OB" migrate --project "$PROJ" --sqlite "$DB"
finish_phase

start_phase users

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

# Независимая личная раскладка второго обычного пользователя (#1362).
printf '%s' "${OB_SECOND_USER_PASSWORD:-Us3r-P@ssw0rd!}" |
  "$OB" user add "${OB_SECOND_USER_LOGIN:-second-user}" --name "Второй пользователь" \
    --project "$PROJ" --sqlite "$DB" --password-stdin
finish_phase

start_phase roles
"$OB" user role assign "${OB_USER_LOGIN:-user}" "${OB_USER_ROLE:-Кладовщик}" \
  --project "$PROJ" --sqlite "$DB" >/dev/null
"$OB" user role assign "${OB_SECOND_USER_LOGIN:-second-user}" "${OB_USER_ROLE:-Кладовщик}" \
  --project "$PROJ" --sqlite "$DB" >/dev/null
finish_phase

# Демо-данные: без них список справочника пуст и проводить нечего.
start_phase seed
"$OB" procrun --project "$PROJ" --sqlite "$DB" --proc ЗаполнитьТестовуюБазу >/dev/null
finish_phase

start_phase serve
"$OB" run --project "$PROJ" --sqlite "$DB" --port "$PORT" --host 127.0.0.1 &
server_pid=$!
wait "$server_pid"
server_pid=""
finish_phase
