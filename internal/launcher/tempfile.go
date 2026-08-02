package launcher

import (
	"errors"
	"io"
	"os"
)

// Временные файлы, которые сразу же читает кто-то другой.
//
// В конфигураторе это устойчивый приём: YAML из редактора пишется во временный
// файл и скармливается загрузчику (виджета, управляемой формы) — чтобы разбор
// шёл ровно тем же кодом, что и при обычной загрузке с диска, и кривой ввод не
// заменил рабочее определение.
//
// Приём хороший, но в нём три ошибки подряд оставались непроверенными:
// CreateTemp, WriteString и особенно Close. Close здесь не формальность — он
// сбрасывает буфер, и без него загрузчик читает усечённый или пустой файл.
// Дальше он честно сообщает о своей беде («missing name», «ошибка YAML»), и
// пользователь видит сообщение о том, что его YAML плохой, хотя на самом деле
// не записался временный файл. Диагностика уводит в сторону — а на диске в этот
// момент, например, кончилось место.
//
// Поэтому запись и Close проверяются, а удаление — нет: файл к тому моменту уже
// прочитан, и неудача удаления ничего не портит, кроме занятого места в TMP.

// writeTempFile создаёт временный файл с содержимым content и возвращает его
// путь вместе с функцией удаления. Вызывающий обязан отложить cleanup.
// При любой ошибке файл удаляется, а cleanup — пустышка.
func writeTempFile(pattern, content string) (path string, cleanup func(), err error) {
	noop := func() {}

	f, err := os.CreateTemp("", pattern)
	if err != nil {
		return "", noop, err
	}
	name := f.Name()
	remove := func() {
		if rerr := os.Remove(name); rerr != nil && !errors.Is(rerr, os.ErrNotExist) {
			respondLog().Debug("не удалось удалить временный файл", "path", name, "err", rerr)
		}
	}

	if _, werr := f.WriteString(content); werr != nil {
		// Close тут вторичен — основная ошибка уже есть; errors.Join
		// сохраняет обе, не подменяя причину.
		werr = errors.Join(werr, f.Close())
		remove()
		return "", noop, werr
	}
	if cerr := f.Close(); cerr != nil {
		remove()
		return "", noop, cerr
	}
	return name, remove, nil
}

// closeRead закрывает читающую сторону (загруженный файл, открытый исходник).
// Ошибка тут вторична — данные уже прочитаны, и отказывать в операции из-за
// неё было бы хуже самой ошибки. Но и глотать молча незачем: Debug.
//
// Для пишущей стороны это НЕ подходит: там Close сбрасывает буфер, и его
// ошибка основная. Такие места возвращают её вызывающему (io.Copy → out.Close
// в ai_generate и extractImportSource).
func closeRead(what string, c io.Closer) {
	if err := c.Close(); err != nil {
		respondLog().Debug("не удалось закрыть "+what, "err", err)
	}
}

// writePDFTemp дописывает содержимое во временный файл, который останется на
// диске (его открывает внешний просмотрщик), и закрывает его.
func writePDFTemp(f *os.File, content []byte) error {
	if _, err := f.Write(content); err != nil {
		return errors.Join(err, f.Close())
	}
	return f.Close()
}
