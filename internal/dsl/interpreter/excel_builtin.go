package interpreter

import (
	"encoding/base64"
	"fmt"
	"strings"

	"github.com/ivantit66/onebase/internal/excel"
	"github.com/ivantit66/onebase/internal/i18n/i18nerr"
)

func init() {
	builtins["выгрузитьвexcel"] = builtinExportExcel
	builtins["exportexcel"] = builtinExportExcel
	builtins["прочитатьexcel"] = builtinImportExcel
	builtins["importexcel"] = builtinImportExcel
}

// builtinImportExcel(путь)
// Читает первый лист книги .xlsx и возвращает Массив Массивов строк: внешний
// элемент — строка книги, внутренний — ячейки. Заголовок (первая строка) НЕ
// отделяется: где у файла шапка, знает прикладной код, а не платформа.
//
// Значения приходят текстом. У ячейки Excel формат и значение живут отдельно, и
// автоматическое приведение молча портит ровно то, что чаще всего и грузят:
// код с ведущими нулями, дату в чужом формате, число, записанное строкой.
// Приводит прикладной код — он один знает, что за колонка перед ним (#1470).
//
// Путь проходит ту же файловую песочницу, что ЧтениеТекста: в demo-режиме
// обработка не должна читать книгу за пределами каталога базы.
func builtinImportExcel(args []any, file string, line int) (any, error) {
	if len(args) < 1 {
		return nil, i18nerr.New("ПрочитатьExcel: ожидается аргумент Путь (Строка)")
	}
	path, ok := args[0].(string)
	if !ok {
		return nil, i18nerr.New("ПрочитатьExcel: аргумент Путь должен быть Строкой")
	}
	if strings.TrimSpace(path) == "" {
		return nil, i18nerr.New("ПрочитатьExcel: путь не задан")
	}
	safe := safePathOrRaise("ПрочитатьExcel", path)
	rows, err := excel.ImportRows(safe)
	if err != nil {
		return nil, i18nerr.Wrapf(err, "ПрочитатьExcel")
	}
	out := &Array{}
	for _, row := range rows {
		cells := &Array{}
		for _, cell := range row {
			cells.items = append(cells.items, cell)
		}
		out.items = append(out.items, cells)
	}
	return out, nil
}

// builtinExportExcel(data, title)
// data — Массив массивов; первый подмассив — заголовки, остальные — строки данных.
// Возвращает base64-строку содержимого xlsx-файла.
func builtinExportExcel(args []any, file string, line int) (any, error) {
	if len(args) < 1 {
		return nil, i18nerr.New("ВыгрузитьВExcel: ожидается аргумент Данные (Массив)")
	}

	outerArr, ok := args[0].(*Array)
	if !ok {
		return nil, i18nerr.New("ВыгрузитьВExcel: аргумент Данные должен быть Массивом")
	}
	if len(outerArr.items) < 1 {
		return "", nil
	}

	// First row → column headers
	firstRow, ok := outerArr.items[0].(*Array)
	if !ok {
		return nil, i18nerr.New("ВыгрузитьВExcel: первая строка (заголовки) должна быть Массивом")
	}
	cols := make([]string, len(firstRow.items))
	for i, v := range firstRow.items {
		cols[i] = fmt.Sprintf("%v", v)
	}

	// Data rows
	rows := make([][]any, 0, len(outerArr.items)-1)
	for _, rowVal := range outerArr.items[1:] {
		rowArr, ok := rowVal.(*Array)
		if !ok {
			continue
		}
		cells := make([]any, len(rowArr.items))
		copy(cells, rowArr.items)
		rows = append(rows, cells)
	}

	data, err := excel.ExportList(cols, rows)
	if err != nil {
		return nil, i18nerr.Wrapf(err, "ВыгрузитьВExcel")
	}
	return base64.StdEncoding.EncodeToString(data), nil
}
