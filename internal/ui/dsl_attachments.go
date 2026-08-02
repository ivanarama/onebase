package ui

// DSL-builtins работы с вложениями (план 105): присоединение файла с диска к
// записи справочника/документа, список вложений, путь к файлу вложения,
// удаление. Нужны прикладным сценариям вида «сканер папки релизов регистрирует
// файл вложением», «отправить вложение письмом/в мессенджер через файл».
//
//	ИдВложения = ПрисоединитьФайл(Ссылка, "/path/file.zip");        // или + имя
//	Вложения   = СписокВложений(Ссылка);   // Массив Структур {ИД, ИмяФайла, Размер, ТипMIME, Загружен}
//	Путь       = ПутьКВложению(ИдВложения);
//	УдалитьВложение(ИдВложения);

import (
	"context"
	"fmt"
	"mime"
	"os"
	"path/filepath"
	"strings"

	"github.com/google/uuid"

	"github.com/ivantit66/onebase/internal/auth"
	"github.com/ivantit66/onebase/internal/dsl/interpreter"
	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/ivantit66/onebase/internal/storage"
)

// resolveAttachmentOwner превращает первый аргумент builtin'а (ссылку) в
// сущность-владельца и UUID записи.
func (s *Server) resolveAttachmentOwner(arg any) (*metadata.Entity, uuid.UUID, error) {
	ref, ok := arg.(*interpreter.Ref)
	if !ok || ref == nil {
		return nil, uuid.Nil, fmt.Errorf("ожидается ссылка на элемент справочника или документ, получено %T", arg)
	}
	entity := s.reg.GetEntity(ref.Type)
	if entity == nil {
		return nil, uuid.Nil, fmt.Errorf("неизвестный тип ссылки %q", ref.Type)
	}
	id, err := uuid.Parse(ref.UUID)
	if err != nil {
		return nil, uuid.Nil, fmt.Errorf("неверный идентификатор ссылки: %q", ref.UUID)
	}
	return entity, id, nil
}

// resolveStoredAttachmentOwner is fail-closed: an attachment whose owner no
// longer exists in the registry (or whose stored kind does not match) must not
// bypass RLS merely because metadata lookup failed.
func (s *Server) resolveStoredAttachmentOwner(att *storage.Attachment) (*metadata.Entity, error) {
	if att == nil {
		return nil, fmt.Errorf("вложение не найдено")
	}
	entity := s.reg.GetEntity(att.OwnerName)
	if entity == nil || !strings.EqualFold(string(entity.Kind), att.OwnerKind) {
		return nil, fmt.Errorf("неизвестный владелец вложения %s.%s", att.OwnerKind, att.OwnerName)
	}
	return entity, nil
}

// emailAttachmentPathResolver keeps the normal file sandbox, but also accepts
// the exact storage path of an attachment after checking its owner through
// RLS. This makes ПутьКВложению → ПисьмоEmail.ПрисоединитьФайл work in demo
// mode without opening the whole attachment directory to arbitrary DSL reads.
func (s *Server) emailAttachmentPathResolver(ctxFn func() context.Context) interpreter.EmailFileResolver {
	return func(path string) (string, error) {
		if safe, err := interpreter.ResolveSafePath(path); err == nil {
			return safe, nil
		} else {
			sandboxErr := err
			id, parseErr := uuid.Parse(filepath.Base(filepath.Clean(path)))
			if parseErr != nil {
				return "", sandboxErr
			}
			ctx := ctxFn()
			att, getErr := s.store.GetAttachment(ctx, id)
			if getErr != nil {
				return "", sandboxErr
			}
			entity, ownerErr := s.resolveStoredAttachmentOwner(att)
			if ownerErr != nil {
				return "", ownerErr
			}
			if accessErr := s.checkDSLRowAccess(ctx, entity, "read", att.OwnerID, nil); accessErr != nil {
				return "", accessErr
			}
			storedPath, cleanup, matAtt, matErr := s.store.MaterializeAttachment(ctx, id)
			if matErr != nil {
				return "", matErr
			}
			if cleanup != nil {
				context.AfterFunc(ctx, cleanup)
			}
			// S3-вложение: локального «настоящего» пути нет, каждая материализация
			// даёт новый temp-путь. Доступ уже авторизован RLS-проверкой выше —
			// отдаём свежую материализацию (её basename = ИД вложения).
			if matAtt.Loc == storage.FileStorageS3 {
				return storedPath, nil
			}
			// disk: путь должен совпадать с запрошенным (та же защита, что была).
			want, absErr := filepath.Abs(filepath.Clean(path))
			if absErr != nil {
				return "", sandboxErr
			}
			got, absErr := filepath.Abs(filepath.Clean(storedPath))
			if absErr != nil || want != got {
				return "", sandboxErr
			}
			return storedPath, nil
		}
	}
}

func (s *Server) attachmentMaxBytes() int64 {
	if s.maxFileSizeBytes > 0 {
		return s.maxFileSizeBytes
	}
	return 50 * 1024 * 1024
}

// registerAttachmentBuiltins добавляет функции вложений в DSL-окружение.
// ctxFn отдаёт живой контекст (по образцу txState в buildDSLVars).
func (s *Server) registerAttachmentBuiltins(vars map[string]any, ctxFn func() context.Context) {
	attachFn := interpreter.BuiltinFunc(func(args []any, _ string, _ int) (any, error) {
		if len(args) < 2 {
			return nil, fmt.Errorf("ПрисоединитьФайл(Ссылка, ПутьКФайлу[, ИмяФайла]): недостаточно аргументов")
		}
		entity, ownerID, err := s.resolveAttachmentOwner(args[0])
		if err != nil {
			return nil, fmt.Errorf("ПрисоединитьФайл: %w", err)
		}
		ctx := ctxFn()
		if err := s.checkDSLRowAccess(ctx, entity, "write", ownerID, nil); err != nil {
			return nil, fmt.Errorf("ПрисоединитьФайл: %w", err)
		}
		path, err := interpreter.ResolveSafePath(strings.TrimSpace(fmt.Sprint(args[1])))
		if err != nil {
			return nil, fmt.Errorf("ПрисоединитьФайл: %w", err)
		}
		name := filepath.Base(path)
		if len(args) > 2 {
			if n := strings.TrimSpace(fmt.Sprint(args[2])); n != "" {
				name = n
			}
		}
		name = storage.SanitizeAttachmentName(name)
		if !storage.AttachmentExtAllowed(s.allowedAttachmentTypes, name) {
			return nil, fmt.Errorf("ПрисоединитьФайл: недопустимый тип файла %q", name)
		}
		f, err := os.Open(path)
		if err != nil {
			return nil, fmt.Errorf("ПрисоединитьФайл: открытие файла: %w", err)
		}
		defer closeRead("вложение", f)
		mimeType := mime.TypeByExtension(strings.ToLower(filepath.Ext(name)))
		if mimeType == "" {
			mimeType = "application/octet-stream"
		}
		uploadedBy := ""
		if u := auth.UserFromContext(ctx); u != nil {
			uploadedBy = u.Login
		}
		att, err := s.store.UploadAttachment(ctx, string(entity.Kind), entity.Name, ownerID,
			name, mimeType, uploadedBy, f, s.attachmentMaxBytes())
		if err != nil {
			return nil, fmt.Errorf("ПрисоединитьФайл: %w", err)
		}
		return att.ID.String(), nil
	})

	listFn := interpreter.BuiltinFunc(func(args []any, _ string, _ int) (any, error) {
		if len(args) < 1 {
			return nil, fmt.Errorf("СписокВложений(Ссылка): не передана ссылка")
		}
		entity, ownerID, err := s.resolveAttachmentOwner(args[0])
		if err != nil {
			return nil, fmt.Errorf("СписокВложений: %w", err)
		}
		ctx := ctxFn()
		if err := s.checkDSLRowAccess(ctx, entity, "read", ownerID, nil); err != nil {
			return nil, fmt.Errorf("СписокВложений: %w", err)
		}
		atts, err := s.store.ListAttachments(ctx, string(entity.Kind), entity.Name, ownerID)
		if err != nil {
			return nil, fmt.Errorf("СписокВложений: %w", err)
		}
		items := make([]any, 0, len(atts))
		for _, a := range atts {
			items = append(items, interpreter.NewStructFromMap(map[string]any{
				"ИД":       a.ID.String(),
				"ИмяФайла": a.Filename,
				"Размер":   float64(a.SizeBytes),
				"ТипMIME":  a.MimeType,
				"Загружен": a.UploadedAt,
			}))
		}
		return interpreter.NewArray(items), nil
	})

	pathFn := interpreter.BuiltinFunc(func(args []any, _ string, _ int) (any, error) {
		if len(args) < 1 {
			return nil, fmt.Errorf("ПутьКВложению(ИдВложения): не передан идентификатор")
		}
		id, err := uuid.Parse(strings.TrimSpace(fmt.Sprint(args[0])))
		if err != nil {
			return nil, fmt.Errorf("ПутьКВложению: неверный идентификатор %q", fmt.Sprint(args[0]))
		}
		ctx := ctxFn()
		att, err := s.store.GetAttachment(ctx, id)
		if err != nil {
			return nil, fmt.Errorf("ПутьКВложению: %w", err)
		}
		entity, err := s.resolveStoredAttachmentOwner(att)
		if err != nil {
			return nil, fmt.Errorf("ПутьКВложению: %w", err)
		}
		if err := s.checkDSLRowAccess(ctx, entity, "read", att.OwnerID, nil); err != nil {
			return nil, fmt.Errorf("ПутьКВложению: %w", err)
		}
		p, cleanup, _, err := s.store.MaterializeAttachment(ctx, id)
		if err != nil {
			return nil, fmt.Errorf("ПутьКВложению: %w", err)
		}
		// Для S3 путь — временный файл; чистим его по завершении запроса/прогона.
		if cleanup != nil {
			context.AfterFunc(ctx, cleanup)
		}
		return p, nil
	})

	delFn := interpreter.BuiltinFunc(func(args []any, _ string, _ int) (any, error) {
		if len(args) < 1 {
			return nil, fmt.Errorf("УдалитьВложение(ИдВложения): не передан идентификатор")
		}
		id, err := uuid.Parse(strings.TrimSpace(fmt.Sprint(args[0])))
		if err != nil {
			return nil, fmt.Errorf("УдалитьВложение: неверный идентификатор %q", fmt.Sprint(args[0]))
		}
		ctx := ctxFn()
		att, err := s.store.GetAttachment(ctx, id)
		if err != nil {
			return nil, fmt.Errorf("УдалитьВложение: %w", err)
		}
		entity, err := s.resolveStoredAttachmentOwner(att)
		if err != nil {
			return nil, fmt.Errorf("УдалитьВложение: %w", err)
		}
		if err := s.checkDSLRowAccess(ctx, entity, "write", att.OwnerID, nil); err != nil {
			return nil, fmt.Errorf("УдалитьВложение: %w", err)
		}
		if err := s.store.DeleteAttachment(ctx, id); err != nil {
			return nil, fmt.Errorf("УдалитьВложение: %w", err)
		}
		return nil, nil
	})

	vars["ПрисоединитьФайл"] = attachFn
	vars["AttachFile"] = attachFn
	vars["СписокВложений"] = listFn
	vars["ListAttachments"] = listFn
	vars["ПутьКВложению"] = pathFn
	vars["AttachmentPath"] = pathFn
	vars["УдалитьВложение"] = delFn
	vars["DeleteAttachment"] = delFn
}
