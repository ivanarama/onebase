// Package objstore is a minimal, dependency-free client for S3-compatible
// object storage (AWS S3, MinIO, Ceph RGW, Backblaze B2, Wasabi, …). It speaks
// just enough of the REST API for OneBase's needs — PutObject, ListObjectsV2,
// DeleteObject — signed with AWS Signature Version 4 built on the standard
// library only (net/http + crypto/{hmac,sha256}). No AWS SDK, no minio-go: this
// keeps OneBase a single self-contained binary.
//
// It is a leaf package (no dependencies on other internal/ packages) so both
// backup (off-site dumps) and storage (blob/attachment backend) can reuse it
// without import cycles.
package objstore

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/xml"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"
)

// service is fixed for S3 SigV4.
const service = "s3"

// emptyPayloadHash is sha256("") — used for requests without a body.
const emptyPayloadHash = "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855"

// unsignedPayload lets us stream a body of unknown/large size without hashing
// it first (allowed by S3-compatible stores over any transport).
const unsignedPayload = "UNSIGNED-PAYLOAD"

// Config describes how to reach an S3-compatible endpoint.
type Config struct {
	Endpoint  string // host[:port], e.g. "s3.amazonaws.com" or "minio.local:9000"
	Region    string // e.g. "us-east-1"; empty defaults to "us-east-1"
	Bucket    string
	AccessKey string
	SecretKey string
	// UseSSL toggles https (nil => true). Set to a pointer to false only for a
	// plain-http endpoint such as a local MinIO.
	UseSSL *bool
	// PathStyle chooses scheme://endpoint/bucket/key (nil => true, the widely
	// compatible default) over virtual-host scheme://bucket.endpoint/key.
	PathStyle *bool
	// HTTPClient is optional; nil uses a client with no hard timeout so large
	// uploads are governed by the request context, not a fixed deadline.
	HTTPClient *http.Client
}

// Client talks to one bucket on one endpoint.
type Client struct {
	cfg    Config
	scheme string
	hc     *http.Client
}

// New validates cfg and returns a ready client.
func New(cfg Config) (*Client, error) {
	if strings.TrimSpace(cfg.Endpoint) == "" {
		return nil, fmt.Errorf("objstore: endpoint is empty")
	}
	if strings.TrimSpace(cfg.Bucket) == "" {
		return nil, fmt.Errorf("objstore: bucket is empty")
	}
	if strings.TrimSpace(cfg.AccessKey) == "" || strings.TrimSpace(cfg.SecretKey) == "" {
		return nil, fmt.Errorf("objstore: access_key/secret_key are required")
	}
	if strings.TrimSpace(cfg.Region) == "" {
		cfg.Region = "us-east-1"
	}
	scheme := "https"
	if cfg.UseSSL != nil && !*cfg.UseSSL {
		scheme = "http"
	}
	hc := cfg.HTTPClient
	if hc == nil {
		hc = &http.Client{}
	}
	return &Client{cfg: cfg, scheme: scheme, hc: hc}, nil
}

// pathStyle reports the effective addressing style (default: path-style).
func (c *Client) pathStyle() bool {
	return c.cfg.PathStyle == nil || *c.cfg.PathStyle
}

// hostAndPath returns the request Host header value and the canonical (already
// percent-encoded) URI path for a given object key. An empty key addresses the
// bucket itself (used by ListObjectsV2).
func (c *Client) hostAndPath(key string) (host, canonicalURI string) {
	encKey := uriEncode(key, false) // keep "/" between key segments
	if c.pathStyle() {
		host = c.cfg.Endpoint
		canonicalURI = "/" + uriEncode(c.cfg.Bucket, true)
		if encKey != "" {
			canonicalURI += "/" + encKey
		}
		return host, canonicalURI
	}
	host = uriEncode(c.cfg.Bucket, true) + "." + c.cfg.Endpoint
	canonicalURI = "/"
	if encKey != "" {
		canonicalURI += encKey
	}
	return host, canonicalURI
}

// PutObject uploads size bytes read from r under key. contentType may be empty.
func (c *Client) PutObject(ctx context.Context, key string, r io.Reader, size int64, contentType string) error {
	host, canonicalURI := c.hostAndPath(key)
	req, err := c.newRequest(ctx, http.MethodPut, host, canonicalURI, "", r)
	if err != nil {
		return err
	}
	req.ContentLength = size
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	c.sign(req, unsignedPayload, time.Now().UTC())
	return c.do(req, http.StatusOK)
}

// GetObject fetches key and returns a reader over its body plus the object
// size (from Content-Length, -1 if unknown). The caller must close the reader.
func (c *Client) GetObject(ctx context.Context, key string) (io.ReadCloser, int64, error) {
	host, canonicalURI := c.hostAndPath(key)
	req, err := c.newRequest(ctx, http.MethodGet, host, canonicalURI, "", nil)
	if err != nil {
		return nil, 0, err
	}
	c.sign(req, emptyPayloadHash, time.Now().UTC())
	resp, err := c.hc.Do(req)
	if err != nil {
		return nil, 0, err
	}
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
		resp.Body.Close() //nolint:errcheck,gosec // G104: bodyclose распознаёт только прямой вызов; тело прочитано, закрытие вторично
		return nil, 0, s3Error(resp.StatusCode, body)
	}
	return resp.Body, resp.ContentLength, nil
}

// getFrom issues a GET for key starting at offset (Range: bytes=offset-) and
// returns the body; the caller closes it. offset 0 sends no Range (full object).
func (c *Client) getFrom(ctx context.Context, key string, offset int64) (io.ReadCloser, error) {
	host, canonicalURI := c.hostAndPath(key)
	req, err := c.newRequest(ctx, http.MethodGet, host, canonicalURI, "", nil)
	if err != nil {
		return nil, err
	}
	if offset > 0 {
		req.Header.Set("Range", fmt.Sprintf("bytes=%d-", offset))
	}
	c.sign(req, emptyPayloadHash, time.Now().UTC())
	resp, err := c.hc.Do(req)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusPartialContent {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
		resp.Body.Close() //nolint:errcheck,gosec // G104: bodyclose распознаёт только прямой вызов; тело прочитано, закрытие вторично
		return nil, s3Error(resp.StatusCode, body)
	}
	return resp.Body, nil
}

// OpenReadSeeker returns a lazy, Range-backed io.ReadSeekCloser over key. size
// must be the object's size (the caller knows it from stored metadata) so Seek
// to the end needs no request. No I/O happens until the first Read; Read fetches
// only from the current offset. This lets http.ServeContent stream an object
// (honoring Range requests) without a local temp copy.
func (c *Client) OpenReadSeeker(ctx context.Context, key string, size int64) io.ReadSeekCloser {
	return &rangeReader{
		ctx:  ctx,
		size: size,
		get: func(ctx context.Context, offset int64) (io.ReadCloser, error) {
			return c.getFrom(ctx, key, offset)
		},
	}
}

// rangeReader is a lazy io.ReadSeekCloser over an object: Seek only moves an
// offset (no I/O); Read opens a ranged GET at the current offset. The body is
// closed and reopened when a Seek jumps the offset.
type rangeReader struct {
	ctx  context.Context
	get  func(ctx context.Context, offset int64) (io.ReadCloser, error)
	size int64
	pos  int64
	body io.ReadCloser
}

func (r *rangeReader) Read(p []byte) (int, error) {
	if r.pos >= r.size {
		return 0, io.EOF
	}
	if r.body == nil {
		b, err := r.get(r.ctx, r.pos)
		if err != nil {
			return 0, err
		}
		r.body = b
	}
	n, err := r.body.Read(p)
	r.pos += int64(n)
	return n, err
}

func (r *rangeReader) Seek(offset int64, whence int) (int64, error) {
	var abs int64
	switch whence {
	case io.SeekStart:
		abs = offset
	case io.SeekCurrent:
		abs = r.pos + offset
	case io.SeekEnd:
		abs = r.size + offset
	default:
		return 0, fmt.Errorf("objstore: invalid whence %d", whence)
	}
	if abs < 0 {
		return 0, fmt.Errorf("objstore: negative seek position %d", abs)
	}
	if abs != r.pos {
		r.closeBody()
		r.pos = abs
	}
	return abs, nil
}

func (r *rangeReader) closeBody() {
	if r.body != nil {
		r.body.Close() //nolint:errcheck,gosec // G104: тело прочитано, закрытие вторично
		r.body = nil
	}
}

func (r *rangeReader) Close() error {
	r.closeBody()
	return nil
}

// DeleteObject removes key. A missing key is not treated as an error by S3.
func (c *Client) DeleteObject(ctx context.Context, key string) error {
	host, canonicalURI := c.hostAndPath(key)
	req, err := c.newRequest(ctx, http.MethodDelete, host, canonicalURI, "", nil)
	if err != nil {
		return err
	}
	c.sign(req, emptyPayloadHash, time.Now().UTC())
	// S3 returns 204 No Content on delete.
	return c.do(req, http.StatusNoContent, http.StatusOK)
}

// listResult mirrors the parts of the ListObjectsV2 XML response we consume.
type listResult struct {
	XMLName               xml.Name `xml:"ListBucketResult"`
	IsTruncated           bool     `xml:"IsTruncated"`
	NextContinuationToken string   `xml:"NextContinuationToken"`
	Contents              []struct {
		Key string `xml:"Key"`
	} `xml:"Contents"`
}

// ListKeys returns every object key under prefix (following pagination). Keys
// come back in the store's lexical order; callers that need newest-first sort
// themselves.
func (c *Client) ListKeys(ctx context.Context, prefix string) ([]string, error) {
	host, canonicalURI := c.hostAndPath("")
	var keys []string
	token := ""
	for {
		q := url.Values{}
		q.Set("list-type", "2")
		if prefix != "" {
			q.Set("prefix", prefix)
		}
		if token != "" {
			q.Set("continuation-token", token)
		}
		req, err := c.newRequest(ctx, http.MethodGet, host, canonicalURI, canonicalQuery(q), nil)
		if err != nil {
			return nil, err
		}
		c.sign(req, emptyPayloadHash, time.Now().UTC())
		resp, err := c.hc.Do(req)
		if err != nil {
			return nil, err
		}
		body, err := io.ReadAll(io.LimitReader(resp.Body, 16<<20))
		resp.Body.Close() //nolint:errcheck,gosec // G104: bodyclose распознаёт только прямой вызов; тело прочитано, закрытие вторично
		if err != nil {
			return nil, err
		}
		if resp.StatusCode != http.StatusOK {
			return nil, s3Error(resp.StatusCode, body)
		}
		var lr listResult
		if err := xml.Unmarshal(body, &lr); err != nil {
			return nil, fmt.Errorf("objstore: parse list response: %w", err)
		}
		for _, o := range lr.Contents {
			keys = append(keys, o.Key)
		}
		if !lr.IsTruncated || lr.NextContinuationToken == "" {
			break
		}
		token = lr.NextContinuationToken
	}
	return keys, nil
}

// newRequest builds an http.Request whose on-the-wire path is exactly
// canonicalURI (we set url.Opaque so Go does not re-escape it), so what we sign
// is byte-for-byte what we send.
func (c *Client) newRequest(ctx context.Context, method, host, canonicalURI, rawQuery string, body io.Reader) (*http.Request, error) {
	req, err := http.NewRequestWithContext(ctx, method, c.scheme+"://"+host+"/", body)
	if err != nil {
		return nil, err
	}
	req.URL.Opaque = canonicalURI
	req.URL.RawQuery = rawQuery
	return req, nil
}

// do executes req and verifies the status code, draining the body.
func (c *Client) do(req *http.Request, okStatuses ...int) error {
	resp, err := c.hc.Do(req) //nolint:gosec // G704: адрес собран из фиксированной схемы и хоста, внешний URL подставить нельзя
	if err != nil {
		return err
	}
	defer resp.Body.Close() //nolint:errcheck,gosec // G104: bodyclose распознаёт только прямой вызов; тело прочитано, закрытие вторично
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
	for _, ok := range okStatuses {
		if resp.StatusCode == ok {
			return nil
		}
	}
	return s3Error(resp.StatusCode, body)
}

// sign adds the SigV4 Authorization header (and the x-amz-* headers it covers)
// to req for the given payload hash and time.
func (c *Client) sign(req *http.Request, payloadHash string, t time.Time) {
	amzDate := t.Format("20060102T150405Z")
	dateStamp := t.Format("20060102")

	req.Header.Set("X-Amz-Date", amzDate)
	req.Header.Set("X-Amz-Content-Sha256", payloadHash)

	// Canonical headers: host + every header currently set on the request,
	// lower-cased and sorted. Signing extra headers is valid SigV4; the server
	// recomputes over SignedHeaders, so we just sign exactly what we send.
	type hdr struct{ name, value string }
	headers := []hdr{{"host", req.URL.Host}}
	for name, vals := range req.Header {
		headers = append(headers, hdr{strings.ToLower(name), strings.Join(vals, ",")})
	}
	sort.Slice(headers, func(i, j int) bool { return headers[i].name < headers[j].name })

	var canonHeaders strings.Builder
	signedNames := make([]string, 0, len(headers))
	for _, h := range headers {
		canonHeaders.WriteString(h.name)
		canonHeaders.WriteByte(':')
		canonHeaders.WriteString(trimHeaderValue(h.value))
		canonHeaders.WriteByte('\n')
		signedNames = append(signedNames, h.name)
	}
	signedHeaders := strings.Join(signedNames, ";")

	canonicalRequest := strings.Join([]string{
		req.Method,
		req.URL.Opaque,
		req.URL.RawQuery,
		canonHeaders.String(),
		signedHeaders,
		payloadHash,
	}, "\n")

	scope := dateStamp + "/" + c.cfg.Region + "/" + service + "/aws4_request"
	stringToSign := strings.Join([]string{
		"AWS4-HMAC-SHA256",
		amzDate,
		scope,
		sha256Hex([]byte(canonicalRequest)),
	}, "\n")

	signingKey := c.signingKey(dateStamp)
	signature := hex.EncodeToString(hmacSHA256(signingKey, stringToSign))

	auth := fmt.Sprintf(
		"AWS4-HMAC-SHA256 Credential=%s/%s, SignedHeaders=%s, Signature=%s",
		c.cfg.AccessKey, scope, signedHeaders, signature)
	req.Header.Set("Authorization", auth)
}

// signingKey derives the SigV4 signing key for the day.
func (c *Client) signingKey(dateStamp string) []byte {
	kDate := hmacSHA256([]byte("AWS4"+c.cfg.SecretKey), dateStamp)
	kRegion := hmacSHA256(kDate, c.cfg.Region)
	kService := hmacSHA256(kRegion, service)
	return hmacSHA256(kService, "aws4_request")
}

func hmacSHA256(key []byte, data string) []byte {
	h := hmac.New(sha256.New, key)
	h.Write([]byte(data))
	return h.Sum(nil)
}

func sha256Hex(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

// trimHeaderValue collapses internal whitespace runs and trims ends, per the
// SigV4 canonical-header rules.
func trimHeaderValue(v string) string {
	return strings.Join(strings.Fields(v), " ")
}

// uriEncode percent-encodes per RFC 3986 the way SigV4 expects (space => %20,
// upper-case hex). When encodeSlash is false, "/" is left intact (path
// separators). Operates on bytes so UTF-8 encodes correctly.
func uriEncode(s string, encodeSlash bool) string {
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		ch := s[i]
		switch {
		case (ch >= 'A' && ch <= 'Z') || (ch >= 'a' && ch <= 'z') ||
			(ch >= '0' && ch <= '9') || ch == '-' || ch == '.' || ch == '_' || ch == '~':
			b.WriteByte(ch)
		case ch == '/' && !encodeSlash:
			b.WriteByte(ch)
		default:
			fmt.Fprintf(&b, "%%%02X", ch)
		}
	}
	return b.String()
}

// canonicalQuery builds the SigV4 canonical query string: keys sorted, keys and
// values percent-encoded (everything, including "/").
func canonicalQuery(q url.Values) string {
	keys := make([]string, 0, len(q))
	for k := range q {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var parts []string
	for _, k := range keys {
		vals := append([]string(nil), q[k]...)
		sort.Strings(vals)
		for _, v := range vals {
			parts = append(parts, uriEncode(k, true)+"="+uriEncode(v, true))
		}
	}
	return strings.Join(parts, "&")
}

// s3Error turns a non-2xx response into an error, surfacing the S3 <Code> and
// <Message> when present.
func s3Error(status int, body []byte) error {
	var e struct {
		Code    string `xml:"Code"`
		Message string `xml:"Message"`
	}
	if err := xml.Unmarshal(body, &e); err == nil && e.Code != "" {
		return fmt.Errorf("objstore: s3 status %d: %s: %s", status, e.Code, e.Message)
	}
	snippet := strings.TrimSpace(string(body))
	if len(snippet) > 300 {
		snippet = snippet[:300]
	}
	return fmt.Errorf("objstore: s3 status %d: %s", status, snippet)
}
