//go:build !windows && !darwin && !dragonfly && !freebsd && !linux && !netbsd && !openbsd && !solaris

package sheet

import (
	"fmt"
	"os"
)

func tryWorkbookLock(_ *os.File) (bool, error) {
	return false, fmt.Errorf("эта ОС не поддерживает блокировку обновления XLSX")
}
