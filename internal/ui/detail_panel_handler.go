package ui

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/ivantit66/onebase/internal/runtime"
	"github.com/ivantit66/onebase/internal/storage"
)

type detailPanelSnapshot struct {
	row        map[string]any
	rowAllowed bool
	hookObject *runtime.Object
}

// loadDetailPanelSnapshot связывает декларативную RLS-проверку, объект
// ПриЧтенииНаСервере и будущий ответ с одним снимком записи. Повторный GetByID
// между этими этапами позволил бы конкурентному обновлению подменить данные,
// по которым read-hook принимает решение (TOCTOU).
func (s *Server) loadDetailPanelSnapshot(
	ctx context.Context,
	entity *metadata.Entity,
	form *metadata.FormModule,
	id uuid.UUID,
) (*detailPanelSnapshot, error) {
	snapshot := &detailPanelSnapshot{}
	err := s.store.WithReadSnapshot(ctx, func(snapshotCtx context.Context) error {
		row, err := s.store.GetByID(snapshotCtx, entity.Name, id, entity)
		if err != nil {
			return err
		}
		snapshot.row = row
		if row == nil {
			return nil
		}
		snapshot.rowAllowed = s.rowAllowsSelected(snapshotCtx, entity, row)
		if !snapshot.rowAllowed || form == nil {
			return nil
		}

		tablePartRows := make(map[string][]map[string]any, len(entity.TableParts))
		for _, tablePart := range entity.TableParts {
			rows, err := s.store.GetTablePartRows(snapshotCtx, entity.Name, tablePart.Name, id, tablePart)
			if err != nil {
				return fmt.Errorf("табличная часть %s: %w", tablePart.Name, err)
			}
			tablePartRows[tablePart.Name] = rows
		}
		snapshot.hookObject = s.runtimeObjectFromSnapshot(snapshotCtx, entity, id, row, tablePartRows)
		return nil
	})
	if err != nil {
		return nil, err
	}
	if snapshot.rowAllowed && snapshot.row != nil {
		// Отдельная копия оставляет hookObject с каноническими значениями, а из
		// этого helper наружу выпускает только уже замаскированный снимок.
		snapshot.row = cloneRecord(snapshot.row)
		s.maskRecord(ctx, entity, snapshot.row)
	}
	return snapshot, nil
}

// detailPanelRecord отдаёт payload боковой панели для ОДНОЙ записи.
//
// Раньше payload уезжал в атрибут data-ob-detail КАЖДОЙ строки списка —
// безусловно, даже когда панель у пользователя выключена (её включение живёт в
// localStorage, сервер о нём не знает). В атрибут попадали ВСЕ реквизиты шапки,
// а не только показанные колонки: полная карточка каждой записи оказывалась в
// DOM, доступном расширению браузера и обычному «сохранить страницу» (#860).
//
// Ленивая загрузка убирает и то, и другое: данных нет в разметке вовсе, а
// запрос уходит только когда панель открыта и строка выбрана. Заодно это тот
// самый ленивый хендлер, которого ждал этап 118D плана.
//
// Границы прав здесь те же, что у карточки: getEntity → requirePerm(read) →
// чтение строки → rowAllowed (строковые политики) → maskRecord (маска ПДн).
// Ни одна из них не «наследуется» из списка: запрос приходит отдельный, и
// проверять его надо целиком.
func (s *Server) detailPanelRecord(w http.ResponseWriter, r *http.Request) {
	entity := s.getEntity(w, r)
	if entity == nil {
		return
	}
	if !s.requirePerm(w, r, string(entity.Kind), entity.Name, "read") {
		return
	}
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		http.Error(w, "invalid id", http.StatusBadRequest)
		return
	}
	objForm := pickObjectFormWithReadHook(entity)
	snapshot, err := s.loadDetailPanelSnapshot(r.Context(), entity, objForm, id)
	if err != nil {
		if storage.IsNotFound(err) {
			http.Error(w, s.errText(r, err), http.StatusNotFound)
		} else {
			s.serverError(w, r, fmt.Errorf("панель деталей: загрузка записи: %w", err))
		}
		return
	}
	if snapshot.row == nil {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	if !snapshot.rowAllowed {
		s.renderForbidden(w, r)
		return
	}
	// Карточка объекта и панель деталей — два равноправных read-path. Поэтому
	// серверный read-hook формы обязан закрывать оба пути до формирования ответа
	// и видеть тот же снимок, который уже прошёл RLS и попадёт в payload.
	if objForm != nil {
		if denied := s.runFormReadHookOnObject(r.Context(), entity, objForm, snapshot.hookObject); denied != nil {
			s.renderForbidden(w, r)
			return
		}
	}
	// Порядок тот же, что в списке (handlers_entity.go): маска ПДн ДО
	// разрешения ссылок, иначе защищённое значение уже стало бы подписью.
	// loadDetailPanelSnapshot уже вернул отдельную замаскированную копию.
	row := snapshot.row
	s.resolveRefs(r.Context(), entity, []map[string]any{row})

	lang := s.resolveLang(r)
	payload := detailPanelForEntity(entity, row, s.buildEnumLabels(entity, lang), lang,
		func(key string) string { return s.tr(lang, key) })

	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")

	// Ответ не пишется строкой, хотя detailPanelForEntity уже отдал JSON:
	// значение проходит через json.Encoder, который экранирует его сам. Так
	// ответ гарантированно валиден (сломанный payload станет 500, а не мусором
	// у клиента), и статический анализ не считает данные из БД утёкшими в
	// ответ как есть (G705).
	data := detailPanelData{Tabs: []detailPanelTab{}}
	if payload != "" {
		if err := json.Unmarshal([]byte(payload), &data); err != nil {
			s.serverError(w, r, fmt.Errorf("панель деталей: %w", err))
			return
		}
	}
	if data.Tabs == nil {
		// Пустой состав — не ошибка: у сущности может не быть ни одного
		// показываемого реквизита. Клиент покажет «нет данных».
		data.Tabs = []detailPanelTab{}
	}
	if err := json.NewEncoder(w).Encode(data); err != nil {
		s.serverError(w, r, err)
	}
}
