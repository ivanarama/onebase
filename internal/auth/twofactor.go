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
	// ErrInvalidBindTicket — код привязки не подошёл: неверный, просроченный или
	// уже использованный. Формулировка одна на все случаи (как у кода 2FA).
	ErrInvalidBindTicket = errors.New("auth: неверный или просроченный код привязки")
)

// BindTicketTTL — сколько живёт выданный администратором одноразовый код
// привязки второго фактора (issue #577). Сутки: администратор передаёт код
// пользователю вне системы (звонок, мессенджер), и тому нужно время им
// воспользоваться, но вечно висеть коду незачем.
const BindTicketTTL = 24 * time.Hour

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
	for _, c := range []struct{ col, typ string }{
		{"totp_secret", "TEXT NOT NULL DEFAULT ''"},
		{"totp_enabled", d.TypeBool() + " NOT NULL DEFAULT " + boolFalseFor(d)},
		{"totp_last_step", "BIGINT NOT NULL DEFAULT 0"},
		// Привязка к внешнему провайдеру (SSO): по паре (провайдер, subject)
		// учётка находится даже после смены адреса почты в каталоге.
		{"auth_provider", "TEXT NOT NULL DEFAULT ''"},
		{"auth_subject", "TEXT NOT NULL DEFAULT ''"},
	} {
		if err := r.db.AddColumnIfMissing(ctx, "_users", c.col, c.typ); err != nil {
			return fmt.Errorf("auth: migrate _users.%s (2fa): %w", c.col, err)
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
	// Одноразовые коды привязки второго фактора (issue #577). По одному активному
	// на учётку: переиздание заменяет прежний. Срок — Unix-секунды в BIGINT, а не
	// timestamp: сравнение «не истёк» одинаково работает на обоих диалектах, без
	// разбора часовых поясов. В базе только хэш кода — как у резервных кодов.
	ticketsDDL := fmt.Sprintf(`CREATE TABLE IF NOT EXISTS _auth_bind_tickets (
		user_id %s NOT NULL REFERENCES _users(id) ON DELETE CASCADE,
		code_hash TEXT NOT NULL,
		expires_at BIGINT NOT NULL,
		PRIMARY KEY (user_id)
	)`, d.TypeUUID())
	if _, err := r.db.Exec(ctx, ticketsDDL); err != nil {
		return fmt.Errorf("auth: create _auth_bind_tickets: %w", err)
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

// IssueBindTicket выдаёт одноразовый код привязки второго фактора для учётки
// (issue #577): администратор передаёт его пользователю, тот вводит на входе,
// когда политика требует 2FA, а самопривязка по паролю выключена. Возвращает
// код открытым текстом — единственный раз, когда он виден; в базе лежит только
// хэш. Прежний код учётки при этом гасится: активный код один.
func (r *Repo) IssueBindTicket(ctx context.Context, userID string) (string, error) {
	code, err := generateBackupCode()
	if err != nil {
		return "", err
	}
	expires := time.Now().Add(BindTicketTTL).Unix()
	err = r.db.WithTxScope(ctx, func(txCtx context.Context) error {
		d := r.db.Dialect()
		del := fmt.Sprintf(`DELETE FROM _auth_bind_tickets WHERE user_id = %s`, d.Placeholder(1))
		if _, err := r.db.Exec(txCtx, del, userID); err != nil {
			return err
		}
		ins := fmt.Sprintf(`INSERT INTO _auth_bind_tickets (user_id, code_hash, expires_at) VALUES (%s, %s, %s)`,
			d.Placeholder(1), d.Placeholder(2), d.Placeholder(3))
		_, err := r.db.Exec(txCtx, ins, userID, hashBackupCode(code), expires)
		return err
	})
	if err != nil {
		return "", fmt.Errorf("auth: код привязки: %w", err)
	}
	return code, nil
}

// ConsumeBindTicket проверяет и гасит код привязки учётки. Одноразовость и срок
// обеспечивает сам DELETE: строка удаляется ровно один раз и только пока не
// истекла, параллельная попытка с тем же кодом получит 0 изменённых строк.
func (r *Repo) ConsumeBindTicket(ctx context.Context, userID, code string, now time.Time) error {
	if strings.TrimSpace(code) == "" {
		return ErrInvalidBindTicket
	}
	d := r.db.Dialect()
	q := fmt.Sprintf(`DELETE FROM _auth_bind_tickets WHERE user_id = %s AND code_hash = %s AND expires_at > %s`,
		d.Placeholder(1), d.Placeholder(2), d.Placeholder(3))
	tag, err := r.db.Exec(ctx, q, userID, hashBackupCode(code), now.Unix())
	if err != nil {
		return err
	}
	if tag.RowsAffected == 0 {
		return ErrInvalidBindTicket
	}
	return nil
}

// VerifyBindTicket проверяет код привязки, НЕ гася его.
//
// Билет гасится только когда второй фактор действительно привязан
// (completeEnrollment). Раньше он удалялся в начале второго шага, и любой сбой
// на сканировании QR — истёкший challenge, пять опечаток в коде TOTP, закрытая
// вкладка — оставлял пользователя разом без второго фактора и без кода
// привязки: за новым надо было идти к администратору (#615).
//
// Перебор от этого не выигрывает: неподошедший код и раньше ничего не гасил
// (DELETE отбирал строку по хэшу), а число попыток ограничивает счётчик
// challenge'а и лимитер входа.
func (r *Repo) VerifyBindTicket(ctx context.Context, userID, code string, now time.Time) error {
	if strings.TrimSpace(code) == "" {
		return ErrInvalidBindTicket
	}
	d := r.db.Dialect()
	q := fmt.Sprintf(`SELECT COUNT(*) FROM _auth_bind_tickets WHERE user_id = %s AND code_hash = %s AND expires_at > %s`,
		d.Placeholder(1), d.Placeholder(2), d.Placeholder(3))
	var n int
	if err := r.db.QueryRow(ctx, q, userID, hashBackupCode(code), now.Unix()).Scan(&n); err != nil {
		return err
	}
	if n == 0 {
		return ErrInvalidBindTicket
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

// backupCodeLength — длина резервного кода в символах алфавита.
//
// Стойкость кода считается НЕ по лимитеру входа, а по оффлайн-перебору: кто
// прочитал _auth_backup_codes (копия файла SQLite, файловый бэкап, дамп PG),
// перебирает хэши у себя. Прежних 8 символов из алфавита в 31 знак — 31^8,
// около 40 бит: замер на одном ядре Go дал 1.8 млн хэшей в секунду, то есть
// всё пространство ≈ 133 часа одноядерно и десятки секунд на GPU, причём все
// 10 кодов учётки атакуются одним проходом. Это обесценивало то, ради чего
// секрет TOTP шифруется мастер-ключом.
//
// 16 символов дают 31^16 ≈ 79 бит — тот же порядок, что у API-токенов, где
// такой же несолёный sha256: закономерен, потому что случайности достаточно.
// Медленный хэш (bcrypt/argon2) взамен выбран не был намеренно: код гасится
// одним DELETE по совпадению хэша, поэтому проверка стоила бы до десяти
// медленных хэшей на попытку — это канал DoS, а одноразовость пришлось бы
// переносить из атомарного DELETE в чтение с последующей записью.
const backupCodeLength = 16

// generateBackupCode выдаёт код вида «abcd-efgh-ijkl-mnop».
func generateBackupCode() (string, error) {
	buf := make([]byte, 0, backupCodeLength+backupCodeLength/4)
	max := big.NewInt(int64(len(backupCodeAlphabet)))
	for i := 0; i < backupCodeLength; i++ {
		if i > 0 && i%4 == 0 {
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
