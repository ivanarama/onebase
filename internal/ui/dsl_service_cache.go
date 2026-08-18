package ui

// DSL-функции управления кэшем ответов HTTP-сервисов (план 126):
//
//	СброситьКэшСервисов();            // весь кэш процесса
//	СброситьКэшСервисов("Сайт");      // только один сервис (по имени или root_url)
//	Байт = РазмерКэшаСервисов();      // для диагностики
//
// Инвалидация «по данным» — обязанность конфигурации: платформа не отслеживает,
// какие сущности участвовали в сборке ответа (это отдельный механизм
// трассировки зависимостей). Типовой приём — вызов из ПриЗаписи контентных
// справочников.

import (
	"fmt"
	"strings"

	"github.com/ivantit66/onebase/internal/dsl/interpreter"
)

// registerServiceCacheBuiltins добавляет функции сброса кэша в DSL-окружение.
func (s *Server) registerServiceCacheBuiltins(vars map[string]any) {
	clearFn := interpreter.BuiltinFunc(func(args []any, _ string, _ int) (any, error) {
		if s.svcCache == nil {
			return float64(0), nil
		}
		target := ""
		if len(args) > 0 && args[0] != nil {
			target = strings.TrimSpace(fmt.Sprint(args[0]))
		}
		// Принимаем и имя сервиса, и root_url: конфигуратор помнит имя из
		// services/<Имя>.yaml, а ключ кэша построен по корневому URL.
		if target != "" {
			if svc := s.reg.GetHTTPService(target); svc != nil {
				target = svc.RootURL
			}
		}
		return float64(s.svcCache.Clear(target)), nil
	})

	sizeFn := interpreter.BuiltinFunc(func(_ []any, _ string, _ int) (any, error) {
		if s.svcCache == nil {
			return float64(0), nil
		}
		return float64(s.svcCache.Size()), nil
	})

	vars["СброситьКэшСервисов"] = clearFn
	vars["ResetServiceCache"] = clearFn
	vars["РазмерКэшаСервисов"] = sizeFn
	vars["ServiceCacheSize"] = sizeFn
}
