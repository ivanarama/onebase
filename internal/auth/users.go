package auth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"github.com/google/uuid"
	"golang.org/x/crypto/bcrypt"

	"github.com/ivantit66/onebase/internal/storage"
)

type User struct {
	ID               string
	Login            string
	FullName         string
	IsAdmin          bool
	DenyPasswdChange bool
	ShowInList       bool   // appears in reference pickers when true
	AIDataAccess     bool   // can use AI chat data tools without being admin
	Lang             string // preferred UI language ("" = use base default)
	CreatedAt        time.Time
	Attrs            map[string]any // optional host-provided attributes for row-level access
	Roles            []*Role        // loaded by middleware after session lookup
}

type Repo struct {
	db             *storage.DB
	passwordPolicy PasswordPolicy

	// usersExist latches true once the auth middleware observes that the base
	// has at least one user. HasUsers can never go true→false — deleting the
	// last user is refused (ErrLastUser) — so the latch is authoritative for the
	// process lifetime and lets protected requests skip SELECT count(*) FROM
	// _users. Only the middleware hot path reads it (hasUsersCached); HasUsers
	// itself stays an uncached ground-truth query for every other caller.
	// План 111 (P0-2).
	usersExist atomic.Bool

	// rolesCache memoizes GetRolesForUser per user for rolesCacheTTL on the auth
	// hot path, cutting a roles JOIN off every protected request. Invalidated
	// eagerly on role assignment/definition changes; the TTL is only a backstop.
	// План 111 (P0-2).
	rolesMu    sync.RWMutex
	rolesCache map[string]cachedRoles
}

// cachedRoles is one memoized GetRolesForUser result. The slice is shared across
// requests until invalidated or expired — treat it as read-only.
type cachedRoles struct {
	roles   []*Role
	expires time.Time
}

// rolesCacheTTL bounds how long stale roles can survive a change the eager
// invalidation somehow missed. The review suggests 30–60s (Plans/111 §3.2).
const rolesCacheTTL = 60 * time.Second

var (
	ErrFirstUserMustBeAdmin = errors.New("первый пользователь должен быть администратором")
	ErrLastAdmin            = errors.New("нельзя удалить или разжаловать последнего администратора")
	ErrLastUser             = errors.New("нельзя удалить последнего пользователя; авторизация должна отключаться отдельным действием")
)

// NewRepo wires the auth repository to the storage layer. Internally Exec/
// Query/QueryRow are routed to PostgreSQL or SQLite via the DB abstraction.
func NewRepo(db *storage.DB) *Repo {
	return &Repo{
		db:             db,
		passwordPolicy: passwordPolicyFromEnv(),
		rolesCache:     make(map[string]cachedRoles),
	}
}

func (r *Repo) EnsureSchema(ctx context.Context) error {
	d := r.db.Dialect()
	usersDDL := fmt.Sprintf(`
		CREATE TABLE IF NOT EXISTS _users (
			id %s PRIMARY KEY,
			login TEXT UNIQUE NOT NULL,
			password_hash %s NOT NULL,
			full_name TEXT NOT NULL DEFAULT '',
			is_admin %s NOT NULL DEFAULT %s,
			created_at %s NOT NULL DEFAULT %s
		)`, d.TypeUUID(), d.TypeBytes(), d.TypeBool(), boolFalseFor(d), d.TypeTimestamp(), d.CurrentTimestampTZ())
	if _, err := r.db.Exec(ctx, usersDDL); err != nil {
		return fmt.Errorf("auth: create _users: %w", err)
	}
	// Единственная строка служит переносимым mutex для инвариантов пользователей:
	// UPDATE берёт write-lock в SQLite и row-lock в PostgreSQL.
	if _, err := r.db.Exec(ctx, `CREATE TABLE IF NOT EXISTS _auth_user_guard (
		id INTEGER PRIMARY KEY CHECK (id = 1)
	)`); err != nil {
		return fmt.Errorf("auth: create user guard: %w", err)
	}
	if _, err := r.db.Exec(ctx, `INSERT INTO _auth_user_guard (id) VALUES (1) ON CONFLICT (id) DO NOTHING`); err != nil {
		return fmt.Errorf("auth: seed user guard: %w", err)
	}
	sessionsDDL := fmt.Sprintf(`
		CREATE TABLE IF NOT EXISTS _sessions (
			token TEXT PRIMARY KEY,
			token_hash TEXT NOT NULL,
			user_id %s NOT NULL REFERENCES _users(id) ON DELETE CASCADE,
			expires_at %s NOT NULL,
			public_id TEXT,
			kind TEXT,
			created_at %s,
			last_seen_at %s,
			ip TEXT,
			user_agent TEXT
		)`, d.TypeUUID(), d.TypeTimestamp(), d.TypeTimestamp(), d.TypeTimestamp())
	if _, err := r.db.Exec(ctx, sessionsDDL); err != nil {
		return fmt.Errorf("auth: create _sessions: %w", err)
	}
	if err := r.EnsureRolesSchema(ctx); err != nil {
		return err
	}
	if err := r.EnsureAPITokenSchema(ctx); err != nil {
		return err
	}
	// Второй фактор и привязка к внешнему провайдеру (план 84). Колонки
	// добавляются всегда, но пустые: пока 2FA/SSO не включены, вход не меняется.
	if err := r.ensureTwoFactorSchema(ctx); err != nil {
		return err
	}
	// Догоняющие миграции для баз, созданных до появления этих колонок.
	// На свежей базе колонки уже созданы через CREATE TABLE выше, поэтому проба
	// обязана быть идемпотентной — этим занимается storage.AddColumnIfMissing
	// (PostgreSQL: ADD COLUMN IF NOT EXISTS, SQLite: проверка каталога), а не
	// разбор текста ошибки драйвера.
	for _, c := range []struct{ col, typ string }{
		{"deny_passwd_change", d.TypeBool() + " NOT NULL DEFAULT " + boolFalseFor(d)},
		{"show_in_list", d.TypeBool() + " NOT NULL DEFAULT " + boolFalseFor(d)},
		{"lang", "TEXT NOT NULL DEFAULT ''"},
		{"ai_data_access", d.TypeBool() + " NOT NULL DEFAULT " + boolFalseFor(d)},
	} {
		if err := r.db.AddColumnIfMissing(ctx, "_users", c.col, c.typ); err != nil {
			return fmt.Errorf("auth: migrate _users.%s: %w", c.col, err)
		}
	}
	// Мультисессии (план 78): служебные метаданные сессии. Колонки nullable без
	// DEFAULT — SQLite не разрешает ни UNIQUE, ни CURRENT_TIMESTAMP в ADD COLUMN;
	// уникальность public_id обеспечивает отдельный индекс.
	for _, c := range []struct{ col, typ string }{
		{"token_hash", "TEXT"},
		{"public_id", "TEXT"},
		{"kind", "TEXT"},
		{"created_at", d.TypeTimestamp()},
		{"last_seen_at", d.TypeTimestamp()},
		{"ip", "TEXT"},
		{"user_agent", "TEXT"},
	} {
		if err := r.db.AddColumnIfMissing(ctx, "_sessions", c.col, c.typ); err != nil {
			return fmt.Errorf("auth: migrate _sessions.%s: %w", c.col, err)
		}
	}
	if err := r.migrateSessionTokens(ctx); err != nil {
		return fmt.Errorf("auth: hash legacy session tokens: %w", err)
	}
	for _, ddl := range []string{
		`CREATE UNIQUE INDEX IF NOT EXISTS ix_sessions_token_hash ON _sessions(token_hash)`,
		`CREATE UNIQUE INDEX IF NOT EXISTS ix_sessions_public_id ON _sessions(public_id)`,
		`CREATE INDEX IF NOT EXISTS ix_sessions_user_id ON _sessions(user_id)`,
		`CREATE INDEX IF NOT EXISTS ix_sessions_expires_at ON _sessions(expires_at)`,
	} {
		if _, err := r.db.Exec(ctx, ddl); err != nil {
			return fmt.Errorf("auth: index _sessions: %w", err)
		}
	}
	return nil
}

// boolFalseFor returns "FALSE" for PG and "0" for SQLite, used in DEFAULT clauses.
func boolFalseFor(d storage.Dialect) string {
	if d.Name() == "sqlite" {
		return "0"
	}
	return "FALSE"
}

func (r *Repo) HasUsers(ctx context.Context) (bool, error) {
	var count int
	err := r.db.QueryRow(ctx, `SELECT count(*) FROM _users`).Scan(&count)
	return count > 0, err
}

func (r *Repo) List(ctx context.Context) ([]*User, error) {
	return r.listWhere(ctx, "")
}

// ListForSelection returns only users with show_in_list=true, for reference pickers.
func (r *Repo) ListForSelection(ctx context.Context) ([]*User, error) {
	return r.listWhere(ctx, "WHERE show_in_list")
}

func (r *Repo) listWhere(ctx context.Context, where string) ([]*User, error) {
	q := `SELECT id, login, full_name, is_admin, deny_passwd_change, show_in_list, ai_data_access, lang, created_at FROM _users ` + where + ` ORDER BY login`
	rows, err := r.db.Query(ctx, q)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var users []*User
	for rows.Next() {
		u := &User{}
		var isAdmin, denyPasswd, showInList, aiData, createdAt any
		if err := rows.Scan(&u.ID, &u.Login, &u.FullName, &isAdmin, &denyPasswd, &showInList, &aiData, &u.Lang, &createdAt); err != nil {
			return nil, err
		}
		u.IsAdmin = scanBool(isAdmin)
		u.DenyPasswdChange = scanBool(denyPasswd)
		u.ShowInList = scanBool(showInList)
		u.AIDataAccess = scanBool(aiData)
		u.CreatedAt = scanTime(createdAt)
		users = append(users, u)
	}
	return users, nil
}

// GetByID returns a single user by ID.
func (r *Repo) GetByID(ctx context.Context, userID string) (*User, error) {
	d := r.db.Dialect()
	u := &User{}
	var isAdmin, denyPasswd, showInList, aiData, createdAt any
	q := fmt.Sprintf(`SELECT id, login, full_name, is_admin, deny_passwd_change, show_in_list, ai_data_access, lang, created_at FROM _users WHERE id = %s`, d.Placeholder(1))
	if err := r.db.QueryRow(ctx, q, userID).Scan(&u.ID, &u.Login, &u.FullName, &isAdmin, &denyPasswd, &showInList, &aiData, &u.Lang, &createdAt); err != nil {
		return nil, err
	}
	u.IsAdmin = scanBool(isAdmin)
	u.DenyPasswdChange = scanBool(denyPasswd)
	u.ShowInList = scanBool(showInList)
	u.AIDataAccess = scanBool(aiData)
	u.CreatedAt = scanTime(createdAt)
	return u, nil
}

// Update saves editable fields on a user.
func (r *Repo) Update(ctx context.Context, userID, fullName string, isAdmin, denyPasswdChange, showInList, aiDataAccess bool) error {
	return r.withUserInvariantLock(ctx, func(txCtx context.Context) error {
		d := r.db.Dialect()
		var currentAdmin any
		qCurrent := fmt.Sprintf(`SELECT is_admin FROM _users WHERE id = %s`, d.Placeholder(1))
		if err := r.db.QueryRow(txCtx, qCurrent, userID).Scan(&currentAdmin); err != nil {
			return err
		}
		if scanBool(currentAdmin) && !isAdmin {
			admins, err := r.adminCount(txCtx)
			if err != nil {
				return err
			}
			if admins <= 1 {
				return ErrLastAdmin
			}
		}
		q := fmt.Sprintf(`UPDATE _users SET full_name=%s, is_admin=%s, deny_passwd_change=%s, show_in_list=%s, ai_data_access=%s WHERE id=%s`,
			d.Placeholder(1), d.Placeholder(2), d.Placeholder(3), d.Placeholder(4), d.Placeholder(5), d.Placeholder(6))
		_, err := r.db.Exec(txCtx, q, fullName, isAdmin, denyPasswdChange, showInList, aiDataAccess, userID)
		return err
	})
}

// SetShowInList toggles the show_in_list flag for a user.
func (r *Repo) SetShowInList(ctx context.Context, userID string, show bool) error {
	d := r.db.Dialect()
	q := fmt.Sprintf(`UPDATE _users SET show_in_list = %s WHERE id = %s`, d.Placeholder(1), d.Placeholder(2))
	_, err := r.db.Exec(ctx, q, show, userID)
	return err
}

// SetAIDataAccess toggles the ai_data_access flag for a user.
func (r *Repo) SetAIDataAccess(ctx context.Context, userID string, allow bool) error {
	d := r.db.Dialect()
	q := fmt.Sprintf(`UPDATE _users SET ai_data_access = %s WHERE id = %s`, d.Placeholder(1), d.Placeholder(2))
	_, err := r.db.Exec(ctx, q, allow, userID)
	return err
}

// SetUserLang sets the preferred UI language for a user.
func (r *Repo) SetUserLang(ctx context.Context, userID, lang string) error {
	d := r.db.Dialect()
	q := fmt.Sprintf(`UPDATE _users SET lang = %s WHERE id = %s`, d.Placeholder(1), d.Placeholder(2))
	_, err := r.db.Exec(ctx, q, lang, userID)
	return err
}

func scanBool(v any) bool {
	switch t := v.(type) {
	case bool:
		return t
	case int64:
		return t != 0
	case int:
		return t != 0
	}
	return false
}

func scanTime(v any) time.Time {
	if t := storage.ParseDBTime(v); !t.IsZero() {
		return t
	}
	if t, ok := v.(time.Time); ok {
		return t
	}
	if s, ok := v.(string); ok {
		for _, layout := range []string{time.RFC3339, time.RFC3339Nano, "2006-01-02 15:04:05 -0700 MST", "2006-01-02T15:04:05", "2006-01-02 15:04:05", "2006-01-02"} {
			if t, err := time.Parse(layout, s); err == nil {
				return t
			}
		}
	}
	return time.Time{}
}

func (r *Repo) Create(ctx context.Context, login, password, fullName string, isAdmin bool) (*User, error) {
	if err := r.EffectivePasswordPolicy(ctx).validate(password); err != nil {
		return nil, err
	}
	d := r.db.Dialect()
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return nil, err
	}
	id := uuid.New().String()
	q := fmt.Sprintf(`INSERT INTO _users (id, login, password_hash, full_name, is_admin) VALUES (%s, %s, %s, %s, %s)`,
		d.Placeholder(1), d.Placeholder(2), d.Placeholder(3), d.Placeholder(4), d.Placeholder(5))
	_, err = r.db.Exec(ctx, q, id, login, hash, fullName, isAdmin)
	if err != nil {
		return nil, fmt.Errorf("auth: create user: %w", err)
	}
	return &User{ID: id, Login: login, FullName: fullName, IsAdmin: isAdmin}, nil
}

// CreateManaged is the user-management entry point. Unlike low-level Create,
// it enforces that authentication can only be enabled by an administrator.
func (r *Repo) CreateManaged(ctx context.Context, login, password, fullName string, isAdmin bool) (*User, error) {
	if !isAdmin {
		err := r.withUserInvariantLock(ctx, func(txCtx context.Context) error {
			hasUsers, err := r.HasUsers(txCtx)
			if err != nil {
				return err
			}
			if !hasUsers {
				return ErrFirstUserMustBeAdmin
			}
			return nil
		})
		if err != nil {
			return nil, err
		}
	}
	return r.Create(ctx, login, password, fullName, isAdmin)
}

func (r *Repo) Delete(ctx context.Context, id string) error {
	return r.withUserInvariantLock(ctx, func(txCtx context.Context) error {
		d := r.db.Dialect()
		var targetAdmin any
		qTarget := fmt.Sprintf(`SELECT is_admin FROM _users WHERE id = %s`, d.Placeholder(1))
		if err := r.db.QueryRow(txCtx, qTarget, id).Scan(&targetAdmin); err != nil {
			return err
		}
		var users int
		if err := r.db.QueryRow(txCtx, `SELECT count(*) FROM _users`).Scan(&users); err != nil {
			return err
		}
		if users <= 1 {
			return ErrLastUser
		}
		if scanBool(targetAdmin) {
			admins, err := r.adminCount(txCtx)
			if err != nil {
				return err
			}
			if admins <= 1 {
				return ErrLastAdmin
			}
		}
		q := fmt.Sprintf(`DELETE FROM _users WHERE id = %s`, d.Placeholder(1))
		_, err := r.db.Exec(txCtx, q, id)
		return err
	})
}

func (r *Repo) withUserInvariantLock(ctx context.Context, fn func(context.Context) error) error {
	return r.db.WithTxScope(ctx, func(txCtx context.Context) error {
		if _, err := r.db.Exec(txCtx, `UPDATE _auth_user_guard SET id = 1 WHERE id = 1`); err != nil {
			return fmt.Errorf("auth: lock user invariants: %w", err)
		}
		return fn(txCtx)
	})
}

func (r *Repo) adminCount(ctx context.Context) (int, error) {
	var count int
	q := fmt.Sprintf(`SELECT count(*) FROM _users WHERE is_admin = %s`, r.db.Dialect().Placeholder(1))
	err := r.db.QueryRow(ctx, q, true).Scan(&count)
	return count, err
}

func (r *Repo) Authenticate(ctx context.Context, login, password string) (*User, error) {
	d := r.db.Dialect()
	u := &User{}
	var hash []byte
	q := fmt.Sprintf(`SELECT id, login, password_hash, full_name, is_admin FROM _users WHERE login = %s`, d.Placeholder(1))
	err := r.db.QueryRow(ctx, q, login).Scan(&u.ID, &u.Login, &hash, &u.FullName, &u.IsAdmin)
	if err != nil {
		return nil, fmt.Errorf("auth: user not found")
	}
	if err := bcrypt.CompareHashAndPassword(hash, []byte(password)); err != nil {
		return nil, fmt.Errorf("auth: wrong password")
	}
	return u, nil
}

// SessionKind* — значения колонки _sessions.kind: откуда создана сессия.
const (
	SessionKindEnterprise   = "enterprise"   // пользовательский режим (Предприятие)
	SessionKindConfigurator = "configurator" // конфигуратор (лаунчер)
)

// SessionMeta — служебный контекст новой сессии (план 78): вид, IP и
// user-agent. Показывается в админке активных сессий; токен не содержит.
type SessionMeta struct {
	Kind      string
	IP        string
	UserAgent string
}

// CreateSession создаёт новую сессию пользователя. Живые сессии не трогает
// (мультисессии, план 78) — подчищает только истёкшие, так что рост _sessions
// ограничен TTL. Опциональная политика «максимум сессий на пользователя»
// (п. 1.6) может вытеснить старейшую enterprise-сессию.
func (r *Repo) CreateSession(ctx context.Context, userID string, meta SessionMeta) (string, error) {
	d := r.db.Dialect()
	// Уборка истёкших — попутная housekeeping-операция перед выдачей новой
	// сессии. Её сбой не должен мешать входу: пользователь войдёт, а таблица
	// подчистится при следующей попытке. Но молчать нельзя — постоянные сбои
	// означают неограниченный рост _sessions.
	if err := r.DeleteExpiredSessions(ctx); err != nil {
		authLog().Warn("не удалось подчистить истёкшие сессии", "err", err)
	}
	if meta.Kind == SessionKindEnterprise {
		r.enforceSessionLimit(ctx, userID, meta)
	}

	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	token := hex.EncodeToString(b)
	tokenHash := sessionTokenHash(token)
	now := time.Now()
	expires := now.Add(24 * time.Hour)
	q := fmt.Sprintf(`INSERT INTO _sessions (token, token_hash, user_id, expires_at, public_id, kind, created_at, last_seen_at, ip, user_agent)
		VALUES (%s, %s, %s, %s, %s, %s, %s, %s, %s, %s)`,
		d.Placeholder(1), d.Placeholder(2), d.Placeholder(3), d.Placeholder(4), d.Placeholder(5),
		d.Placeholder(6), d.Placeholder(7), d.Placeholder(8), d.Placeholder(9), d.Placeholder(10))
	_, err := r.db.Exec(ctx, q, tokenHash, tokenHash, userID, expires, uuid.New().String(), meta.Kind, now, now, meta.IP, meta.UserAgent)
	return token, err
}

const sessionTokenHashPrefix = "sha256:"

func sessionTokenHash(token string) string {
	sum := sha256.Sum256([]byte(token))
	return sessionTokenHashPrefix + hex.EncodeToString(sum[:])
}

// migrateSessionTokens replaces legacy plaintext bearer tokens in-place with
// one-way digests. The new token_hash column is also the idempotence marker:
// if startup is interrupted after ADD COLUMN, the next startup resumes the
// backfill. Existing browser cookies continue to work because lookups hash the
// cookie before querying.
func (r *Repo) migrateSessionTokens(ctx context.Context) error {
	return r.db.WithTxScope(ctx, func(txCtx context.Context) error {
		rows, err := r.db.Query(txCtx, `SELECT token FROM _sessions WHERE token_hash IS NULL OR token_hash = ''`)
		if err != nil {
			return err
		}
		var legacyTokens []string
		for rows.Next() {
			var token string
			if err := rows.Scan(&token); err != nil {
				rows.Close()
				return err
			}
			legacyTokens = append(legacyTokens, token)
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return err
		}

		d := r.db.Dialect()
		q := fmt.Sprintf(`UPDATE _sessions SET token = %s, token_hash = %s
			WHERE token = %s AND (token_hash IS NULL OR token_hash = '')`,
			d.Placeholder(1), d.Placeholder(2), d.Placeholder(3))
		for _, token := range legacyTokens {
			digest := sessionTokenHash(token)
			if _, err := r.db.Exec(txCtx, q, digest, digest, token); err != nil {
				return err
			}
		}
		return nil
	})
}

// enforceSessionLimit применяет политику `auth.max_sessions_per_user`
// (план 78, п. 1.6): при превышении лимита вытесняет старейшие по активности
// enterprise-сессии пользователя, освобождая место новой. Именно вытеснение,
// а не отказ во входе: брошенная сессия (браузер закрыт без «Выйти») при TTL
// 24 ч заблокировала бы пользователя до вмешательства админа. Сессии
// конфигуратора не считаются и не вытесняются — иначе вернулся бы баг «вход
// в конфигуратор выбивает Предприятие». Ошибки не фатальны: политика не
// должна ломать вход.
func (r *Repo) enforceSessionLimit(ctx context.Context, userID string, meta SessionMeta) {
	limit := r.db.GetMaxSessionsPerUser(ctx)
	if limit <= 0 {
		return
	}
	d := r.db.Dialect()
	var count int
	q := fmt.Sprintf(`SELECT count(*) FROM _sessions WHERE user_id = %s AND kind = %s AND expires_at > %s`,
		d.Placeholder(1), d.Placeholder(2), d.Now())
	if err := r.db.QueryRow(ctx, q, userID, SessionKindEnterprise).Scan(&count); err != nil {
		return
	}
	excess := count - limit + 1 // +1: новая сессия должна поместиться в лимит
	if excess <= 0 {
		return
	}
	delQ := fmt.Sprintf(`DELETE FROM _sessions WHERE token IN (
		SELECT token FROM _sessions WHERE user_id = %s AND kind = %s
		ORDER BY COALESCE(last_seen_at, created_at, expires_at) ASC LIMIT %s)`,
		d.Placeholder(1), d.Placeholder(2), d.Placeholder(3))
	if _, err := r.db.Exec(ctx, delQ, userID, SessionKindEnterprise, excess); err != nil {
		return
	}
	// Аудит вытеснения — актор сам пользователь: это его новый вход. Логин нужен
	// лишь для читаемости записи: если прочитать не удалось, пишем аудит с
	// пустым логином, но по user_id событие всё равно найдётся.
	login := ""
	if err := r.db.QueryRow(ctx, fmt.Sprintf(`SELECT login FROM _users WHERE id = %s`,
		d.Placeholder(1)), userID).Scan(&login); err != nil {
		authLog().Debug("не удалось прочитать логин для аудита вытеснения сессии", "err", err)
	}
	r.db.LogAction(ctx, "session_displaced", "", login, userID, userID, login, meta.IP)
}

// DeleteExpiredSessions удаляет истёкшие сессии всех пользователей.
// Вызывается при каждом логине (из CreateSession).
func (r *Repo) DeleteExpiredSessions(ctx context.Context) error {
	q := fmt.Sprintf(`DELETE FROM _sessions WHERE expires_at <= %s`, r.db.Dialect().Now())
	_, err := r.db.Exec(ctx, q)
	return err
}

// touchThrottle — глобальный (на процесс) трекер последних записей last_seen_at:
// не чаще touchInterval на токен. Package-level, а не поле Repo: лаунчер создаёт
// Repo на каждый запрос (cfgAuthMiddleware), троттлинг должен переживать Repo.
// Ключи — валидные токены сессий, поэтому размер ограничен числом сессий.
var touchThrottle sync.Map // map[string]time.Time

const touchInterval = 5 * time.Minute

// TouchSession обновляет last_seen_at сессии, но не чаще раза в touchInterval:
// SQLite single-writer, а лаунчер и процесс базы пишут в один файл — лишние
// записи ни к чему. now передаётся параметром ради детерминизма в тестах.
func (r *Repo) TouchSession(ctx context.Context, token string, now time.Time) error {
	tokenHash := sessionTokenHash(token)
	if last, ok := touchThrottle.Load(tokenHash); ok && now.Sub(last.(time.Time)) < touchInterval {
		return nil
	}
	touchThrottle.Store(tokenHash, now)
	// Попутная уборка записей умерших сессий (не чаще реальных touch'ей).
	touchThrottle.Range(func(k, v any) bool {
		if now.Sub(v.(time.Time)) > 24*time.Hour {
			touchThrottle.Delete(k)
		}
		return true
	})
	d := r.db.Dialect()
	q := fmt.Sprintf(`UPDATE _sessions SET last_seen_at = %s WHERE token_hash = %s`, d.Placeholder(1), d.Placeholder(2))
	_, err := r.db.Exec(ctx, q, now, tokenHash)
	return err
}

// LookupSession resolves any live session regardless of its surface kind.
// Prefer LookupSessionKind at every authorization boundary; this broad helper
// remains for administration, revocation and compatibility callers that need
// to inspect either kind deliberately.
func (r *Repo) LookupSession(ctx context.Context, token string) (*User, error) {
	return r.lookupSession(ctx, token, "", false)
}

// LookupSessionKind resolves a session only when it was issued for the
// requested surface. Configurator sessions are administrator capabilities and
// must not become ordinary Enterprise sessions merely because both surfaces
// use the same database-backed session table.
func (r *Repo) LookupSessionKind(ctx context.Context, token, kind string) (*User, error) {
	return r.lookupSession(ctx, token, kind, true)
}

func (r *Repo) lookupSession(ctx context.Context, token, kind string, requireKind bool) (*User, error) {
	d := r.db.Dialect()
	u := &User{}
	var aiData any
	q := fmt.Sprintf(`
		SELECT u.id, u.login, u.full_name, u.is_admin, u.deny_passwd_change, u.ai_data_access, u.lang
		FROM _sessions s JOIN _users u ON u.id = s.user_id
		WHERE s.token_hash = %s AND s.expires_at > %s
	`, d.Placeholder(1), d.Now())
	args := []any{sessionTokenHash(token)}
	if requireKind {
		q += " AND s.kind = " + d.Placeholder(2)
		args = append(args, kind)
	}
	err := r.db.QueryRow(ctx, q, args...).Scan(&u.ID, &u.Login, &u.FullName, &u.IsAdmin, &u.DenyPasswdChange, &aiData, &u.Lang)
	if err != nil {
		return nil, err
	}
	u.AIDataAccess = scanBool(aiData)
	return u, nil
}

func (r *Repo) DeleteSession(ctx context.Context, token string) error {
	d := r.db.Dialect()
	q := fmt.Sprintf(`DELETE FROM _sessions WHERE token_hash = %s`, d.Placeholder(1))
	_, err := r.db.Exec(ctx, q, sessionTokenHash(token))
	return err
}

// SessionInfo describes one active session. Токен наружу не отдаётся —
// сессию идентифицирует PublicID. Поля метаданных пустые у сессий, созданных
// до миграции плана 78.
type SessionInfo struct {
	PublicID   string
	Kind       string // SessionKindEnterprise | SessionKindConfigurator | ""
	Login      string
	FullName   string
	IsAdmin    bool
	CreatedAt  time.Time
	LastSeenAt time.Time
	ExpiresAt  time.Time
	IP         string
	UserAgent  string
}

// ActiveSessions returns all non-expired sessions with user info.
// Один логин может встречаться несколько раз — по строке на сессию (план 78).
func (r *Repo) ActiveSessions(ctx context.Context) ([]*SessionInfo, error) {
	d := r.db.Dialect()
	q := fmt.Sprintf(`
		SELECT s.public_id, s.kind, u.login, u.full_name, u.is_admin,
		       s.created_at, s.last_seen_at, s.expires_at, s.ip, s.user_agent
		FROM _sessions s
		JOIN _users u ON u.id = s.user_id
		WHERE s.expires_at > %s
		ORDER BY u.login, s.expires_at DESC
	`, d.Now())
	rows, err := r.db.Query(ctx, q)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var sessions []*SessionInfo
	for rows.Next() {
		si := &SessionInfo{}
		var publicID, kind, createdRaw, lastSeenRaw, expiresRaw, ip, ua any
		if err := rows.Scan(&publicID, &kind, &si.Login, &si.FullName, &si.IsAdmin, &createdRaw, &lastSeenRaw, &expiresRaw, &ip, &ua); err != nil {
			return nil, err
		}
		si.PublicID = scanString(publicID)
		si.Kind = scanString(kind)
		si.IP = scanString(ip)
		si.UserAgent = scanString(ua)
		si.CreatedAt = parseSessionTime(createdRaw)
		si.LastSeenAt = parseSessionTime(lastSeenRaw)
		si.ExpiresAt = parseSessionTime(expiresRaw)
		sessions = append(sessions, si)
	}
	return sessions, rows.Err()
}

// scanString нормализует nullable-текстовую колонку: NULL → "".
func scanString(v any) string {
	switch t := v.(type) {
	case string:
		return t
	case []byte:
		return string(t)
	}
	return ""
}

// ActiveSessionCount returns the number of non-expired sessions.
func (r *Repo) ActiveSessionCount(ctx context.Context) (int, error) {
	d := r.db.Dialect()
	q := fmt.Sprintf(`SELECT COUNT(*) FROM _sessions WHERE expires_at > %s`, d.Now())
	var count int
	if err := r.db.QueryRow(ctx, q).Scan(&count); err != nil {
		return 0, err
	}
	return count, nil
}

// parseSessionTime normalises an expires_at column value to time.Time.
// PostgreSQL returns time.Time natively; SQLite stores it as TEXT which
// the driver may return as string or []byte in Go's time format.
func parseSessionTime(v any) time.Time {
	switch t := v.(type) {
	case time.Time:
		return t
	case string:
		// Try standard formats first, then Go's own String() format.
		for _, layout := range []string{
			time.RFC3339Nano, time.RFC3339,
			"2006-01-02 15:04:05-07:00",
			"2006-01-02 15:04:05.999999999 -0700 MST",
			"2006-01-02 15:04:05 -0700 MST",
			"2006-01-02 15:04:05",
		} {
			if parsed, err := time.Parse(layout, t); err == nil {
				return parsed
			}
		}
	case []byte:
		return parseSessionTime(string(t))
	}
	return time.Time{}
}

// SetDenyPasswdChange sets the deny_passwd_change flag for a user.
func (r *Repo) SetDenyPasswdChange(ctx context.Context, userID string, deny bool) error {
	d := r.db.Dialect()
	q := fmt.Sprintf(`UPDATE _users SET deny_passwd_change = %s WHERE id = %s`, d.Placeholder(1), d.Placeholder(2))
	_, err := r.db.Exec(ctx, q, deny, userID)
	return err
}

// UpdatePassword sets a new bcrypt-hashed password for the given user ID.
func (r *Repo) UpdatePassword(ctx context.Context, userID, newPassword string) error {
	if err := r.EffectivePasswordPolicy(ctx).validate(newPassword); err != nil {
		return err
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(newPassword), bcrypt.DefaultCost)
	if err != nil {
		return err
	}
	d := r.db.Dialect()
	q := fmt.Sprintf(`UPDATE _users SET password_hash = %s WHERE id = %s`, d.Placeholder(1), d.Placeholder(2))
	_, err = r.db.Exec(ctx, q, hash, userID)
	return err
}

// KickUser deletes all sessions for the given login (forces re-login).
func (r *Repo) KickUser(ctx context.Context, login string) error {
	d := r.db.Dialect()
	q := fmt.Sprintf(`DELETE FROM _sessions WHERE user_id = (SELECT id FROM _users WHERE login = %s)`,
		d.Placeholder(1))
	_, err := r.db.Exec(ctx, q, login)
	return err
}

// KickUserSessions удаляет все сессии пользователя по его ID — вариант KickUser
// для мест, где известен ID, а не логин (ревокация при смене пароля админом).
func (r *Repo) KickUserSessions(ctx context.Context, userID string) error {
	d := r.db.Dialect()
	q := fmt.Sprintf(`DELETE FROM _sessions WHERE user_id = %s`, d.Placeholder(1))
	_, err := r.db.Exec(ctx, q, userID)
	return err
}

// KickSession завершает одну сессию по её публичному идентификатору (план 78).
func (r *Repo) KickSession(ctx context.Context, publicID string) error {
	if publicID == "" {
		return fmt.Errorf("auth: kick session: пустой public_id")
	}
	d := r.db.Dialect()
	q := fmt.Sprintf(`DELETE FROM _sessions WHERE public_id = %s`, d.Placeholder(1))
	_, err := r.db.Exec(ctx, q, publicID)
	return err
}

// KickOtherSessions завершает все сессии пользователя, кроме текущей —
// «выйти со всех устройств кроме этого» (план 78).
func (r *Repo) KickOtherSessions(ctx context.Context, userID, currentToken string) error {
	d := r.db.Dialect()
	q := fmt.Sprintf(`DELETE FROM _sessions WHERE user_id = %s AND token_hash <> %s`,
		d.Placeholder(1), d.Placeholder(2))
	_, err := r.db.Exec(ctx, q, userID, sessionTokenHash(currentToken))
	return err
}

// EffectivePasswordPolicy — политика паролей, действующая для этой базы:
// умолчания процесса (переменные окружения, захваченные при создании Repo),
// уточнённые политикой, сохранённой администратором в _settings.
//
// Читается на каждой проверке, а не кэшируется: смена политики в интерфейсе
// обязана действовать сразу, иначе администратор снимает ограничение и не
// понимает, почему пароль всё ещё отвергается. Обращений мало — установка
// пароля и отрисовка подсказки в форме.
func (r *Repo) EffectivePasswordPolicy(ctx context.Context) PasswordPolicy {
	if r == nil {
		return PasswordPolicy{MinLength: DefaultMinPasswordLength}
	}
	return r.passwordPolicy.applyStored(r.AuthPolicy(ctx))
}
