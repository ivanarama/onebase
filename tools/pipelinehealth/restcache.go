package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"time"
)

const (
	cacheEntryVersion = 1
	maxResponseBytes  = 64 << 20
	maxPaginationPage = 10_000
)

type githubRESTClient struct {
	baseURL    *url.URL
	token      string
	httpClient *http.Client
	cache      *restResponseCache
}

type restResponseCache struct {
	dir       string
	authScope string
}

type restCacheEntry struct {
	Version int    `json:"version"`
	URL     string `json:"url"`
	ETag    string `json:"etag"`
	Body    []byte `json:"body"`
}

func resolveCacheDir(configured string) (string, error) {
	configured = strings.TrimSpace(configured)
	if configured == "" {
		root, err := os.UserCacheDir()
		if err != nil {
			return "", fmt.Errorf("resolve user cache directory: %w", err)
		}
		configured = filepath.Join(root, "onebase", "pipelinehealth")
	}
	abs, err := filepath.Abs(configured)
	if err != nil {
		return "", fmt.Errorf("resolve pipelinehealth cache path: %w", err)
	}
	return abs, nil
}

func newGitHubRESTClient(cacheDir string) (*githubRESTClient, error) {
	host := strings.TrimSpace(os.Getenv("GH_HOST"))
	if host == "" {
		host = "github.com"
	}
	if strings.Contains(host, "://") || strings.ContainsAny(host, "/?#") {
		return nil, fmt.Errorf("invalid GH_HOST %q", host)
	}

	token, err := resolveGitHubToken(host)
	if err != nil {
		return nil, err
	}
	apiBase := "https://" + host
	if strings.EqualFold(host, "github.com") {
		apiBase = "https://api.github.com"
	} else {
		apiBase += "/api/v3"
	}
	return newGitHubRESTClientWithBase(apiBase, token, cacheDir, &http.Client{Timeout: 30 * time.Second})
}

func newGitHubRESTClientWithBase(apiBase, token, cacheDir string, httpClient *http.Client) (*githubRESTClient, error) {
	baseURL, err := url.Parse(apiBase)
	if err != nil {
		return nil, fmt.Errorf("parse GitHub API base URL: %w", err)
	}
	if baseURL.Scheme != "http" && baseURL.Scheme != "https" {
		return nil, fmt.Errorf("GitHub API base URL must use HTTP or HTTPS")
	}
	if baseURL.Host == "" {
		return nil, fmt.Errorf("GitHub API base URL has no host")
	}
	if strings.TrimSpace(token) == "" {
		return nil, fmt.Errorf("GitHub authentication token is empty")
	}
	if httpClient == nil {
		httpClient = &http.Client{Timeout: 30 * time.Second}
	}
	// cacheDir is explicit trusted operator configuration, not a path derived
	// from repository or GitHub response data.
	//nolint:gosec // G703: writing to the operator-selected cache directory is intentional.
	if err := os.MkdirAll(cacheDir, 0o700); err != nil {
		return nil, fmt.Errorf("create pipelinehealth cache directory: %w", err)
	}
	//nolint:gosec // G302: directories need the execute bit; 0700 is the restrictive directory mode.
	if err := os.Chmod(cacheDir, 0o700); err != nil {
		return nil, fmt.Errorf("secure pipelinehealth cache directory: %w", err)
	}
	authScopeDigest := sha256.Sum256([]byte(strings.ToLower(baseURL.Host) + "\n" + strings.TrimSpace(token)))
	return &githubRESTClient{
		baseURL:    baseURL,
		token:      strings.TrimSpace(token),
		httpClient: httpClient,
		cache: &restResponseCache{
			dir:       cacheDir,
			authScope: hex.EncodeToString(authScopeDigest[:]),
		},
	}, nil
}

func resolveGitHubToken(host string) (string, error) {
	var names []string
	if strings.EqualFold(host, "github.com") {
		names = []string{"GH_TOKEN", "GITHUB_TOKEN"}
	} else {
		names = []string{"GH_ENTERPRISE_TOKEN", "GITHUB_ENTERPRISE_TOKEN"}
	}
	for _, name := range names {
		if token := strings.TrimSpace(os.Getenv(name)); token != "" {
			return token, nil
		}
	}

	gh := strings.TrimSpace(os.Getenv("GH_EXE"))
	if gh == "" {
		gh = "gh"
	}
	// GH_EXE is explicit trusted operator configuration; no shell is involved.
	//nolint:gosec
	cmd := exec.Command(gh, "auth", "token", "--hostname", host)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("read GitHub token with gh: %w: %s", err, strings.TrimSpace(string(output)))
	}
	token := strings.TrimSpace(string(output))
	if token == "" {
		return "", fmt.Errorf("gh returned an empty GitHub token")
	}
	return token, nil
}

func (client *githubRESTClient) getJSON(endpoint string, destination any) error {
	requestURL, err := client.endpointURL(endpoint)
	if err != nil {
		return err
	}

	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	lock, err := acquireCacheFileLock(ctx, client.cache.lockPath(requestURL))
	if err != nil {
		return fmt.Errorf("lock REST cache for %s: %w", endpoint, err)
	}
	defer func() { _ = lock.Close() }()

	entry, cacheOK := client.cache.read(requestURL)
	var cachedValue any
	if cacheOK {
		cachedValue, err = decodeJSONLike(destination, entry.Body)
		if err != nil {
			cacheOK = false
			cachedValue = nil
		}
	}

	// requestURL was resolved by endpointURL against the validated API base and
	// is rejected if it changes either the scheme or host.
	//nolint:gosec // G704: the endpoint cannot escape the configured GitHub API host.
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, requestURL, nil)
	if err != nil {
		return fmt.Errorf("create GitHub REST request: %w", err)
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("Authorization", "Bearer "+client.token)
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	req.Header.Set("User-Agent", "onebase-pipelinehealth")
	if cacheOK {
		req.Header.Set("If-None-Match", entry.ETag)
	}

	//nolint:gosec // G704: req uses the same-host URL validated immediately above.
	response, err := client.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("GitHub REST GET %s: %w", endpoint, err)
	}
	body, readErr := readLimitedBody(response.Body, maxResponseBytes)
	closeErr := response.Body.Close()
	if readErr != nil {
		return fmt.Errorf("read GitHub REST response for %s: %w", endpoint, readErr)
	}
	if closeErr != nil {
		return fmt.Errorf("close GitHub REST response for %s: %w", endpoint, closeErr)
	}

	if response.StatusCode == http.StatusNotModified {
		if !cacheOK {
			return fmt.Errorf("GitHub REST GET %s returned 304 without a valid cache entry", endpoint)
		}
		assignDecodedJSON(destination, cachedValue)
		return nil
	}
	if response.StatusCode != http.StatusOK {
		return fmt.Errorf("GitHub REST GET %s returned %s: %s", endpoint, response.Status, abbreviatedBody(body))
	}

	freshValue, err := decodeJSONLike(destination, body)
	if err != nil {
		return fmt.Errorf("decode GitHub REST response for %s: %w", endpoint, err)
	}
	if etag := strings.TrimSpace(response.Header.Get("ETag")); etag != "" {
		if err := client.cache.write(requestURL, restCacheEntry{
			Version: cacheEntryVersion,
			URL:     requestURL,
			ETag:    etag,
			Body:    body,
		}); err != nil {
			return fmt.Errorf("persist GitHub REST cache for %s: %w", endpoint, err)
		}
	}
	assignDecodedJSON(destination, freshValue)
	return nil
}

func (client *githubRESTClient) endpointURL(endpoint string) (string, error) {
	reference, err := url.Parse(endpoint)
	if err != nil {
		return "", fmt.Errorf("parse GitHub REST endpoint: %w", err)
	}
	if reference.IsAbs() || reference.Host != "" {
		return "", fmt.Errorf("absolute GitHub REST endpoint is not allowed")
	}
	base := *client.baseURL
	base.Path = strings.TrimRight(base.Path, "/") + "/"
	resolved := base.ResolveReference(reference)
	if resolved.Host != base.Host || resolved.Scheme != base.Scheme {
		return "", fmt.Errorf("GitHub REST endpoint escaped configured API host")
	}
	return resolved.String(), nil
}

func getAllPages[T any](client *githubRESTClient, endpoint string) ([]T, error) {
	parsed, err := url.Parse(endpoint)
	if err != nil {
		return nil, fmt.Errorf("parse paginated GitHub endpoint: %w", err)
	}
	perPage := 30
	if raw := parsed.Query().Get("per_page"); raw != "" {
		value, parseErr := strconv.Atoi(raw)
		if parseErr != nil || value < 1 || value > 100 {
			return nil, fmt.Errorf("invalid per_page value %q", raw)
		}
		perPage = value
	}

	var all []T
	for page := 1; page <= maxPaginationPage; page++ {
		query := parsed.Query()
		query.Set("page", strconv.Itoa(page))
		parsed.RawQuery = query.Encode()
		var values []T
		if err := client.getJSON(parsed.String(), &values); err != nil {
			return nil, err
		}
		all = append(all, values...)
		if len(values) < perPage {
			return all, nil
		}
	}
	return nil, fmt.Errorf("GitHub pagination exceeded %d pages", maxPaginationPage)
}

func decodeJSONLike(destination any, body []byte) (any, error) {
	value := reflect.ValueOf(destination)
	if !value.IsValid() || value.Kind() != reflect.Pointer || value.IsNil() {
		return nil, fmt.Errorf("JSON destination must be a non-nil pointer")
	}
	fresh := reflect.New(value.Elem().Type()).Interface()
	if err := json.Unmarshal(body, fresh); err != nil {
		return nil, err
	}
	return fresh, nil
}

func assignDecodedJSON(destination, decoded any) {
	reflect.ValueOf(destination).Elem().Set(reflect.ValueOf(decoded).Elem())
}

func readLimitedBody(reader io.Reader, limit int64) ([]byte, error) {
	body, err := io.ReadAll(io.LimitReader(reader, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(body)) > limit {
		return nil, fmt.Errorf("response exceeds %d bytes", limit)
	}
	return body, nil
}

func abbreviatedBody(body []byte) string {
	const limit = 2048
	trimmed := strings.TrimSpace(string(body))
	if len(trimmed) > limit {
		return trimmed[:limit] + "..."
	}
	return trimmed
}

func (cache *restResponseCache) entryPath(requestURL string) string {
	digest := sha256.Sum256([]byte(cache.authScope + "\napplication/vnd.github+json\n2022-11-28\n" + requestURL))
	return filepath.Join(cache.dir, hex.EncodeToString(digest[:])+".json")
}

func (cache *restResponseCache) lockPath(requestURL string) string {
	return cache.entryPath(requestURL) + ".lock"
}

func (cache *restResponseCache) read(requestURL string) (restCacheEntry, bool) {
	path := cache.entryPath(requestURL)
	// entryPath appends a fixed suffix to a SHA-256 digest under the trusted
	// operator-selected cache directory; requestURL cannot add path segments.
	//nolint:gosec // G703: path is a digest-derived cache entry, not a request path.
	info, err := os.Stat(path)
	if err != nil || info.IsDir() || info.Size() > maxResponseBytes*2 {
		return restCacheEntry{}, false
	}
	//nolint:gosec // G703: path is the same digest-derived cache entry validated above.
	data, err := os.ReadFile(path)
	if err != nil {
		return restCacheEntry{}, false
	}
	var entry restCacheEntry
	if err := json.Unmarshal(data, &entry); err != nil ||
		entry.Version != cacheEntryVersion || entry.URL != requestURL ||
		strings.TrimSpace(entry.ETag) == "" || entry.Body == nil {
		return restCacheEntry{}, false
	}
	return entry, true
}

func (cache *restResponseCache) write(requestURL string, entry restCacheEntry) error {
	if entry.Version != cacheEntryVersion || entry.URL != requestURL ||
		strings.TrimSpace(entry.ETag) == "" || entry.Body == nil {
		return fmt.Errorf("invalid REST cache entry")
	}
	data, err := json.Marshal(entry)
	if err != nil {
		return err
	}
	temporary, err := os.CreateTemp(cache.dir, ".pipelinehealth-cache-*")
	if err != nil {
		return err
	}
	temporaryPath := temporary.Name()
	cleanup := func() {
		_ = temporary.Close()
		removeCreatedCacheTemp(temporaryPath)
	}
	if err := temporary.Chmod(0o600); err != nil {
		cleanup()
		return err
	}
	if _, err := temporary.Write(data); err != nil {
		cleanup()
		return err
	}
	if err := temporary.Sync(); err != nil {
		cleanup()
		return err
	}
	if err := temporary.Close(); err != nil {
		removeCreatedCacheTemp(temporaryPath)
		return err
	}
	if err := replaceCacheFile(temporaryPath, cache.entryPath(requestURL)); err != nil {
		removeCreatedCacheTemp(temporaryPath)
		return err
	}
	// cache.dir is explicit trusted operator configuration.
	//nolint:gosec // G703: opening the selected cache directory is intentional.
	if directory, err := os.Open(cache.dir); err == nil {
		_ = directory.Sync()
		_ = directory.Close()
	}
	return nil
}

func removeCreatedCacheTemp(path string) {
	// path is returned by os.CreateTemp above and is never constructed from
	// repository or response content.
	//nolint:gosec // G703: only the file created by this process is removed.
	_ = os.Remove(path)
}
