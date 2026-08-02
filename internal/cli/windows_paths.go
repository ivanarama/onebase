package cli

import (
	"fmt"
	"strings"
)

type namedPath struct {
	Label string
	Path  string
}

type networkDriveDetector func(path string) (bool, error)

var detectMappedNetworkDrive networkDriveDetector = isMappedNetworkDrive

func findMappedNetworkPaths(paths []namedPath, detect networkDriveDetector) ([]namedPath, error) {
	var mapped []namedPath
	for _, item := range paths {
		if strings.TrimSpace(item.Path) == "" {
			continue
		}
		isMapped, err := detect(item.Path)
		if err != nil {
			return nil, fmt.Errorf("%s %q: %w", item.Label, item.Path, err)
		}
		if isMapped {
			mapped = append(mapped, item)
		}
	}
	return mapped, nil
}

func mappedDriveAdvice(paths []namedPath) string {
	items := make([]string, 0, len(paths))
	for _, item := range paths {
		items = append(items, fmt.Sprintf("%s %q", item.Label, item.Path))
	}
	return fmt.Sprintf("%s находится на подключённом сетевом диске. Windows-сервис запускается от LocalSystem и не видит mapped drives. Используйте UNC-путь (\\\\server\\share\\...) или локальный диск.", strings.Join(items, ", "))
}

func warnMappedNetworkPath(label, path string, detect networkDriveDetector) {
	mapped, err := findMappedNetworkPaths([]namedPath{{Label: label, Path: path}}, detect)
	if err != nil || len(mapped) == 0 {
		return
	}
	errln("Предупреждение:", mappedDriveAdvice(mapped))
}

// quoteWindowsCommandArg формирует один аргумент для команды, которую
// пользователь может скопировать из --print в cmd.exe. Алгоритм совпадает с
// правилами CommandLineToArgvW: кавычки внутри аргумента экранируются, а
// обратные слэши перед ними и перед закрывающей кавычкой удваиваются.
func quoteWindowsCommandArg(s string) string {
	return quoteWindowsCommandArgMode(s, false)
}

func quoteWindowsCommandArgAlways(s string) string {
	return quoteWindowsCommandArgMode(s, true)
}

func quoteWindowsCommandArgMode(s string, always bool) string {
	if s == "" {
		return `""`
	}
	if !always && !strings.ContainsAny(s, " \t\"") {
		return s
	}

	var b strings.Builder
	b.Grow(len(s) + 2)
	b.WriteByte('"')
	slashes := 0
	for i := 0; i < len(s); i++ {
		switch s[i] {
		case '\\':
			slashes++
		case '"':
			b.WriteString(strings.Repeat("\\", slashes*2+1))
			b.WriteByte('"')
			slashes = 0
		default:
			b.WriteString(strings.Repeat("\\", slashes))
			slashes = 0
			b.WriteByte(s[i])
		}
	}
	b.WriteString(strings.Repeat("\\", slashes*2))
	b.WriteByte('"')
	return b.String()
}
