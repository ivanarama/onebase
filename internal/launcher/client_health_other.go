//go:build !windows

package launcher

import (
	"context"
	"net/url"
)

func systemClientHealth(context.Context, *url.URL) (int, error, bool) { return 0, nil, false }
