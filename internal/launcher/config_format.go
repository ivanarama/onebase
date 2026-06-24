package launcher

import (
	"fmt"

	"github.com/ivantit66/onebase/internal/configfmt"
)

func formatConfigContent(relPath string, content []byte) ([]byte, error) {
	formatted, err := configfmt.FormatConfigContent(relPath, content)
	if err != nil {
		return nil, fmt.Errorf("format %s: %w", relPath, err)
	}
	return formatted, nil
}
