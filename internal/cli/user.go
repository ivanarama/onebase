package cli

import (
	"context"
	"crypto/rand"
	"fmt"
	"io"
	"math/big"
	"os"
	"path/filepath"
	"strings"

	"github.com/ivantit66/onebase/internal/auth"
	"github.com/ivantit66/onebase/internal/storage"
	"github.com/spf13/cobra"
)

// Команда `onebase user` (план 112) — офлайн-управление пользователями базы.
// Граница доверия — файл/DSN базы (тот же класс операции, что migrate/procrun):
// у кого есть доступ к базе, тот и так может править _users сырым SQL. CLI делает
// это корректно — через auth.Repo (bcrypt, парольная политика, инвариант «первый
// пользователь — администратор», аудит), не переизобретая SQL.

var userCmd = &cobra.Command{
	Use:   "user",
	Short: "Управление пользователями базы (офлайн)",
	Long: `Заведение и обслуживание учётных записей вне запущенного сервера — для
сгенерированных, демо- и тестовых баз, где иначе пользователей пришлось бы
создавать вручную через веб-админку.

Операция локальная и офлайновая (над --project/--sqlite/--db), как migrate.
Пароли не передаются через аргументы (утекли бы в history/ps): используйте
--generate (случайный пароль печатается) или --password-stdin.

Примеры:
  onebase user add admin --name "Администратор" --admin --generate
  onebase user add kladovshchik --name "Кладовщик" --show-in-list --password-stdin < pass.txt
  onebase user show-in-list kladovshchik --on
  onebase user role assign kladovshchik Кладовщик
  onebase user list --sqlite base.db`,
	SilenceUsage:  true,
	SilenceErrors: true,
}

var userListCmd = &cobra.Command{
	Use:   "list",
	Short: "Список пользователей (логин, признак админа, показ в списках выбора, полное имя, роли)",
	RunE:  runUserList,
}

var userAddCmd = &cobra.Command{
	Use:   "add <login>",
	Short: "Создать пользователя",
	Args:  cobra.ExactArgs(1),
	RunE:  runUserAdd,
}

var userPasswdCmd = &cobra.Command{
	Use:   "passwd <login>",
	Short: "Сменить пароль пользователя (сбрасывает его сессии)",
	Args:  cobra.ExactArgs(1),
	RunE:  runUserPasswd,
}

var userRmCmd = &cobra.Command{
	Use:   "rm <login>",
	Short: "Удалить пользователя (нельзя удалить последнего админа/пользователя)",
	Args:  cobra.ExactArgs(1),
	RunE:  runUserRm,
}

var userShowInListCmd = &cobra.Command{
	Use:   "show-in-list <login>",
	Short: "Показывать/скрывать пользователя в списках выбора (--on/--off)",
	Args:  cobra.ExactArgs(1),
	RunE:  runUserShowInList,
}

var userRoleCmd = &cobra.Command{
	Use:   "role",
	Short: "Назначение ролей пользователям",
}

var userRoleAssignCmd = &cobra.Command{
	Use:   "assign <login> <роль>",
	Short: "Назначить роль (роли берутся из roles/*.yaml проекта)",
	Args:  cobra.ExactArgs(2),
	RunE:  runUserRoleAssign,
}

var userRoleRevokeCmd = &cobra.Command{
	Use:   "revoke <login> <роль>",
	Short: "Снять роль с пользователя",
	Args:  cobra.ExactArgs(2),
	RunE:  runUserRoleRevoke,
}

var user2FACmd = &cobra.Command{
	Use:   "2fa",
	Short: "Второй фактор учётной записи",
}

var user2FAResetForce bool

var user2FAResetCmd = &cobra.Command{
	Use:   "reset <login>",
	Short: "Снять второй фактор с учётной записи (офлайн-восстановление доступа)",
	Long: `Отключает второй фактор у учётной записи по прямому доступу к базе — без
входа и без другого администратора.

Офлайн-выход, когда утрачено устройство с аутентификатором.

ВАЖНО: сама по себе команда НЕ снимает требование второго фактора. Если политика
его требует, пользователь после сброса пойдёт на первичную привязку, а она
просит одноразовый код от администратора. Поэтому команда отказывается снимать
фактор у последней учётной записи когорты, которая его привязала: иначе выдать
код станет некому и база запрётся насовсем (#615).

Выход из уже запертой базы — onebase user 2fa self-enroll on: разрешает
привязку второго фактора на входе по паролю, не снимая самого требования.`,
	Args: cobra.ExactArgs(1),
	RunE: runUser2FAReset,
}

var user2FASelfEnrollCmd = &cobra.Command{
	Use:   "self-enroll <on|off>",
	Short: "Разрешить первичную привязку второго фактора на входе (офлайн-выход из блокировки)",
	Long: `Включает или выключает self_enroll_2fa в политике аутентификации по прямому
доступу к базе — без входа в веб-интерфейс.

Это продуктовый выход из тупика, в котором политика требует второй фактор от
когорты, где его никто не привязал: привязка на входе просит одноразовый код от
администратора, а выдать его некому, потому что администратор сам не может
войти. Раньше выхода не было вовсе — только правка таблицы _settings сырым SQL.

Требование второго фактора при этом остаётся в силе: меняется только то, что
привязать его можно самому, предъявив пароль. Выключать обратно (off) стоит
после того, как фактор привязан хотя бы у одного администратора.`,
	Args: cobra.ExactArgs(1),
	RunE: runUser2FASelfEnroll,
}

func init() {
	// Базовые флаги выбора базы — персистентные на группе, чтобы их видели все
	// подкоманды (resolveBase читает их из cmd.Flags(), куда cobra подмешивает
	// унаследованные персистентные флаги).
	fs := userCmd.PersistentFlags()
	fs.String("id", "", "ID базы из реестра ibases")
	fs.String("project", ".", "путь к каталогу конфигурации")
	fs.String("sqlite", "", "путь к файлу SQLite (альтернатива --db)")
	fs.String("db", "", "PostgreSQL DSN (или переменная DATABASE_URL)")

	userAddCmd.Flags().String("name", "", "полное имя пользователя")
	userAddCmd.Flags().Bool("admin", false, "сделать администратором")
	userAddCmd.Flags().Bool("show-in-list", false, "показывать в списках выбора (reference-пикерах)")
	addPasswordFlags(userAddCmd)
	addPasswordFlags(userPasswdCmd)

	userShowInListCmd.Flags().Bool("on", false, "показывать в списках выбора")
	userShowInListCmd.Flags().Bool("off", false, "скрыть из списков выбора")

	userRoleCmd.AddCommand(userRoleAssignCmd, userRoleRevokeCmd)
	user2FACmd.AddCommand(user2FAResetCmd)
	user2FACmd.AddCommand(user2FASelfEnrollCmd)
	user2FAResetCmd.Flags().BoolVar(&user2FAResetForce, "force", false,
		"снять фактор, даже если после этого войти не сможет никто")
	userCmd.AddCommand(userListCmd, userAddCmd, userPasswdCmd, userRmCmd, userShowInListCmd, userRoleCmd, user2FACmd)
	rootCmd.AddCommand(userCmd)
}

func runUser2FASelfEnroll(cmd *cobra.Command, args []string) error {
	mode := strings.ToLower(strings.TrimSpace(args[0]))
	var on bool
	switch mode {
	case "on", "вкл", "true", "1":
		on = true
	case "off", "выкл", "false", "0":
		on = false
	default:
		return fmt.Errorf("ожидается on или off, получено %q", args[0])
	}
	env, err := openUserEnv(cmd)
	if err != nil {
		return err
	}
	defer env.Close()

	ctx := context.Background()
	policy := env.repo.AuthPolicy(ctx)
	if policy.SelfEnroll2FA == on {
		outf("Самопривязка второго фактора уже %s\n", onOff(on))
		return nil
	}
	policy.SelfEnroll2FA = on
	if err := env.repo.SaveAuthPolicy(ctx, policy); err != nil {
		return err
	}
	env.db.LogAction(ctx, "auth_policy_self_enroll", "policy", onOff(on), "", "", "cli", "")
	outf("Самопривязка второго фактора %s\n", onOff(on))
	if on {
		outf("Теперь второй фактор можно привязать прямо на входе, предъявив пароль.\n" +
			"После того как фактор привязан хотя бы у одного администратора, выключите: onebase user 2fa self-enroll off\n")
	}
	return nil
}

func onOff(v bool) string {
	if v {
		return "включена"
	}
	return "выключена"
}

func runUser2FAReset(cmd *cobra.Command, args []string) error {
	login := strings.TrimSpace(args[0])
	env, err := openUserEnv(cmd)
	if err != nil {
		return err
	}
	defer env.Close()

	ctx := context.Background()
	u, err := findUserByLogin(ctx, env.repo, login)
	if err != nil {
		return err
	}
	// Снятие фактора у последнего, кто его привязал, запирает базу насовсем:
	// политика продолжит требовать второй фактор, а выдать код привязки станет
	// некому. Именно так команда, которую справка называет средством
	// восстановления, работала входом в тупик (#615).
	policy := env.repo.AuthPolicy(ctx)
	if policy.Enabled() {
		cohort, riskErr := env.repo.TwoFactorLockoutRiskAfterDisable(ctx, policy, u.ID)
		if riskErr != nil {
			return riskErr
		}
		if cohort != "" && !user2FAResetForce {
			return fmt.Errorf("нельзя снять второй фактор у %s: это последняя учётная запись из %s с привязанным фактором, "+
				"и после сброса войти не сможет никто — политика продолжит требовать второй фактор, а выдать код привязки будет некому.\n"+
				"Разрешите привязку на входе: onebase user 2fa self-enroll on\n"+
				"Если это осознанно (учётная запись всё равно недоступна) — повторите с --force", login, cohort)
		}
	}
	if err := env.repo.DisableTOTP(ctx, u.ID); err != nil {
		return err
	}
	// Сессии на всякий случай гасим: у восстановления доступа не должно оставаться
	// хвостов, а второй фактор мог сбрасывать не сам владелец.
	_ = env.repo.KickUserSessions(ctx, u.ID)
	env.db.LogAction(ctx, "user_2fa_reset", "user", login, u.ID, "", "cli", "")
	outf("Второй фактор снят у %s\n", login)
	return nil
}

func addPasswordFlags(cmd *cobra.Command) {
	cmd.Flags().Bool("generate", false, "сгенерировать случайный пароль и напечатать его")
	cmd.Flags().Bool("password-stdin", false, "прочитать пароль из stdin (для скриптов/CI)")
}

// userEnv — открытая база + auth-репозиторий со схемой, готовые к операциям.
type userEnv struct {
	bc   *baseConfig
	db   *storage.DB
	repo *auth.Repo
}

func (e *userEnv) Close() {
	if e.db != nil {
		e.db.Close()
	}
	if e.bc != nil {
		e.bc.Cleanup()
	}
}

func openUserEnv(cmd *cobra.Command) (*userEnv, error) {
	bc, err := resolveBase(cmd)
	if err != nil {
		return nil, err
	}
	ctx := context.Background()
	db, err := bc.OpenDB(ctx)
	if err != nil {
		bc.Cleanup()
		return nil, err
	}
	repo := auth.NewRepo(db)
	if err := repo.EnsureSchema(ctx); err != nil {
		db.Close()
		bc.Cleanup()
		return nil, fmt.Errorf("схема auth: %w", err)
	}
	if err := db.EnsureAuditSchema(ctx); err != nil {
		db.Close()
		bc.Cleanup()
		return nil, fmt.Errorf("схема аудита: %w", err)
	}
	return &userEnv{bc: bc, db: db, repo: repo}, nil
}

func runUserList(cmd *cobra.Command, _ []string) error {
	env, err := openUserEnv(cmd)
	if err != nil {
		return err
	}
	defer env.Close()

	ctx := context.Background()
	users, err := env.repo.List(ctx)
	if err != nil {
		return err
	}
	if len(users) == 0 {
		outln("Пользователей нет.")
		return nil
	}
	for _, u := range users {
		roles, _ := env.repo.GetRolesForUser(ctx, u.ID)
		line := u.Login
		if u.IsAdmin {
			line += " [админ]"
		}
		if u.ShowInList {
			line += " [в списках]"
		}
		if u.FullName != "" {
			line += "  — " + u.FullName
		}
		if names := roleNames(roles); len(names) > 0 {
			line += "  роли: " + strings.Join(names, ", ")
		}
		outln(line)
	}
	return nil
}

func runUserAdd(cmd *cobra.Command, args []string) error {
	login := strings.TrimSpace(args[0])
	if login == "" {
		return fmt.Errorf("логин не может быть пустым")
	}
	name, _ := cmd.Flags().GetString("name")
	isAdmin, _ := cmd.Flags().GetBool("admin")
	showInList, _ := cmd.Flags().GetBool("show-in-list")
	pw, generated, err := resolvePassword(cmd)
	if err != nil {
		return err
	}

	env, err := openUserEnv(cmd)
	if err != nil {
		return err
	}
	defer env.Close()

	ctx := context.Background()
	u, err := env.repo.CreateManaged(ctx, login, pw, name, isAdmin)
	if err != nil {
		return err
	}
	// Пользователь уже в базе (Create идёт в автокоммите, общей транзакции с
	// SetShowInList нет). Поэтому сперва фиксируем создание в аудите и печатаем
	// сгенерированный пароль — он живёт только в памяти процесса, и если его не
	// показать, учётка останется недоступной. Только после этого выставляем
	// видимость: её сбой не должен стоить пользователю пароля.
	env.db.LogAction(ctx, "user_create", "user", login, u.ID, "", "cli", "")

	if isAdmin {
		outf("Создан пользователь %s (администратор)\n", login)
	} else {
		outf("Создан пользователь %s\n", login)
	}
	if generated {
		outf("Пароль: %s\n", pw)
	}

	if showInList {
		if err := env.repo.SetShowInList(ctx, u.ID, true); err != nil {
			return fmt.Errorf("пользователь %s создан, но флаг видимости не применён: %w", login, err)
		}
		outf("Пользователь %s показывается в списках выбора\n", login)
	}
	return nil
}

func runUserPasswd(cmd *cobra.Command, args []string) error {
	login := strings.TrimSpace(args[0])
	pw, generated, err := resolvePassword(cmd)
	if err != nil {
		return err
	}

	env, err := openUserEnv(cmd)
	if err != nil {
		return err
	}
	defer env.Close()

	ctx := context.Background()
	u, err := findUserByLogin(ctx, env.repo, login)
	if err != nil {
		return err
	}
	if err := env.repo.UpdatePassword(ctx, u.ID, pw); err != nil {
		return err
	}
	// Отзыв живых сессий: смену пароля часто делают именно чтобы кого-то
	// отключить, и «отозвать не удалось» нельзя проглатывать в «Пароль обновлён»
	// — иначе администратор получит ложное подтверждение (#622). Пароль уже
	// изменён, поэтому сообщаем оба факта и завершаемся ненулевым кодом.
	kickErr := env.repo.KickUserSessions(ctx, u.ID)
	env.db.LogAction(ctx, "user_passwd", "user", login, u.ID, "", "cli", "")

	outf("Пароль обновлён для %s\n", login)
	if generated {
		outf("Пароль: %s\n", pw)
	}
	if kickErr != nil {
		return fmt.Errorf("пароль изменён, но действующие сессии %s отозвать не удалось — прежний вход мог сохраниться: %w", login, kickErr)
	}
	return nil
}

func runUserRm(cmd *cobra.Command, args []string) error {
	login := strings.TrimSpace(args[0])
	env, err := openUserEnv(cmd)
	if err != nil {
		return err
	}
	defer env.Close()

	ctx := context.Background()
	u, err := findUserByLogin(ctx, env.repo, login)
	if err != nil {
		return err
	}
	if err := env.repo.Delete(ctx, u.ID); err != nil {
		return err
	}
	env.db.LogAction(ctx, "user_delete", "user", login, u.ID, "", "cli", "")
	outf("Пользователь %s удалён\n", login)
	return nil
}

func runUserShowInList(cmd *cobra.Command, args []string) error {
	login := strings.TrimSpace(args[0])
	// Различаем «флаг не передан» и «передан со значением»: cobra допускает явную
	// форму --on=false, и по одному лишь значению её не отличить от отсутствия
	// флага. Проверка идёт по факту передачи — как в resolvePassword для
	// --generate/--password-stdin.
	onSet := cmd.Flags().Changed("on")
	offSet := cmd.Flags().Changed("off")
	switch {
	case !onSet && !offSet:
		return fmt.Errorf("укажите --on или --off")
	case onSet && offSet:
		return fmt.Errorf("флаги --on и --off взаимоисключающи")
	}
	on, _ := cmd.Flags().GetBool("on")
	if offSet {
		off, _ := cmd.Flags().GetBool("off")
		on = !off
	}

	env, err := openUserEnv(cmd)
	if err != nil {
		return err
	}
	defer env.Close()

	ctx := context.Background()
	u, err := findUserByLogin(ctx, env.repo, login)
	if err != nil {
		return err
	}
	if err := env.repo.SetShowInList(ctx, u.ID, on); err != nil {
		return err
	}
	env.db.LogAction(ctx, "user_show_in_list", "user", login, u.ID, "", "cli", "")
	if on {
		outf("Пользователь %s показывается в списках выбора\n", login)
	} else {
		outf("Пользователь %s скрыт из списков выбора\n", login)
	}
	return nil
}

func runUserRoleAssign(cmd *cobra.Command, args []string) error {
	return changeUserRole(cmd, args[0], args[1], true)
}

func runUserRoleRevoke(cmd *cobra.Command, args []string) error {
	return changeUserRole(cmd, args[0], args[1], false)
}

func changeUserRole(cmd *cobra.Command, login, roleName string, assign bool) error {
	env, err := openUserEnv(cmd)
	if err != nil {
		return err
	}
	defer env.Close()

	ctx := context.Background()
	// Роли — из roles/*.yaml проекта: синкаем, чтобы назначаемая роль точно была
	// в базе (иначе на свежей базе её ещё нет).
	if err := syncProjectRoles(ctx, env.bc, env.repo); err != nil {
		return err
	}
	u, err := findUserByLogin(ctx, env.repo, login)
	if err != nil {
		return err
	}
	role, err := findRoleByName(ctx, env.repo, roleName)
	if err != nil {
		return err
	}
	if assign {
		if err := env.repo.AssignRole(ctx, u.ID, role.ID); err != nil {
			return err
		}
		env.db.LogAction(ctx, "role_assign", "user", login, u.ID, "", "cli", "")
		outf("Роль «%s» назначена пользователю %s\n", role.Name, login)
		return nil
	}
	if err := env.repo.UnassignRole(ctx, u.ID, role.ID); err != nil {
		return err
	}
	env.db.LogAction(ctx, "role_revoke", "user", login, u.ID, "", "cli", "")
	outf("Роль «%s» снята с пользователя %s\n", role.Name, login)
	return nil
}

// syncProjectRoles грузит roles/*.yaml из каталога базы в таблицу ролей.
func syncProjectRoles(ctx context.Context, bc *baseConfig, repo *auth.Repo) error {
	roles, err := auth.LoadRolesYAML(filepath.Join(bc.Dir, "roles"))
	if err != nil {
		return fmt.Errorf("загрузка roles/*.yaml: %w", err)
	}
	if len(roles) == 0 {
		return nil
	}
	return repo.SyncRoles(ctx, roles)
}

func findUserByLogin(ctx context.Context, repo *auth.Repo, login string) (*auth.User, error) {
	users, err := repo.List(ctx)
	if err != nil {
		return nil, err
	}
	for _, u := range users {
		if u.Login == login {
			return u, nil
		}
	}
	return nil, fmt.Errorf("пользователь %q не найден", login)
}

func findRoleByName(ctx context.Context, repo *auth.Repo, name string) (*auth.Role, error) {
	roles, err := repo.ListRoles(ctx)
	if err != nil {
		return nil, err
	}
	for _, r := range roles {
		if strings.EqualFold(r.Name, name) {
			return r, nil
		}
	}
	return nil, fmt.Errorf("роль %q не найдена (доступны: %s)", name, strings.Join(roleNames(roles), ", "))
}

func roleNames(roles []*auth.Role) []string {
	names := make([]string, 0, len(roles))
	for _, r := range roles {
		names = append(names, r.Name)
	}
	return names
}

// resolvePassword выбирает источник пароля. Пароль намеренно не берётся из
// аргументов командной строки. Возвращает (пароль, сгенерирован ли, ошибка).
func resolvePassword(cmd *cobra.Command) (string, bool, error) {
	fromStdin, _ := cmd.Flags().GetBool("password-stdin")
	gen, _ := cmd.Flags().GetBool("generate")
	if fromStdin && gen {
		return "", false, fmt.Errorf("--generate и --password-stdin взаимоисключающи")
	}
	if fromStdin {
		b, err := io.ReadAll(os.Stdin)
		if err != nil {
			return "", false, fmt.Errorf("чтение пароля из stdin: %w", err)
		}
		pw := strings.TrimRight(string(b), "\r\n")
		if pw == "" {
			return "", false, fmt.Errorf("пустой пароль из stdin")
		}
		return pw, false, nil
	}
	// По умолчанию (и при явном --generate) — генерируем случайный пароль.
	pw, err := generatePassword(16)
	if err != nil {
		return "", false, err
	}
	return pw, true, nil
}

// passwordAlphabet — без визуально неоднозначных символов (0/O, 1/l/I).
const passwordAlphabet = "abcdefghijkmnpqrstuvwxyzABCDEFGHJKLMNPQRSTUVWXYZ23456789"

func generatePassword(n int) (string, error) {
	b := make([]byte, n)
	max := big.NewInt(int64(len(passwordAlphabet)))
	for i := range b {
		idx, err := rand.Int(rand.Reader, max)
		if err != nil {
			return "", fmt.Errorf("генерация пароля: %w", err)
		}
		b[i] = passwordAlphabet[idx.Int64()]
	}
	return string(b), nil
}
