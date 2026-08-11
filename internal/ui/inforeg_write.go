package ui

import (
	"context"
	"errors"
	"time"

	"github.com/ivantit66/onebase/internal/exchange"
	"github.com/ivantit66/onebase/internal/i18n/i18nerr"
	"github.com/ivantit66/onebase/internal/metadata"
)

// Общая точка записи регистра сведений (план 119). Ею пользуются форма в
// интерфейсе и объект DSL «РегистрыСведений».
//
// Это не про переиспользование кода. Запись регистра требует четырёх вещей:
// права на объект, проверки строковой политики для СУЩЕСТВУЮЩЕЙ записи по
// ключу, той же проверки для новой записи и регистрации изменения в планах
// обмена — всё в одной транзакции. Копия этой последовательности рано или
// поздно разойдётся с оригиналом, и разойдётся она именно в проверке, которую
// легче всего забыть. Проверка существующей записи как раз такая: без неё
// чужую, недоступную по строковой политике строку можно перезаписать вслепую —
// знать её содержимое для этого не нужно.
//
// Отсюда контракт: любой новый путь записи зовёт infoRegWrite/infoRegRemove, а
// не storage.InfoRegSet напрямую.

// infoRegAccess — проверка доступа, которую вызывающий обязан предоставить.
// Возвращает ошибку, если запись/удаление запрещено. Форма передаёт проверку,
// работающую с HTTP-запросом, DSL — свою, по контексту сессии.
type infoRegAccess func(ctx context.Context, row map[string]any) error

// errRowPolicyDenied — отказ строковой политики. Вызывающий из HTTP уже
// отправил ответ, поэтому текст ошибки наружу не идёт.
var errRowPolicyDenied = errors.New("строковая политика запрещает запись")

// infoRegWrite записывает (upsert) строку регистра сведений.
//
// dims/resources — значения измерений и ресурсов; period обязателен для
// периодического регистра и запрещён для непериодического: молча
// проигнорированный период положил бы запись не туда, где её потом ищет
// СрезПоследних.
func (s *Server) infoRegWrite(ctx context.Context, ir *metadata.InfoRegister,
	dims, resources map[string]any, period *time.Time, allow infoRegAccess) error {
	if err := infoRegCheckPeriod(ir, period); err != nil {
		return err
	}
	if err := s.infoRegCheckRows(ctx, ir, dims, resources, period, allow); err != nil {
		return err
	}
	plans := s.reg.ExchangePlans()
	return s.store.WithTxIfNeeded(ctx, func(txCtx context.Context) error {
		if err := s.store.InfoRegSet(txCtx, ir, dims, resources, period); err != nil {
			return err
		}
		return exchange.RegisterInfoRegOnSave(txCtx, s.store, plans, ir, dims, false)
	})
}

// infoRegRemove удаляет строку по ключу измерений (и периоду у периодического).
func (s *Server) infoRegRemove(ctx context.Context, ir *metadata.InfoRegister,
	dims map[string]any, period *time.Time, allow infoRegAccess) error {
	if err := infoRegCheckPeriod(ir, period); err != nil {
		return err
	}
	if err := s.infoRegCheckRows(ctx, ir, dims, nil, period, allow); err != nil {
		return err
	}
	plans := s.reg.ExchangePlans()
	return s.store.WithTxIfNeeded(ctx, func(txCtx context.Context) error {
		if err := s.store.InfoRegDelete(txCtx, ir, dims, period); err != nil {
			return err
		}
		return exchange.RegisterInfoRegOnSave(txCtx, s.store, plans, ir, dims, true)
	})
}

// infoRegCheckRows прогоняет строковую политику по существующей записи (если
// она есть) и по новой. Обе проверки обязательны, см. комментарий к файлу.
func (s *Server) infoRegCheckRows(ctx context.Context, ir *metadata.InfoRegister,
	dims, resources map[string]any, period *time.Time, allow infoRegAccess) error {
	if allow == nil {
		return nil
	}
	if existing, ok := s.infoRegExistingPolicyRow(ctx, ir, dims, period); ok {
		if err := allow(ctx, existing); err != nil {
			return err
		}
	}
	return allow(ctx, infoRegPolicyRow(ir, dims, resources, period))
}

// infoRegCheckPeriod сверяет наличие периода с периодичностью регистра.
func infoRegCheckPeriod(ir *metadata.InfoRegister, period *time.Time) error {
	if ir.Periodic && period == nil {
		return i18nerr.Errorf("регистр сведений %s периодический: период обязателен", ir.Name)
	}
	if !ir.Periodic && period != nil {
		return i18nerr.Errorf("регистр сведений %s непериодический: период указывать нельзя", ir.Name)
	}
	return nil
}
