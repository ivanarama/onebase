package ui

import (
	"errors"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"strings"
)

const (
	defaultUIUploadBytes = int64(50 << 20)
	uiMultipartOverhead  = int64(2 << 20)
)

var errUploadTooLarge = errors.New("uploaded file is too large")

func (s *Server) effectiveUploadLimit() int64 {
	if s.maxFileSizeBytes > 0 {
		return s.maxFileSizeBytes
	}
	return defaultUIUploadBytes
}

func (s *Server) limitMultipartRequest(w http.ResponseWriter, r *http.Request) int64 {
	limit := s.effectiveUploadLimit()
	r.Body = http.MaxBytesReader(w, r.Body, limit+uiMultipartOverhead)
	return limit
}

// parseBoundedForm avoids ParseMultipartForm's misleading ErrNotMultipart for
// ordinary urlencoded forms while retaining its bounded temp-file behavior for
// actual multipart requests.
func parseBoundedForm(r *http.Request, maxMemory int64) error {
	contentType, _, _ := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if strings.EqualFold(contentType, "multipart/form-data") {
		return r.ParseMultipartForm(maxMemory)
	}
	return r.ParseForm()
}

func readUploadedBytes(file multipart.File, limit int64) ([]byte, error) {
	data, err := io.ReadAll(io.LimitReader(file, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > limit {
		return nil, errUploadTooLarge
	}
	return data, nil
}

func uploadErrorStatus(err error) int {
	var maxErr *http.MaxBytesError
	if errors.As(err, &maxErr) || errors.Is(err, errUploadTooLarge) {
		return http.StatusRequestEntityTooLarge
	}
	return http.StatusBadRequest
}
