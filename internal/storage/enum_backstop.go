package storage

// Страховка значений перечислений на уровне записи (#962, находка Н3).
//
// Проверка значения enum-реквизита появилась в #769 и живёт КОПИЯМИ у входов:
// entityservice.Save, запись документа из DSL и проведение кнопкой зовут
// entityservice.ValidateEnumFields каждый сам. Пока путей записи три, любой
// новый вход (а их за год прибавилось три) наследует проверку только если о ней
// вспомнили — так дефект #769 и прожил после «исправления».
//
// Здесь тот же приём, что уже применён к запрету проведения помеченного на
// удаление документа (SetPosted, crud.go): гарантия ставится там, где пройти
// мимо нельзя. Проверки у входов остаются — они дают внятное сообщение раньше и
// до открытой транзакции, — но перестают быть единственной защитой.
//
// Обмен (план 121) через страховку НЕ проходит: узел-приёмник может работать на
// другой версии конфигурации, где значения перечисления отличаются, и обрывать
// репликацию из-за одного реквизита дороже, чем принять запись. Что делать с
// такими значениями — отдельное решение (#1037), и здесь оно намеренно не
// принимается: исключение сделано явным и одной строкой, а не забыто.

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/ivantit66/onebase/internal/metadata"
)

// ErrEnumValueUnknown — запись отклонена страховкой: значение не входит в
// перечисление. Типизирована, чтобы вызывающий отличал её от сбоя БД.
var ErrEnumValueUnknown = errors.New("недопустимое значение перечисления")

// EnumSource отдаёт метаданные перечислений. Интерфейс, а не *runtime.Registry:
// storage не знает про runtime, а проверку надо уметь прогонять без реестра.
type EnumSource interface {
	GetEnum(name string) *metadata.Enum
	Enums() []*metadata.Enum
}

// SetEnumSource включает страховку. Без источника проверка не работает —
// как и strict-RLS чокпоинт, она инжектится сервером при старте и при
// перезагрузке конфигурации (реестр там подменяется целиком).
//
// Значение читается атомарно: hot-reload меняет реестр под работающими
// запросами.
func (db *DB) SetEnumSource(src EnumSource) *DB {
	// nil-приёмник допустим: сервер поднимают и без хранилища (dev-режим,
	// генерация статики), и падать на настройке страховки там незачем.
	if db == nil || src == nil {
		return db
	}
	db.enumSource.Store(enumSourceBox{src: src})
	return db
}

// enumSourceBox — обёртка для atomic.Value: интерфейсные значения разных
// динамических типов в одном atomic.Value хранить нельзя.
type enumSourceBox struct{ src EnumSource }

func (db *DB) enumSourceOrNil() EnumSource {
	if db == nil {
		return nil
	}
	box, ok := db.enumSource.Load().(enumSourceBox)
	if !ok {
		return nil
	}
	return box.src
}

// enumBackstop отклоняет запись с неизвестным значением перечисления.
// Возвращает nil, если проверять нечем или запись пришла обменом.
func (db *DB) enumBackstop(ctx context.Context, entity *metadata.Entity, fields map[string]any) error {
	src := db.enumSourceOrNil()
	if src == nil || entity == nil {
		return nil
	}
	if stageModeFromCtx(ctx).Source == StageSourceExchange {
		return nil
	}
	// Табличные части сюда не приходят: они пишутся отдельным вызовом
	// (UpsertTablePartRows), и страховка для них — следующий шаг, а не молчаливо
	// пропущенный случай.
	if msg := ValidateEnumValues(src, entity, fields, nil); msg != "" {
		return fmt.Errorf("%w: %s", ErrEnumValueUnknown, msg)
	}
	return nil
}

// ValidateEnumValues проверяет значения enum-реквизитов шапки и строк табличных
// частей. Возвращает текст пользовательской ошибки либо пустую строку.
//
// Единственная реализация правила: entityservice.ValidateEnumFields теперь
// делегирует сюда. Две копии одной проверки разошлись бы в мелочах (пустое
// значение, регистр ключа, отсутствующее перечисление) — и «страховка» стала бы
// отдельным поведением вместо дубля защиты.
func ValidateEnumValues(src EnumSource, entity *metadata.Entity, fields map[string]any,
	tpRows map[string][]map[string]any) string {
	if src == nil || entity == nil {
		return ""
	}
	// Реестр без перечислений — не повод отклонять запись: проверять не с чем.
	// Так же поступает metadata.Validate; контексты вроде procrun и служебных
	// прогонов поднимают неполный реестр, и отказ здесь сломал бы рабочие
	// сценарии ради несуществующей защиты.
	if len(src.Enums()) == 0 {
		return ""
	}
	for _, f := range entity.Fields {
		if f.EnumName == "" {
			continue
		}
		if msg := checkEnumValue(src, entity.Name+"."+f.Name, f.EnumName, valueByNameFold(fields, f.Name)); msg != "" {
			return msg
		}
	}
	for _, tp := range entity.TableParts {
		rows := tpRows[tp.Name]
		for _, f := range tp.Fields {
			if f.EnumName == "" {
				continue
			}
			for i, row := range rows {
				where := fmt.Sprintf("%s.%s[%d].%s", entity.Name, tp.Name, i+1, f.Name)
				if msg := checkEnumValue(src, where, f.EnumName, valueByNameFold(row, f.Name)); msg != "" {
					return msg
				}
			}
		}
	}
	return ""
}

// checkEnumValue — одно значение. Ненайденное перечисление тоже ошибка:
// «проверить нечем» не повод записать что угодно. Пустое значение допустимо —
// «не выбрано» законно, обязательность поля проверяется другим механизмом.
func checkEnumValue(src EnumSource, where, enumName string, value any) string {
	if value == nil {
		return ""
	}
	val := strings.TrimSpace(fmt.Sprintf("%v", value))
	if val == "" || val == "<nil>" {
		return ""
	}
	en := src.GetEnum(enumName)
	if en == nil {
		return fmt.Sprintf("%s: перечисление %s не найдено", where, enumName)
	}
	for _, v := range en.Values {
		if v == val {
			return ""
		}
	}
	return fmt.Sprintf("%s: недопустимое значение «%s» (не входит в перечисление %s)", where, val, enumName)
}

// valueByNameFold достаёт значение поля без учёта регистра ключа: формы кладут
// PascalCase, Object.Set — lowercase.
func valueByNameFold(row map[string]any, name string) any {
	if row == nil {
		return nil
	}
	if v, ok := row[name]; ok {
		return v
	}
	for k, v := range row {
		if strings.EqualFold(k, name) {
			return v
		}
	}
	return nil
}
