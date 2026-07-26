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
				paramValues[p.Name] = ""
				continue
			}
			data, readErr := os.ReadFile(path)
			if readErr != nil {
				return nil, nil, fmt.Errorf("чтение файла %s: %w", path, readErr)
			}
			paramValues[p.Name] = decodeUploadText(data)
			continue
		}
		paramValues[p.Name] = parseParamValue(strParams[p.Name], p.Type)
	}

	msgFunc := interpreter.BuiltinFunc(func(args []any, _ string, _ int) (any, error) {
		if len(args) > 0 {
			messages = append(messages, fmt.Sprintf("%v", args[0]))
		}
		return nil, nil
	})

	paramsThis := &interpreter.MapThis{M: paramValues}
	mc := runtime.NewMovementsCollector("processor", uuid.Nil)
	dslVars := s.buildDSLVars(ctx, mc)
	dslVars["Параметры"] = paramsThis
	dslVars["Сообщить"] = msgFunc
	dslVars["Message"] = msgFunc
	interpreter.InjectMaket(dslVars, proc.Layout)

	runErr = s.interp.Run(procDecl, paramsThis, dslVars)
	return messages, runErr, nil
}
