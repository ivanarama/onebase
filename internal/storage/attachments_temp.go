package storage

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync"
)

// attachmentTempSpace owns only disposable S3 copies. Its lifetime is separate
// from the durable files root and extends past DB.Close while readers are live.
type attachmentTempSpace struct {
	mu     sync.Mutex
	root   string
	users  int
	closed bool
}

func (s *attachmentTempSpace) newDir() (string, func(), error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return "", nil, fmt.Errorf("attachments: database handle is closed")
	}
	if s.root == "" {
		base := os.TempDir()
		if !filepath.IsAbs(base) {
			return "", nil, fmt.Errorf("attachments: system temporary directory must be absolute")
		}
		root, err := os.MkdirTemp(base, "onebase-attachments-*")
		if err != nil {
			return "", nil, err
		}
		s.root = root
	}
	dir, err := os.MkdirTemp(s.root, "att-*")
	if err != nil {
		return "", nil, err
	}
	s.users++
	var once sync.Once
	return dir, func() {
		once.Do(func() {
			s.mu.Lock()
			defer s.mu.Unlock()
			removeFile(dir)
			s.users--
			if s.closed && s.users == 0 {
				removeFile(s.root)
			}
		})
	}, nil
}

func (s *attachmentTempSpace) close() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.closed = true
	if s.root != "" && s.users == 0 {
		removeFile(s.root)
	}
}

// Check cancellation between reads even for caller-provided upload readers.
// The S3 client remains responsible for interrupting a blocked network read.
type attachmentContextReader struct {
	ctx context.Context
	r   io.Reader
}

func (r attachmentContextReader) Read(p []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	return r.r.Read(p)
}
