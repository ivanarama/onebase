package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestRESTCacheLoaderUsesIsolatedNamespace(t *testing.T) {
	for _, loader := range []string{"pulls", "issues"} {
		t.Run(loader, func(t *testing.T) {
			h := newRESTLoaderServer(t, loader, false)
			child := h.child(t, "fresh", false)
			child.load(t)
			h.entry(t, "fresh")
			h.success(t, child)
			rootEntries, err := os.ReadDir(h.root)
			if err != nil {
				t.Fatal(err)
			}
			if len(rootEntries) != 1 || rootEntries[0].Name() != "rest-v2-lock256" || !rootEntries[0].IsDir() {
				t.Fatalf("loader wrote outside isolated REST namespace: %v", rootEntries)
			}
			locks, err := os.ReadDir(filepath.Join(h.root, "rest-v2-lock256", "locks"))
			if err != nil || len(locks) == 0 || len(locks) > 256 {
				t.Fatalf("slot files: %v, %v", locks, err)
			}
		})
	}
}

// Independent specification of the frozen wire key, also used by the legacy
// test client. In particular it never calls the production digest/slot helper.
func specifiedRESTDigest(base, token, requestURL string) [32]byte {
	host := strings.TrimPrefix(base, "http://")
	scope := sha256.Sum256([]byte(strings.ToLower(host) + "\n" + strings.TrimSpace(token)))
	return sha256.Sum256([]byte(hex.EncodeToString(scope[:]) + "\napplication/vnd.github+json\n2022-11-28\n" + requestURL))
}

func TestRESTCacheSlotGoldenVectors(t *testing.T) {
	cache := &restResponseCache{dir: "namespace", authScope: "scope"}
	for _, tc := range []struct{ url, digest, slot string }{
		{"https://api.github.com/repos/a/b/pulls?page=1", "0e689ed46710b5ac58855e0594607d2db3d13081742bdeb6639a91e59d1427ab", "0e"},
		{"https://api.github.com/repos/a/b/issues?page=2", "a76aeb3c66ea16b1ba5f022d1b7ec474fd3e52c6a624fb1b2198cfcbb3abd8f6", "a7"},
	} {
		t.Run(tc.slot, func(t *testing.T) {
			if got := filepath.Base(cache.entryPath(tc.url)); got != tc.digest+".json" {
				t.Fatalf("entry = %q", got)
			}
			if got := cache.lockPath(tc.url); got != filepath.Join("namespace", "locks", tc.slot+".lock") {
				t.Fatalf("slot = %q", got)
			}
		})
	}
	if restCacheNamespace != "rest-v2-lock256" || restCacheLockSlots != 256 {
		t.Fatal("on-disk protocol constants changed")
	}
}

func assertRESTSlotFiles(t *testing.T, root string) map[string]os.FileInfo {
	t.Helper()
	paths, err := filepath.Glob(filepath.Join(root, "rest-v2-lock256", "locks", "*"))
	if err != nil || len(paths) == 0 || len(paths) > 256 {
		t.Fatalf("bounded lock set: files=%d err=%v", len(paths), err)
	}
	result := make(map[string]os.FileInfo)
	for _, path := range paths {
		name := filepath.Base(path)
		slot, err := strconv.ParseUint(strings.TrimSuffix(name, ".lock"), 16, 8)
		if err != nil || name != fmt.Sprintf("%02x.lock", slot) {
			t.Fatalf("invalid slot name %q", name)
		}
		info := restSlotInfo(t, path)
		if !info.Mode().IsRegular() {
			t.Fatalf("slot is not a regular file: %s", name)
		}
		if runtime.GOOS != "windows" && info.Mode().Perm() != 0o600 {
			t.Fatalf("slot mode = %o", info.Mode().Perm())
		}
		result[name] = info
	}
	err = filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if strings.HasSuffix(entry.Name(), ".json.lock") || strings.HasPrefix(entry.Name(), ".pipelinehealth-cache-") {
			t.Errorf("unexpected per-key lock or leaked temporary: %s", path)
		}
		if runtime.GOOS != "windows" {
			info, err := entry.Info()
			if err != nil {
				return err
			}
			want := os.FileMode(0o600)
			if entry.IsDir() {
				want = 0o700
			}
			if info.Mode().Perm() != want {
				t.Errorf("mode %s = %o, want %o", path, info.Mode().Perm(), want)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return result
}

func restSlotInfo(t *testing.T, path string) os.FileInfo {
	t.Helper()
	file, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	info, statErr := file.Stat()
	closeErr := file.Close()
	if statErr != nil || closeErr != nil {
		t.Fatalf("capture slot identity: %v, %v", statErr, closeErr)
	}
	// File.Stat eagerly obtains the Windows volume/file ID from this open
	// handle. os.Stat can defer that lookup until SameFile, after replacement.
	return info
}

func TestRESTCacheSlotIdentityDetectsReplacement(t *testing.T) {
	path := filepath.Join(t.TempDir(), "00.lock")
	if err := os.WriteFile(path, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	before := restSlotInfo(t, path)
	// Keep the old object present at another name so an inode/file ID cannot
	// be recycled into this test's replacement.
	if err := os.Rename(path, path+".old"); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if os.SameFile(before, restSlotInfo(t, path)) {
		t.Fatal("identity check accepted a replaced slot")
	}
}

func assertSameRESTSlots(t *testing.T, before, after map[string]os.FileInfo) {
	t.Helper()
	if len(before) != len(after) {
		t.Fatalf("slot set grew: %d -> %d", len(before), len(after))
	}
	for name, old := range before {
		// os.SameFile uses device/inode on Unix and the volume/file identity on
		// Windows. A delete/recreate at the same path must fail this assertion.
		if now, ok := after[name]; !ok || !os.SameFile(old, now) {
			t.Fatalf("permanent slot replaced: %s", name)
		}
	}
}

func (h *restLoaderServer) slotIdentity(t *testing.T) os.FileInfo {
	t.Helper()
	url := h.server.URL + h.listPath + "?direction=asc&page=1&per_page=100&sort=created&state=open"
	digest := specifiedRESTDigest(h.server.URL, "test-token", url)
	return restSlotInfo(t, filepath.Join(h.root, "rest-v2-lock256", "locks", fmt.Sprintf("%02x.lock", digest[0])))
}

func (h *restLoaderServer) assertSlotIdentity(t *testing.T, before os.FileInfo) {
	t.Helper()
	if !os.SameFile(before, h.slotIdentity(t)) {
		t.Fatal("slot was unlinked/replaced after owner/waiter release or restart")
	}
}

func TestRESTCacheCollidingSlotsAndParallelSlots(t *testing.T) {
	for _, loader := range []string{"pulls", "issues"} {
		for _, collide := range []bool{true, false} {
			t.Run(fmt.Sprintf("%s/collide_%t", loader, collide), func(t *testing.T) {
				entered := make(chan string, 16)
				release := make(chan struct{})
				var mu sync.Mutex
				active := make(map[byte]int)
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					if !requireTestAuthentication(w, r) {
						return
					}
					digest := specifiedRESTDigest("http://"+r.Host, "test-token", "http://"+r.Host+r.URL.String())
					mu.Lock()
					active[digest[0]]++
					if active[digest[0]] != 1 {
						t.Error("colliding HTTP critical sections overlapped")
					}
					mu.Unlock()
					defer func() { mu.Lock(); active[digest[0]]--; mu.Unlock() }()
					if strings.HasSuffix(r.URL.Path, "/comments") {
						_, _ = io.WriteString(w, "[]")
						return
					}
					worker := r.Header.Get("X-Test-Worker")
					entered <- worker
					select {
					case <-release:
					case <-r.Context().Done():
						return
					}
					_ = json.NewEncoder(w).Encode([]map[string]any{{"number": 7, "title": worker, "state": "open"}})
				}))
				t.Cleanup(server.Close)
				t.Cleanup(func() {
					select {
					case <-release:
					default:
						close(release)
					}
				})
				// Select independent loader repositories by the specified key, not
				// by calling the production slot function under test.
				repoA, repoB := "fixture/a", ""
				urlFor := func(repo string) string {
					return server.URL + "/repos/" + repo + "/" + loader + "?direction=asc&page=1&per_page=100&sort=created&state=open"
				}
				first := specifiedRESTDigest(server.URL, "test-token", urlFor(repoA))
				for n := 0; n < 10000; n++ {
					candidate := fmt.Sprintf("fixture/b%d", n)
					digest := specifiedRESTDigest(server.URL, "test-token", urlFor(candidate))
					if (first[0] == digest[0]) == collide {
						repoB = candidate
						break
					}
				}
				if repoB == "" {
					t.Fatal("no collision fixture")
				}
				root := t.TempDir()
				a := startRESTLoaderChild(t, restLoaderProcessConfig{Base: server.URL, Root: root, Loader: loader, Worker: "A", Repository: repoA})
				b := startRESTLoaderChild(t, restLoaderProcessConfig{Base: server.URL, Root: root, Loader: loader, Worker: "B", Repository: repoB})
				a.load(t)
				select {
				case worker := <-entered:
					if worker != "A" {
						t.Fatalf("first worker %q", worker)
					}
				case <-time.After(10 * time.Second):
					t.Fatal("A did not enter HTTP")
				}
				b.load(t)
				if collide {
					select {
					case worker := <-entered:
						t.Fatalf("colliding worker entered before release: %s", worker)
					case <-time.After(350 * time.Millisecond):
					}
				} else {
					select {
					case worker := <-entered:
						if worker != "B" {
							t.Fatalf("second worker %q", worker)
						}
					case <-time.After(10 * time.Second):
						t.Fatal("different slots serialized behind a global lock")
					}
				}
				close(release)
				for i, child := range []*restLoaderChild{a, b} {
					result := child.message(t, "done", 15*time.Second)
					child.wait(t, false)
					want := []string{"A", "B"}[i]
					if result.Error != "" {
						t.Fatal(result.Error)
					}
					if loader == "pulls" {
						if len(result.Snapshot.Pulls) != 1 || result.Snapshot.Pulls[0].Title != want {
							t.Fatal("wrong colliding pull body")
						}
					} else if len(result.Snapshot.Issues) != 1 || result.Snapshot.Issues[0].Title != want {
						t.Fatal("wrong colliding issue body")
					}
				}
			})
		}
	}
}

// Frozen per-entry-lock protocol for mixed-version tests ONLY. This does not
// add a legacy fallback to the production constructor or loaders. The small
// fixture has one list page and one comments page, with the original envelope,
// complete read/HTTP/persist critical section and atomic replacement.
func loadLegacyRESTFixture(config restLoaderProcessConfig) (restLoaderSnapshot, error) {
	var snapshot restLoaderSnapshot
	fetch := func(endpoint string, target any) error {
		url := config.Base + endpoint
		digest := specifiedRESTDigest(config.Base, config.Token, url)
		path := filepath.Join(config.Root, hex.EncodeToString(digest[:])+".json")
		ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
		defer cancel()
		lock, err := acquireCacheFileLock(ctx, path+".lock")
		if err != nil {
			return err
		}
		defer func() { _ = lock.Close() }()
		var entry restCacheEntry
		data, readErr := os.ReadFile(path)
		valid := readErr == nil && json.Unmarshal(data, &entry) == nil && entry.Version == 1 && entry.URL == url && entry.ETag != "" && json.Valid(entry.Body)
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
		if err != nil {
			return err
		}
		req.Header.Set("Authorization", "Bearer "+config.Token)
		req.Header.Set("Accept", "application/vnd.github+json")
		req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
		if valid {
			req.Header.Set("If-None-Match", entry.ETag)
		}
		client := &http.Client{Transport: restLoaderTransport{base: http.DefaultTransport, worker: config.Worker}}
		response, err := client.Do(req)
		if err != nil {
			return err
		}
		body, err := readLimitedBody(response.Body, maxResponseBytes)
		closeErr := response.Body.Close()
		if err != nil {
			return err
		}
		if closeErr != nil {
			return closeErr
		}
		if response.StatusCode == http.StatusNotModified && valid {
			return json.Unmarshal(entry.Body, target)
		}
		if response.StatusCode != http.StatusOK {
			return fmt.Errorf("legacy HTTP %d", response.StatusCode)
		}
		if err := json.Unmarshal(body, target); err != nil {
			return err
		}
		entry = restCacheEntry{Version: 1, URL: url, ETag: response.Header.Get("ETag"), Body: body}
		data, err = json.Marshal(entry)
		if err != nil {
			return err
		}
		temporary, err := os.CreateTemp(config.Root, ".pipelinehealth-cache-*")
		if err != nil {
			return err
		}
		defer func() { _ = os.Remove(temporary.Name()) }()
		if _, err := temporary.Write(data); err != nil {
			_ = temporary.Close()
			return err
		}
		if err := temporary.Sync(); err != nil {
			_ = temporary.Close()
			return err
		}
		if err := temporary.Close(); err != nil {
			return err
		}
		return replaceCacheFile(temporary.Name(), path)
	}
	list := "/repos/" + config.Repository + "/" + config.Loader + "?direction=asc&page=1&per_page=100&sort=created&state=open"
	if config.Loader == "pulls" {
		if err := fetch(list, &snapshot.Pulls); err != nil {
			return snapshot, err
		}
		for _, pull := range snapshot.Pulls {
			var comments []apiComment
			if err := fetch(fmt.Sprintf("/repos/%s/issues/%d/comments?page=1&per_page=100", config.Repository, pull.Number), &comments); err != nil {
				return snapshot, err
			}
			snapshot.PullComments = append(snapshot.PullComments, comments)
		}
	} else {
		if err := fetch(list, &snapshot.Issues); err != nil {
			return snapshot, err
		}
		filtered := snapshot.Issues[:0]
		for _, issue := range snapshot.Issues {
			if issue.PullRequest != nil {
				continue
			}
			if err := fetch(fmt.Sprintf("/repos/%s/issues/%d/comments?page=1&per_page=100", config.Repository, issue.Number), &issue.Thread); err != nil {
				return snapshot, err
			}
			filtered = append(filtered, issue)
		}
		snapshot.Issues = filtered
	}
	return snapshot, nil
}

func TestRESTCacheColdMigrationAndMixedProcesses(t *testing.T) {
	for _, loader := range []string{"pulls", "issues"} {
		t.Run(loader, func(t *testing.T) {
			h := newRESTLoaderServer(t, loader, false)
			h.mu.Lock()
			h.mixedLegacy = true
			h.mu.Unlock()
			// The legacy owner is held in HTTP while the new process must be
			// independently able to obtain the same complete snapshot.
			legacy := startRESTLoaderChild(t, restLoaderProcessConfig{Base: h.server.URL, Root: h.root, Loader: loader, Worker: "A", Legacy: true})
			legacy.load(t)
			h.entry(t, "A")
			fresh := h.child(t, "fresh", false)
			fresh.load(t)
			if r := h.entry(t, "fresh"); r.ETag != "" {
				t.Fatal("new namespace sent legacy ETag")
			}
			h.success(t, fresh)
			close(h.release)
			h.success(t, legacy)
			seedNames := []string{".pipelinehealth-cache-legacy-orphan", "operator-owned-file"}
			for _, name := range seedNames {
				if err := os.WriteFile(filepath.Join(h.root, name), []byte("leave unchanged"), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			before := make(map[string][]byte)
			infos := make(map[string]os.FileInfo)
			entries, err := os.ReadDir(h.root)
			if err != nil {
				t.Fatal(err)
			}
			for _, e := range entries {
				if e.IsDir() {
					continue
				}
				path := filepath.Join(h.root, e.Name())
				before[e.Name()], err = os.ReadFile(path)
				if err != nil {
					t.Fatal(err)
				}
				infos[e.Name()] = restSlotInfo(t, path)
			}
			// Remove only this test's NEW namespace after all children are reaped,
			// to prove a cold migration from the populated legacy root.
			if err := os.RemoveAll(filepath.Join(h.root, "rest-v2-lock256")); err != nil {
				t.Fatal(err)
			}
			cold := h.child(t, "cold", false)
			cold.load(t)
			if r := h.entry(t, "cold"); r.ETag != "" {
				t.Fatal("cold migration read legacy cache")
			}
			h.success(t, cold)
			for name, want := range before {
				path := filepath.Join(h.root, name)
				got, err := os.ReadFile(path)
				if err != nil || !bytes.Equal(got, want) {
					t.Fatalf("legacy seed changed: %s: %v", name, err)
				}
				info := restSlotInfo(t, path)
				if !os.SameFile(infos[name], info) || !info.ModTime().Equal(infos[name].ModTime()) {
					t.Fatalf("legacy seed replaced/touched: %s", name)
				}
			}
			h.disk(t, true)
		})
	}
}

func loadRESTSnapshot(client *githubRESTClient, loader string) (restLoaderSnapshot, error) {
	var result restLoaderSnapshot
	var err error
	if loader == "pulls" {
		result.Pulls, err = loadPulls(client, "ivanarama/onebase", "ivanarama", "")
		for i, pull := range result.Pulls {
			result.PullComments = append(result.PullComments, pull.Comments)
			// Match the subprocess wire snapshot: Comments has json:"-" and
			// travels in the explicit PullComments field instead.
			result.Pulls[i].Comments = nil
		}
	} else {
		result.Issues, err = loadIssues(client, "ivanarama/onebase", "", false)
	}
	return result, err
}

func TestRESTCacheLoadersRejectInvalidEnvelope(t *testing.T) {
	for _, loader := range []string{"pulls", "issues"} {
		for _, corrupt := range []string{"version", "url"} {
			t.Run(loader+"/"+corrupt, func(t *testing.T) {
				h := newRESTLoaderServer(t, loader, false)
				client, err := newGitHubRESTClientWithBase(h.server.URL, "test-token", h.root, h.server.Client())
				if err != nil {
					t.Fatal(err)
				}
				for round := range 3 {
					got, err := loadRESTSnapshot(client, loader)
					if err != nil || !reflect.DeepEqual(got, h.expected) {
						t.Fatalf("snapshot: %v, %+v", err, got)
					}
					request := h.entry(t, "")
					if (round == 2) != (request.ETag != "") {
						t.Fatalf("invalid envelope supplied ETag: round=%d request=%+v", round, request)
					}
					if round != 0 {
						continue
					}
					url := h.server.URL + h.listPath + "?direction=asc&page=1&per_page=100&sort=created&state=open"
					digest := specifiedRESTDigest(h.server.URL, "test-token", url)
					path := filepath.Join(h.root, "rest-v2-lock256", hex.EncodeToString(digest[:])+".json")
					data, err := os.ReadFile(path)
					if err != nil {
						t.Fatal(err)
					}
					var entry restCacheEntry
					if err := json.Unmarshal(data, &entry); err != nil {
						t.Fatal(err)
					}
					if corrupt == "version" {
						entry.Version++
					} else {
						entry.URL += "wrong"
					}
					data, err = json.Marshal(entry)
					if err != nil {
						t.Fatal(err)
					}
					if err := os.WriteFile(path, data, 0o600); err != nil {
						t.Fatal(err)
					}
				}
			})
		}
	}
}

func TestRESTCacheDirectorySetupFailsClosed(t *testing.T) {
	for _, blocked := range []string{"root", "namespace", "locks"} {
		t.Run(blocked, func(t *testing.T) {
			root := t.TempDir()
			path := filepath.Join(root, "rest-v2-lock256")
			if blocked == "root" {
				root = filepath.Join(root, "file")
				path = root
			}
			if blocked == "locks" {
				if err := os.Mkdir(path, 0o700); err != nil {
					t.Fatal(err)
				}
				path = filepath.Join(path, "locks")
			}
			if err := os.WriteFile(path, []byte("operator seed"), 0o600); err != nil {
				t.Fatal(err)
			}
			client, err := newGitHubRESTClientWithBase("http://127.0.0.1:1", "test-token", root, nil)
			if err == nil || client != nil || !strings.Contains(err.Error(), "create pipelinehealth cache directory") {
				t.Fatalf("invalid directory accepted: %v", err)
			}
			got, err := os.ReadFile(path)
			if err != nil || string(got) != "operator seed" {
				t.Fatal("directory setup altered operator file")
			}
		})
	}
}

func TestRESTCacheLoadersPersistAndLockErrors(t *testing.T) {
	for _, loader := range []string{"pulls", "issues"} {
		for _, blocked := range []string{"persist", "locks"} {
			t.Run(loader+"/"+blocked, func(t *testing.T) {
				h := newRESTLoaderServer(t, loader, false)
				client, err := newGitHubRESTClientWithBase(h.server.URL, "test-token", h.root, h.server.Client())
				if err != nil {
					t.Fatal(err)
				}
				url := h.server.URL + h.listPath + "?direction=asc&page=1&per_page=100&sort=created&state=open"
				digest := specifiedRESTDigest(h.server.URL, "test-token", url)
				path := filepath.Join(h.root, "rest-v2-lock256", hex.EncodeToString(digest[:])+".json")
				if blocked == "persist" {
					if err := os.Mkdir(path, 0o700); err != nil {
						t.Fatal(err)
					}
				} else {
					path = filepath.Join(h.root, "rest-v2-lock256", "locks")
					if err := os.Remove(path); err != nil {
						t.Fatal(err)
					}
					if err := os.WriteFile(path, []byte("blocked"), 0o600); err != nil {
						t.Fatal(err)
					}
				}
				got, err := loadRESTSnapshot(client, loader)
				want := "persist GitHub REST cache"
				if blocked == "locks" {
					want = "lock REST cache"
				}
				if err == nil || !strings.Contains(err.Error(), want) || !reflect.DeepEqual(got, restLoaderSnapshot{}) || strings.Contains(err.Error(), "test-token") {
					t.Fatalf("loader did not fail closed: %v, %+v", err, got)
				}
				if blocked == "persist" {
					h.entry(t, "")
				} else {
					h.mu.Lock()
					requests := len(h.requests)
					h.mu.Unlock()
					if requests != 0 {
						t.Fatal("lock error fell back to unprotected HTTP")
					}
				}
				if err := os.Remove(path); err != nil {
					t.Fatal(err)
				}
				if blocked == "locks" {
					if err := os.Mkdir(path, 0o700); err != nil {
						t.Fatal(err)
					}
				}
				got, err = loadRESTSnapshot(client, loader)
				if err != nil || !reflect.DeepEqual(got, h.expected) {
					t.Fatalf("recovery: %v, %+v", err, got)
				}
				if r := h.entry(t, ""); r.ETag != "" {
					t.Fatal("failed persist produced a usable entry")
				}
				h.disk(t, true)
			})
		}
	}
}

func TestRESTCacheLoaderHTTP45SecondBudget(t *testing.T) {
	for _, loader := range []string{"pulls", "issues"} {
		t.Run(loader, func(t *testing.T) {
			t.Parallel()
			h := newRESTLoaderServer(t, loader, false)
			seed := h.child(t, "seed", false)
			seed.load(t)
			h.entry(t, "seed")
			h.success(t, seed)
			before := h.slotIdentity(t)
			a := h.child(t, "A", false)
			started := time.Now()
			a.load(t)
			if r := h.entry(t, "A"); r.ETag == "" {
				t.Fatal("HTTP timeout did not exercise warm cache")
			}
			result := a.message(t, "done", 60*time.Second)
			elapsed := time.Since(started)
			if result.Error == "" || !strings.Contains(result.Error, "context deadline exceeded") || strings.Contains(result.Error, "test-token") || !reflect.DeepEqual(result.Snapshot, restLoaderSnapshot{}) || elapsed < 44*time.Second || elapsed > 60*time.Second {
				t.Fatalf("HTTP deadline: elapsed=%s result=%+v", elapsed, result)
			}
			a.wait(t, false)
			h.ownerExited(t)
			h.disk(t, true)
			fresh := h.child(t, "after", false)
			fresh.load(t)
			h.entry(t, "after")
			h.success(t, fresh)
			h.assertSlotIdentity(t, before)
		})
	}
}

func TestRESTCacheBoundedSlotsAcrossProcessesAndTokens(t *testing.T) {
	for _, loader := range []string{"pulls", "issues"} {
		t.Run(loader, func(t *testing.T) {
			root := t.TempDir()
			var numbers []int
			var calls, conditional atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				auth := r.Header.Get("Authorization")
				if auth != "Bearer test-token" && auth != "Bearer other-test-token" {
					t.Error("missing test authentication")
					w.WriteHeader(http.StatusUnauthorized)
					return
				}
				calls.Add(1)
				if r.Header.Get("If-None-Match") == `"stable"` {
					conditional.Add(1)
					w.WriteHeader(http.StatusNotModified)
					return
				}
				w.Header().Set("ETag", `"stable"`)
				if strings.HasSuffix(r.URL.Path, "/comments") {
					parts := strings.Split(r.URL.Path, "/")
					n, _ := strconv.Atoi(parts[len(parts)-2])
					_ = json.NewEncoder(w).Encode([]map[string]any{{"id": n, "body": fmt.Sprintf("comment-%d", n), "user": map[string]string{"login": "ivanarama"}}})
					return
				}
				if r.URL.Path != "/repos/ivanarama/onebase/"+loader || r.URL.Query().Get("per_page") != "100" {
					t.Errorf("unexpected endpoint: %s", r.URL)
					w.WriteHeader(http.StatusBadRequest)
					return
				}
				page, _ := strconv.Atoi(r.URL.Query().Get("page"))
				if page < 1 || page > 11 {
					t.Errorf("invalid page: %d", page)
					w.WriteHeader(http.StatusBadRequest)
					return
				}
				rows := make([]map[string]any, 0)
				for _, n := range numbers[(page-1)*100 : min(page*100, len(numbers))] {
					rows = append(rows, map[string]any{"number": n, "title": fmt.Sprintf("item-%d", n), "state": "open", "comments": 1})
				}
				_ = json.NewEncoder(w).Encode(rows)
			}))
			defer server.Close()
			// Guarantee all 256 names already exist before changing auth scope,
			// rather than relying on probabilistic coverage of the digest byte.
			seen, selected := make(map[byte]bool), make(map[int]bool)
			for n := 1; n <= 10000 && len(seen) < 256; n++ {
				url := fmt.Sprintf("%s/repos/ivanarama/onebase/issues/%d/comments?page=1&per_page=100", server.URL, n)
				digest := specifiedRESTDigest(server.URL, "test-token", url)
				if !seen[digest[0]] {
					seen[digest[0]], selected[n] = true, true
					numbers = append(numbers, n)
				}
			}
			if len(seen) != 256 {
				t.Fatal("fixture did not cover all slots")
			}
			for n := 1; len(numbers) < 1024; n++ {
				if !selected[n] {
					numbers = append(numbers, n)
				}
			}
			sort.Ints(numbers)
			var before map[string]os.FileInfo
			var previousCalls int32
			for round, token := range []string{"test-token", "test-token", "other-test-token"} {
				child := startRESTLoaderChild(t, restLoaderProcessConfig{Base: server.URL, Root: root, Loader: loader, Worker: fmt.Sprint(round), Token: token})
				child.load(t)
				result := child.message(t, "done", 65*time.Second)
				child.wait(t, false)
				if result.Error != "" || strings.Contains(child.stderr.String(), token) {
					t.Fatalf("loader failed or leaked authentication: %s", result.Error)
				}
				if loader == "pulls" {
					if len(result.Snapshot.Pulls) != 1024 || len(result.Snapshot.PullComments) != 1024 {
						t.Fatal("incomplete paginated pull snapshot")
					}
					for i, pull := range result.Snapshot.Pulls {
						if pull.Number != numbers[i] || pull.Title != fmt.Sprintf("item-%d", numbers[i]) || len(result.Snapshot.PullComments[i]) != 1 || result.Snapshot.PullComments[i][0].Body != fmt.Sprintf("comment-%d", numbers[i]) {
							t.Fatalf("wrong pull/comment payload at %d", i)
						}
					}
				} else {
					if len(result.Snapshot.Issues) != 1024 {
						t.Fatal("incomplete paginated issue snapshot")
					}
					for i, issue := range result.Snapshot.Issues {
						if issue.Number != numbers[i] || issue.Title != fmt.Sprintf("item-%d", numbers[i]) || len(issue.Thread) != 1 || issue.Thread[0].Body != fmt.Sprintf("comment-%d", numbers[i]) {
							t.Fatalf("wrong issue/comment payload at %d", i)
						}
					}
				}
				if got := calls.Load() - previousCalls; got != 1035 {
					t.Fatalf("HTTP count = %d, want 11 pages + 1024 comments", got)
				}
				previousCalls = calls.Load()
				wantConditional := int32(0)
				if round >= 1 {
					wantConditional = 1035
				}
				if conditional.Load() != wantConditional {
					t.Fatalf("conditional requests = %d, want %d", conditional.Load(), wantConditional)
				}
				after := assertRESTSlotFiles(t, root)
				if len(after) != 256 {
					t.Fatalf("lock count = %d, want 256", len(after))
				}
				if before != nil {
					assertSameRESTSlots(t, before, after)
				}
				before = after
				entries, err := filepath.Glob(filepath.Join(root, "rest-v2-lock256", "*.json"))
				want := 1035
				if round == 2 {
					want *= 2
				}
				if err != nil || len(entries) != want {
					t.Fatalf("isolated entries = %d, want %d: %v", len(entries), want, err)
				}
			}
		})
	}
}
