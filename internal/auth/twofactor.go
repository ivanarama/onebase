package auth

// Хранение второго фактора (план 84): секрет TOTP и резервные коды учётной
// записи. Всё opt-in: пока пользователь не включил 2FA, ни одна колонка не
// заполнена и вход работает ровно как раньше.

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"math/big"
	"strings"
	"time"

	"github.com/ivantit66/onebase/internal/secrets"
)

var (
	// ErrInvalidSecondFactor — код не подошёл: неверный, просроченный или уже
	// предъявленный. Формулировка одна на все случаи намеренно: подсказка «код
	// правильный, но старый» сообщает подбирающему, что он на верном пути.
	ErrInvalidSecondFactor = errors.New("auth: неверный код подтверждения")
	// ErrTwoFactorNotEnabled — второй фактор у учётки не настроен.
	ErrTwoFactorNotEnabled = errors.New("auth: второй фактор не настроен")
)

// TwoFactorInfo — состояние второго фактора учётки для экранов профиля и
// администрирования.
type TwoFactorInfo struct {
	Enabled         bool
	BackupCodesLeft int
	// SecretPlaintext — секрет записан в базу открытым текстом (мастер-ключа
	// плана 83 не было в момент включения). Повод показать предупреждение.
	SecretPlaintext bool
}

// backupCodeCount — сколько резервных кодов выдаётся за раз.
const backupCodeCount = 10

// backupCodeAlphabet — без похожих символов (0/O, 1/I/L): коды переписывают
// с экрана на бумагу, и опечатка стоит доступа к учётке.
const backupCodeAlphabet = "abcdefghjkmnpqrstuvwxyz23456789"

// ensureTwoFactorSchema добавляет колонки второго фактора и таблицу резервных
// кодов. Вызывается из EnsureSchema; идемпотентна.
func (r *Repo) ensureTwoFactorSchema(ctx context.Context) error {
	d := r.db.Dialect()
	for _, ddl := range []string{
		`ALTER TABLE _users ADD COLUMN totp_secret TEXT NOT NULL DEFAULT ''`,
		fmt.Sprintf(`ALTER TABLE _users ADD COLUMN totp_enabled %s NOT NULL DEFAULT %s`, d.TypeBool(), boolFalseFor(d)),
		`ALTER TABLE _users ADD COLUMN totp_last_step BIGINT NOT NULL DEFAULT 0`,
		// Привязка к внешнему провайдеру (SSO): по паре (провайдер, subject)
		// учётка находится даже после смены адреса почты в каталоге.
		`ALTER TABLE _users ADD COLUMN auth_provider TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE _users ADD COLUMN auth_subject TEXT NOT NULL DEFAULT ''`,
	} {
		if _, err := r.db.Exec(ctx, ddl); err != nil && !isDuplicateColumnErr(err) {
			return fmt.Errorf("auth: migrate _users (2fa): %w", err)
		}
	}
	codesDDL := fmt.Sprintf(`CREATE TABLE IF NOT EXISTS _auth_backup_codes (
		user_id %s NOT NULL REFERENCES _users(id) ON DELETE CASCADE,
		code_hash TEXT NOT NULL,
		created_at %s,
		PRIMARY KEY (user_id, code_hash)
	)`, d.TypeUUID(), d.TypeTimestamp())
	if _, err := r.db.Exec(ctx, codesDDL); err != nil {
		return fmt.Errorf("auth: create _auth_backup_codes: %w", err)
	}
	return nil
}

// TwoFactorInfoFor возвращает состояние второго фактора пользователя.
func (r *Repo) TwoFactorInfoFor(ctx context.Context, userID string) (TwoFactorInfo, error) {
	d := r.db.Dialect()
	var enabled any
	var stored string
	q := fmt.Sprintf(`SELECT totp_enabled, totp_secret FROM _users WHERE id = %s`, d.Placeholder(1))
	if err := r.db.QueryRow(ctx, q, userID).Scan(&enabled, &stored); err != nil {
		return TwoFactorInfo{}, err
	}
	info := TwoFactorInfo{
		Enabled:         scanBool(enabled),
		SecretPlaintext: stored != "" && !secrets.IsRef(stored),
	}
	left, err := r.countBackupCodes(ctx, userID)
	if err != nil {
		return info, err
	}
	info.BackupCodesLeft = left
	return info, nil
}

func (r *Repo) countBackupCodes(ctx context.Context, userID string) (int, error) {
	d := r.db.Dialect()
	var n int
	q := fmt.Sprintf(`SELECT count(*) FROM _auth_backup_codes WHERE user_id = %s`, d.Placeholder(1))
	if err := r.db.QueryRow(ctx, q, userID).Scan(&n); err != nil {
		return 0, err
	}
	return n, nil
}

// EnableTOTP включает второй фактор: сохраняет секрет и сразу отмечает шаг
// подтверждающего кода использованным — иначе тот же код прошёл бы ещё раз уже
// как код входа.
//
// Секрет шифруется мастер-ключом (план 83), если он задан. Без ключа секрет
// ложится открытым текстом: отказать во включении 2FA было бы хуже — база без
// мастер-ключа осталась бы вовсе без второго фактора. Расхождение видно в
// профиле и в `onebase secret list`.
func (r *Repo) EnableTOTP(ctx context.Context, userID, secret string, usedStep int64) error {
	secret = NormalizeTOTPSecret(secret)
	if secret == "" {
		return errors.New("auth: пустой секрет TOTP")
	}
	stored := secret
	if key, err := secrets.Default().Key(); err == nil {
		enc, encErr := key.Encrypt(secret)
		if encErr != nil {
			return fmt.Errorf("auth: шифрование секрета TOTP: %w", encErr)
		}
		stored = enc
	} else {
		authLog().Warn("мастер-ключ не задан — секрет TOTP сохранён открытым текстом",
			"подсказка", "ONEBASE_MASTER_KEY (план 83)")
	}
	d := r.db.Dialect()
	q := fmt.Sprintf(`UPDATE _users SET totp_secret = %s, totp_enabled = %s, totp_last_step = %s WHERE id = %s`,
		d.Placeholder(1), d.Placeholder(2), d.Placeholder(3), d.Placeholder(4))
	_, err := r.db.Exec(ctx, q, stored, true, usedStep, userID)
	return err
}

// DisableTOTP выключает второй фактор и удаляет резервные коды.
func (r *Repo) DisableTOTP(ctx context.Context, userID string) error {
	d := r.db.Dialect()
	q := fmt.Sprintf(`UPDATE _users SET totp_secret = '', totp_enabled = %s, totp_last_step = 0 WHERE id = %s`,
		d.Placeholder(1), d.Placeholder(2))
	if _, err := r.db.Exec(ctx, q, false, userID); err != nil {
		return err
	}
	return r.deleteBackupCodes(ctx, userID)
}

// TOTPEnabled сообщает, включён ли второй фактор у учётки.
func (r *Repo) TOTPEnabled(ctx context.Context, userID string) (bool, error) {
	d := r.db.Dialect()
	var enabled any
	q := fmt.Sprintf(`SELECT totp_enabled FROM _users WHERE id = %s`, d.Placeholder(1))
	if err := r.db.QueryRow(ctx, q, userID).Scan(&enabled); err != nil {
		return false, err
	}
	return scanBool(enabled), nil
}

// VerifySecondFactor проверяет предъявленный код: сначала как код TOTP, затем
// как резервный. Оба варианта одноразовы — код TOTP гасится записью его шага,
// резервный удаляется из таблицы.
func (r *Repo) VerifySecondFactor(ctx context.Context, userID, code string, now time.Time) error {
	code = strings.TrimSpace(code)
	if code == "" {
		return ErrInvalidSecondFactor
	}
	d := r.db.Dialect()
	var enabled any
	var stored string
	var lastStep int64
	q := fmt.Sprintf(`SELECT totp_enabled, totp_secret, totp_last_step FROM _users WHERE id = %s`, d.Placeholder(1))
	if err := r.db.QueryRow(ctx, q, userID).Scan(&enabled, &stored, &lastStep); err != nil {
		return err
	}
	if !scanBool(enabled) {
		return ErrTwoFactorNotEnabled
	}
	if secret, err := secrets.Default().Resolve(stored); err == nil && secret != "" {
		if step, ok := VerifyTOTP(secret, code, now, lastStep+1); ok {
			return r.consumeTOTPStep(ctx, userID, step)
		}
	} else if err != nil {
		// Нерасшифровываемый секрет — не «неверный код»: с таким диагнозом
		// администратор будет искать проблему в телефоне пользователя, а дело в
		// потерянном мастер-ключе. Резервный код при этом ещё может сработать.
		authLog().Error("не удалось разыменовать секрет TOTP", "user_id", userID, "err", err)
	}
	return r.consumeBackupCode(ctx, userID, code)
}

// consumeTOTPStep помечает шаг использованным. Условие totp_last_step < step
// делает проверку атомарной: два параллельных запроса с одним кодом пройдут
// по одному, второй получит 0 изменённых строк и отказ.
func (r *Repo) consumeTOTPStep(ctx context.Context, userID string, step int64) error {
	d := r.db.Dialect()
	q := fmt.Sprintf(`UPDATE _users SET totp_last_step = %s WHERE id = %s AND totp_last_step < %s`,
		d.Placeholder(1), d.Placeholder(2), d.Placeholder(3))
	tag, err := r.db.Exec(ctx, q, step, userID, step)
	if err != nil {
		return err
	}
	if tag.RowsAffected == 0 {
		return ErrInvalidSecondFactor
	}
	return nil
}

// ReplaceBackupCodes выдаёт новый комплект резервных кодов, отменяя прежние.
// Возвращает коды открытым текстом — единственный раз, когда их видно: в базе
// лежат только хэши.
func (r *Repo) ReplaceBackupCodes(ctx context.Context, userID string) ([]string, error) {
	codes := make([]string, 0, backupCodeCount)
	for i := 0; i < backupCodeCount; i++ {
		code, err := generateBackupCode()
		if err != nil {
			return nil, err
		}
		codes = append(codes, code)
	}
	err := r.db.WithTxScope(ctx, func(txCtx context.Context) error {
		if err := r.deleteBackupCodes(txCtx, userID); err != nil {
			return err
		}
		d := r.db.Dialect()
		q := fmt.Sprintf(`INSERT INTO _auth_backup_codes (user_id, code_hash, created_at) VALUES (%s, %s, %s)`,
			d.Placeholder(1), d.Placeholder(2), d.Placeholder(3))
		now := time.Now()
		for _, code := range codes {
			if _, err := r.db.Exec(txCtx, q, userID, hashBackupCode(code), now); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("auth: резервные коды: %w", err)
	}
	return codes, nil
}

func (r *Repo) deleteBackupCodes(ctx context.Context, userID string) error {
	d := r.db.Dialect()
	q := fmt.Sprintf(`DELETE FROM _auth_backup_codes WHERE user_id = %s`, d.Placeholder(1))
	_, err := r.db.Exec(ctx, q, userID)
	return err
}

// consumeBackupCode гасит резервный код. Одноразовость обеспечивает сам DELETE:
// строка удаляется ровно один раз, и параллельная попытка с тем же кодом
// получит 0 изменённых строк.
func (r *Repo) consumeBackupCode(ctx context.Context, userID, code string) error {
	d := r.db.Dialect()
	q := fmt.Sprintf(`DELETE FROM _auth_backup_codes WHERE user_id = %s AND code_hash = %s`,
		d.Placeholder(1), d.Placeholder(2))
	tag, err := r.db.Exec(ctx, q, userID, hashBackupCode(code))
	if err != nil {
		return err
	}
	if tag.RowsAffected == 0 {
		return ErrInvalidSecondFactor
	}
	return nil
}

// normalizeBackupCode убирает разделители и регистр: код переписывают руками.
func normalizeBackupCode(code string) string {
	s := strings.ToLower(strings.TrimSpace(code))
	s = strings.ReplaceAll(s, "-", "")
	s = strings.ReplaceAll(s, " ", "")
	return s
}

func hashBackupCode(code string) string {
	sum := sha256.Sum256([]byte(normalizeBackupCode(code)))
	return "sha256:" + hex.EncodeToString(sum[:])
}

// generateBackupCode выдаёт код вида «abcd-efgh»: 8 символов из алфавита в 31
// знак — около 40 бит, столько же, сколько у одноразового кода приличного
// банка, и заведомо больше, чем переживёт лимитер попыток входа.
func generateBackupCode() (string, error) {
	buf := make([]byte, 0, 9)
	max := big.NewInt(int64(len(backupCodeAlphabet)))
	for i := 0; i < 8; i++ {
		if i == 4 {
			buf = append(buf, '-')
		}
		n, err := rand.Int(rand.Reader, max)
		if err != nil {
			return "", fmt.Errorf("auth: резервный код: %w", err)
		}
		buf = append(buf, backupCodeAlphabet[n.Int64()])
	}
	return string(buf), nil
}
