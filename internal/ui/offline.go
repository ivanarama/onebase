package ui

import (
	"context"
	"fmt"
	"os"

	"github.com/google/uuid"
	"github.com/ivantit66/onebase/internal/dsl/interpreter"
	"github.com/ivantit66/onebase/internal/project"
	"github.com/ivantit66/onebase/internal/runtime"
	"github.com/ivantit66/onebase/internal/storage"
)

// RunProcessorOffline запускает процедуру Выполнить() обработки вне HTTP-сервера —
// для отладки из CLI. Файловые параметры читаются с диска и декодируются
// (UTF-8 с откатом на Windows-1251), как при загрузке через браузер.
// Возвращает сообщения (Сообщить) и ошибку выполнения скрипта, если она была.
func RunProcessorOffline(ctx context.Context, proj *project.Project, db *storage.DB, procName string, strParams, fileParams map[string]string) (messages []string, runErr error, err error) {
	s, reg, err := NewOfflineServer(proj, db)
	if err != nil {
		return nil, nil, err
	}
	return s.RunProcessor(ctx, reg, procName, strParams, fileParams, nil)
}

// NewOfflineServer собирает Server и Registry для офлайн-прогона обработок вне
// HTTP-сервера (procrun, раннер тестов). Регистрирует справочники/документы/
// регистры/модули/обработки так же, как полный сервер, чтобы запись данных,
// проведение и запросы работали идентично. Один и тот же сервер можно
// переиспользовать для нескольких обработок (например, набора тестов).
func NewOfflineServer(proj *project.Project, db *storage.DB) (*Server, *runtime.Registry, error) {
	reg := runtime.NewRegistry()
	reg.Load(runtime.LoadOptions{
		Entities:        proj.Entities,
		Programs:        proj.Programs,
		ManagerPrograms: proj.ManagerPrograms,
		ServicePrograms: proj.ServicePrograms,
		Registers:       proj.Registers,
		InfoRegs:        proj.InfoRegisters,
		Enums:           proj.Enums,
		Constants:       proj.Constants,
		Reports:         proj.Reports,
		PrintForms:      proj.PrintForms,
	})
	reg.LoadModules(proj.Modules)
	reg.LoadProcessors(proj.Processors)
	// Регистры бухгалтерии нужны, чтобы запросы РегистрБухгалтерии.X.Остатки()/
	// .Обороты() и проведение документов с проводками работали в offline-режиме
	// (procrun), как и на полном сервере (run.go).
	reg.LoadAccountRegisters(proj.AccountRegisters, proj.ChartsOfAccounts)

	interp := interpreter.New()
	interp.LookupProc = reg.GetModuleProc
	interp.LookupSiblingProc = reg.GetSiblingProc
	interp.LookupModuleProc = reg.GetModuleNamespacedProc
	appCfg, err := project.LoadConfig(proj.Dir)
	if err != nil {
		return nil, nil, fmt.Errorf("load app config: %w", err)
	}
	if appCfg.DSL != nil {
		interp.StrictLexicalScope = appCfg.DSL.StrictLexicalScope
	}

	s := &Server{
		store:    db,
		reg:      reg,
		interp:   interp,
		lockMgr:  runtime.NewLockManager(),
		messages: NewMessageStore(),
	}
	// Запись справочников/документов из обработки (catWriter/docWriter →
	// entityservice.Save) должна работать и в offline-режиме.
	s.entitySvc = s.newEntityService(nil)
	return s, reg, nil
}

// RunProcessor выполняет процедуру Выполнить() обработки procName на уже
// собранном офлайн-сервере. extraVars инжектируются в окружение DSL поверх
// стандартных переменных (например, объект «Утверждать» для тестов). Возвращает
// сообщения (Сообщить) и ошибку выполнения скрипта, если она была.
func (s *Server) RunProcessor(ctx context.Context, reg *runtime.Registry, procName string, strParams, fileParams map[string]string, extraVars map[string]any) (messages []string, runErr error, err error) {
	proc := reg.GetProcessor(procName)
	if proc == nil {
		return nil, nil, fmt.Errorf("обработка %q не найдена", procName)
	}
	procDecl := reg.GetProcedure(proc.Name, "Выполнить")
	if procDecl == nil {
		return nil, nil, fmt.Errorf("процедура Выполнить() не найдена в обработке %q", procName)
	}

	paramValues := map[string]any{}
	for _, p := range proc.Params {
		if p.Type == "file" {
			path, ok := fileParams[p.Name]
			if !ok {
				continue
			}
			data, readErr := os.ReadFile(path)
			if readErr != nil {
				return nil, nil, fmt.Errorf("чтение файла %s: %w", path, readErr)
			}
			paramValues[p.Name] = decodeUploadText(data)
			continue
		}
		raw, ok := strParams[p.Name]
		if !ok {
			continue
		}
		paramValues[p.Name] = parseParamValue(raw, p.Type)
	}

	paramsThis := &interpreter.MapThis{M: paramValues}
	mc := runtime.NewMovementsCollector("processor", uuid.Nil)
	dslVars, txState := s.buildDSLVarsWithMessagesTx(ctx, mc, &messages)
	defer rollbackDSLExecution(txState)
	dslVars["Параметры"] = paramsThis
	interpreter.InjectMaket(dslVars, proc.Layout)
	for k, v := range extraVars {
		dslVars[k] = v
	}

	// Параметры обработки связываем и с одноимёнными аргументами Выполнить:
	// объявленный параметр процедуры иначе затеняет инжектированный и приходит
	// пустым — молча, со значением по умолчанию (#706).
	_, runErr = s.interp.Call(procDecl, paramsThis, interpreter.BindNamedArgs(procDecl, paramValues), dslVars)
	runErr = finishDSLExecution(txState, runErr)
	return messages, runErr, nil
}
