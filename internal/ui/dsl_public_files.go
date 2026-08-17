package ui

// DSL-функции публикации вложений (план 127). Продолжают ряд функций вложений
// из плана 105 (ПрисоединитьФайл / СписокВложений / ПутьКВложению):
//
//	Ссылка = ОпубликоватьФайл(ИдВложения[, Опции]);   // "/pub/<токен>"
//	Ссылка = СсылкаНаФайл(ИдВложения);                 // Строка или Неопределено
//	СнятьПубликациюФайла(ИдВложения);
//
// Опции — Структура: КэшСекунд (число), ДействуетДо (дата), Имя (строка).

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"

	"github.com/ivantit66/onebase/internal/dsl/interpreter"
	"github.com/ivantit66/onebase/internal/storage"
)

// publicFileURL — путь публикации. Относительный: домен знает конфигурация,
// платформа его не выдумывает.
func publicFileURL(token string) string { return "/pub/" + token }

func parseAttachmentID(fnName string, arg any) (uuid.UUID, error) {
	id, err := uuid.Parse(strings.TrimSpace(fmt.Sprint(arg)))
	if err != nil {
		return uuid.Nil, fmt.Errorf("%s: неверный идентификатор вложения", fnName)
	}
	return id, nil
}

// publishOptionsFromDSL читает Структуру опций. Неизвестные ключи игнорируются
// молча — как в остальных структурах-параметрах DSL.
func publishOptionsFromDSL(arg any) storage.PublishOptions {
	var opts storage.PublishOptions
	st, ok := arg.(*interpreter.Struct)
	if !ok || st == nil {
		return opts
	}
	if v := st.Get("Имя"); v != nil {
		opts.Filename = strings.TrimSpace(fmt.Sprint(v))
	}
	if v := st.Get("КэшСекунд"); v != nil {
		opts.CacheSeconds = intFromDSLNumber(v)
	}
	if v := st.Get("ДействуетДо"); v != nil {
		if t, ok := v.(time.Time); ok {
			opts.ExpiresAt = &t
		}
	}
	return opts
}

// intFromDSLNumber приводит числовое значение DSL к int. Числа в языке —
// decimal (план 42), поэтому одного приведения к float64 недостаточно.
func intFromDSLNumber(v any) int {
	switch n := v.(type) {
	case decimal.Decimal:
		return int(n.IntPart())
	case float64:
		return int(n)
	case int:
		return n
	case int64:
		return int(n)
	}
	return 0
}

// registerPublicFileBuiltins добавляет функции публикации в DSL-окружение.
// Регистрируется там же, где остальные функции вложений (handlers_dsl.go).
func (s *Server) registerPublicFileBuiltins(vars map[string]any, ctxFn func() context.Context) {
	publishFn := interpreter.BuiltinFunc(func(args []any, _ string, _ int) (any, error) {
		if len(args) < 1 {
			return nil, fmt.Errorf("ОпубликоватьФайл(ИдВложения[, Опции]): не передан идентификатор")
		}
		id, err := parseAttachmentID("ОпубликоватьФайл", args[0])
		if err != nil {
			return nil, err
		}
		ctx := ctxFn()
		// Право на вложение проверяем по его владельцу — тем же путём, что и
		// скачивание в админке: публикация не должна быть обходным каналом к
		// файлу, который вызывающему читать нельзя.
		if err := s.checkAttachmentAccess(ctx, id, "write"); err != nil {
			return nil, fmt.Errorf("ОпубликоватьФайл: %w", err)
		}
		var opts storage.PublishOptions
		if len(args) > 1 {
			opts = publishOptionsFromDSL(args[1])
		}
		token, err := s.store.PublishAttachment(ctx, id, opts)
		if err != nil {
			return nil, fmt.Errorf("ОпубликоватьФайл: %w", err)
		}
		return publicFileURL(token), nil
	})

	urlFn := interpreter.BuiltinFunc(func(args []any, _ string, _ int) (any, error) {
		if len(args) < 1 {
			return nil, fmt.Errorf("СсылкаНаФайл(ИдВложения): не передан идентификатор")
		}
		id, err := parseAttachmentID("СсылкаНаФайл", args[0])
		if err != nil {
			return nil, err
		}
		ctx := ctxFn()
		if err := s.checkAttachmentAccess(ctx, id, "read"); err != nil {
			return nil, fmt.Errorf("СсылкаНаФайл: %w", err)
		}
		pf, err := s.store.PublicFileByAttachment(ctx, id)
		if err != nil {
			return nil, fmt.Errorf("СсылкаНаФайл: %w", err)
		}
		if pf == nil || pf.Expired(time.Now()) {
			return nil, nil // Неопределено
		}
		return publicFileURL(pf.Token), nil
	})

	unpublishFn := interpreter.BuiltinFunc(func(args []any, _ string, _ int) (any, error) {
		if len(args) < 1 {
			return nil, fmt.Errorf("СнятьПубликациюФайла(ИдВложения): не передан идентификатор")
		}
		id, err := parseAttachmentID("СнятьПубликациюФайла", args[0])
		if err != nil {
			return nil, err
		}
		ctx := ctxFn()
		if err := s.checkAttachmentAccess(ctx, id, "write"); err != nil {
			return nil, fmt.Errorf("СнятьПубликациюФайла: %w", err)
		}
		if err := s.store.UnpublishAttachment(ctx, id); err != nil {
			return nil, fmt.Errorf("СнятьПубликациюФайла: %w", err)
		}
		return nil, nil
	})

	// Картинки (поле image) живут в блобах, а не во вложениях, поэтому у них
	// свои функции. Пользовательский смысл один — «дать ссылку на файл», но
	// идентификаторы разного происхождения путать нельзя: перепутанный ид
	// молча вернул бы «не найдено».
	publishImageFn := interpreter.BuiltinFunc(func(args []any, _ string, _ int) (any, error) {
		if len(args) < 1 {
			return nil, fmt.Errorf("ОпубликоватьКартинку(ИдКартинки[, Опции]): не передан идентификатор")
		}
		id, err := parseAttachmentID("ОпубликоватьКартинку", args[0])
		if err != nil {
			return nil, err
		}
		ctx := ctxFn()
		if err := s.checkBlobAccess(ctx, id); err != nil {
			return nil, fmt.Errorf("ОпубликоватьКартинку: %w", err)
		}
		var opts storage.PublishOptions
		if len(args) > 1 {
			opts = publishOptionsFromDSL(args[1])
		}
		token, err := s.store.PublishBlob(ctx, id, opts)
		if err != nil {
			return nil, fmt.Errorf("ОпубликоватьКартинку: %w", err)
		}
		return publicFileURL(token), nil
	})

	imageURLFn := interpreter.BuiltinFunc(func(args []any, _ string, _ int) (any, error) {
		if len(args) < 1 {
			return nil, fmt.Errorf("СсылкаНаКартинку(ИдКартинки): не передан идентификатор")
		}
		id, err := parseAttachmentID("СсылкаНаКартинку", args[0])
		if err != nil {
			return nil, err
		}
		ctx := ctxFn()
		if err := s.checkBlobAccess(ctx, id); err != nil {
			return nil, fmt.Errorf("СсылкаНаКартинку: %w", err)
		}
		pf, err := s.store.PublicFileByBlob(ctx, id)
		if err != nil {
			return nil, fmt.Errorf("СсылкаНаКартинку: %w", err)
		}
		if pf == nil || pf.Expired(time.Now()) {
			return nil, nil
		}
		return publicFileURL(pf.Token), nil
	})

	unpublishImageFn := interpreter.BuiltinFunc(func(args []any, _ string, _ int) (any, error) {
		if len(args) < 1 {
			return nil, fmt.Errorf("СнятьПубликациюКартинки(ИдКартинки): не передан идентификатор")
		}
		id, err := parseAttachmentID("СнятьПубликациюКартинки", args[0])
		if err != nil {
			return nil, err
		}
		ctx := ctxFn()
		if err := s.checkBlobAccess(ctx, id); err != nil {
			return nil, fmt.Errorf("СнятьПубликациюКартинки: %w", err)
		}
		if err := s.store.UnpublishBlob(ctx, id); err != nil {
			return nil, fmt.Errorf("СнятьПубликациюКартинки: %w", err)
		}
		return nil, nil
	})

	vars["ОпубликоватьКартинку"] = publishImageFn
	vars["PublishImage"] = publishImageFn
	vars["СсылкаНаКартинку"] = imageURLFn
	vars["PublicImageURL"] = imageURLFn
	vars["СнятьПубликациюКартинки"] = unpublishImageFn
	vars["UnpublishImage"] = unpublishImageFn

	vars["ОпубликоватьФайл"] = publishFn
	vars["PublishFile"] = publishFn
	vars["СсылкаНаФайл"] = urlFn
	vars["PublicFileURL"] = urlFn
	vars["СнятьПубликациюФайла"] = unpublishFn
	vars["UnpublishFile"] = unpublishFn
}

// checkBlobAccess проверяет доступ к картинке через сущность-владельца блоба.
//
// У блоба, в отличие от вложения, нет идентификатора СТРОКИ-владельца — только
// вид и имя сущности (storage.Blob). Поэтому проверка идёт на уровне сущности:
// строковые политики здесь неприменимы, и это стоит помнить, публикуя картинки
// из справочника с построчным доступом.
func (s *Server) checkBlobAccess(ctx context.Context, blobID uuid.UUID) error {
	blob, rc, err := s.store.OpenBlob(ctx, blobID)
	if err != nil {
		return fmt.Errorf("картинка не найдена")
	}
	closeRead("проверка доступа к картинке", rc)
	if blob.OwnerEntity == "" {
		// Легаси-блоб без владельца: публиковать его вправе только код
		// конфигурации, который и так исполняется с полномочиями контекста.
		return nil
	}
	entity := s.reg.GetEntity(blob.OwnerEntity)
	if entity == nil {
		return fmt.Errorf("неизвестный владелец картинки %s", blob.OwnerEntity)
	}
	return s.checkDSLRowAccess(ctx, entity, "write", uuid.Nil, nil)
}

// checkAttachmentAccess проверяет доступ к вложению через его владельца
// (fail-closed: неизвестный владелец = отказ, как в resolveStoredAttachmentOwner).
func (s *Server) checkAttachmentAccess(ctx context.Context, attID uuid.UUID, mode string) error {
	att, err := s.store.GetAttachment(ctx, attID)
	if err != nil {
		return fmt.Errorf("вложение не найдено")
	}
	entity, err := s.resolveStoredAttachmentOwner(att)
	if err != nil {
		return err
	}
	return s.checkDSLRowAccess(ctx, entity, mode, att.OwnerID, nil)
}
