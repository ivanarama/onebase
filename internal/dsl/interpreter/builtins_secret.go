package interpreter

import (
	"errors"
	"fmt"

	"github.com/ivantit66/onebase/internal/secrets"
)

// РасшифроватьСекрет — enc:-значение из данных базы в открытый текст в момент
// использования. Нужен там, где секрет хранит сама конфигурация: пароль
// подключения к внешней системе в реквизите справочника, ключ API в константе.
// Без него такой секрет лежал открытым текстом, и его видел каждый, кому дано
// чтение справочника, — в форме, списке и выгрузке.
//
// Разворачиваются только enc:-ссылки (secrets.ResolveEnc): значение вписывает
// пользователь с правом записи, и env:/file: дали бы ему прочитать окружение
// и файлы сервера. Мастер-ключ — у процесса (ONEBASE_MASTER_KEY), в базе его
// нет. Песочница недоверенного кода функцию закрывает (DenySecrets).
func init() {
	builtins["расшифроватьсекрет"] = catchableEnvBuiltin(decryptSecretFn)
	builtins["decryptsecret"] = catchableEnvBuiltin(decryptSecretFn)
}

func decryptSecretFn(args []any, _ string, _ int) (any, error) {
	if len(args) != 1 {
		return nil, fmt.Errorf("РасшифроватьСекрет: ожидается 1 аргумент, передано %d", len(args))
	}
	if args[0] == nil {
		return "", nil
	}
	s, ok := args[0].(string)
	if !ok {
		return nil, fmt.Errorf("РасшифроватьСекрет: ожидается строка, передано %s", getTypeName(args[0]))
	}
	out, err := secrets.Default().ResolveEnc(s)
	if err != nil {
		return nil, decryptSecretError(err)
	}
	return out, nil
}

// decryptSecretError переводит ошибку резолвера в сообщение для прикладника.
// Текст ошибки не содержит ни значения, ни шифротекста — его можно показывать.
func decryptSecretError(err error) error {
	switch {
	case errors.Is(err, secrets.ErrRefNotAllowed):
		return fmt.Errorf("РасшифроватьСекрет: в данных допустимы только enc:-ссылки (onebase secret encrypt); env:/file: не разыменовываются")
	case errors.Is(err, secrets.ErrNoMasterKey):
		return fmt.Errorf("РасшифроватьСекрет: серверу не задан мастер-ключ (%s или %s)", secrets.EnvMasterKey, secrets.EnvMasterKeyFile)
	case errors.Is(err, secrets.ErrWrongKey):
		return fmt.Errorf("РасшифроватьСекрет: значение зашифровано другим мастер-ключом")
	}
	return fmt.Errorf("РасшифроватьСекрет: %w", err)
}
