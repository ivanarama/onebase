package userhelp_test

import (
	"encoding/json"
	"encoding/xml"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/ivantit66/onebase/internal/userhelp"
)

func TestConfluencePublishRoundTrip(t *testing.T) {
	for _, cloud := range []bool{false, true} {
		t.Run(fmt.Sprintf("cloud=%t", cloud), func(t *testing.T) {
			dir := fixture(t)
			// Exercise ordinary Markdown and escaping in the body sent by the
			// concrete HTTP publisher, including Confluence image namespaces.
			write(t, dir, "docs/help/operator.md", "# Оператор\n\n[Оформить звонок](operator/calls.md#call)\n\nFirst line  \nSecond line\n\n---\n\n&copy; &nbsp; & <text> \"quoted\" справка\n")
			b := userhelp.Load(dir)
			versions, bodies := map[string]int{}, map[string]string{}
			creates, updates, uploads := 0, 0, 0
			attachments := map[string]bool{}
			apiPath := "/context/rest/api/content"
			space := "CC"
			if cloud {
				apiPath, space = "/context/api/v2/pages", "17"
			}
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				user, token, ok := r.BasicAuth()
				if !ok || user != "writer" || token != "example-token" {
					t.Error("missing authentication")
				}
				w.Header().Set("Content-Type", "application/json")
				if strings.Contains(r.URL.Path, "/child/attachment") {
					if r.Method == "GET" && attachments[r.URL.Path+"/"+r.URL.Query().Get("filename")] {
						if _, err := fmt.Fprint(w, `{"results":[{"id":"1"}]}`); err != nil {
							t.Error(err)
						}
						return
					}
					if r.Method == "POST" {
						r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
						if r.Header.Get("X-Atlassian-Token") != "nocheck" {
							t.Error("missing CSRF header")
						}
						if err := r.ParseMultipartForm(1 << 20); err != nil {
							t.Error(err)
						}
						if r.MultipartForm != nil {
							for _, f := range r.MultipartForm.File["file"] {
								attachments[r.URL.Path+"/"+f.Filename] = true
							}
							defer func() {
								if err := r.MultipartForm.RemoveAll(); err != nil {
									t.Error(err)
								}
							}()
						}
						uploads++
					}
					if _, err := fmt.Fprint(w, `{"results":[]}`); err != nil {
						t.Error(err)
					}
					return
				}
				if !strings.HasPrefix(r.URL.Path, apiPath) {
					t.Errorf("unexpected endpoint: %s", r.URL.Path)
				}
				id := strings.TrimPrefix(r.URL.Path, apiPath+"/")
				switch r.Method {
				case "POST":
					creates++
					id = fmt.Sprint(100 + creates)
					versions[id] = 1
					var payload map[string]any
					if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
						t.Error(err)
					}
					if cloud && payload["spaceId"] != space {
						t.Error("wrong Cloud space")
					}
					if creates == 2 {
						if cloud && payload["parentId"] != "101" {
							t.Error("child not placed under parent chapter")
						}
						if !cloud && payload["ancestors"].([]any)[0].(map[string]any)["id"] != "101" {
							t.Error("child not placed under parent chapter")
						}
					}
				case "PUT":
					var payload struct {
						Version struct{ Number int }
						Body    json.RawMessage
					}
					if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
						t.Error(err)
					}
					if payload.Version.Number != versions[id]+1 {
						w.WriteHeader(http.StatusConflict)
						return
					}
					var body struct {
						Representation string
						Value          string
						Storage        struct{ Representation, Value string }
					}
					if err := json.Unmarshal(payload.Body, &body); err != nil {
						t.Error(err)
					}
					storage, representation := body.Value, body.Representation
					if !cloud {
						storage, representation = body.Storage.Value, body.Storage.Representation
					}
					if representation != "storage" {
						t.Errorf("unexpected representation: %q", representation)
					}
					decoder := xml.NewDecoder(strings.NewReader(`<root xmlns:ac="urn:ac" xmlns:ri="urn:ri">` + storage + `</root>`))
					tags := map[string]int{}
					for {
						token, err := decoder.Token()
						if err == io.EOF {
							break
						}
						if err != nil {
							t.Errorf("invalid storage XML for page %s: %v", id, err)
							http.Error(w, "invalid storage XML", http.StatusBadRequest)
							return
						}
						if start, ok := token.(xml.StartElement); ok {
							tags[start.Name.Local]++
						}
					}
					if id == "101" && (tags["br"] != 1 || tags["hr"] != 1) {
						t.Errorf("Markdown line break or rule lost: %s", storage)
					}
					updates++
					versions[id]++
					bodies[id] = string(payload.Body)
				}
				if err := json.NewEncoder(w).Encode(map[string]any{"id": id, "version": map[string]int{"number": versions[id]}}); err != nil {
					t.Error(err)
				}
			}))
			defer srv.Close()
			pub, err := userhelp.NewConfluence(userhelp.ConfluenceOptions{BaseURL: srv.URL + "/context", Space: space, User: "writer", Token: "example-token", Cloud: cloud})
			if err != nil {
				t.Fatal(err)
			}
			state, checkpoints := userhelp.PublishState{}, 0
			checkpoint := func() error { checkpoints++; return nil }
			if err := b.Publish(t.Context(), pub, "9", &state, checkpoint); err != nil {
				t.Fatal(err)
			}
			if creates != 3 || updates != 3 || uploads != 1 || checkpoints != 6 {
				t.Fatalf("creates=%d updates=%d uploads=%d checkpoints=%d", creates, updates, uploads, checkpoints)
			}
			if !strings.Contains(bodies["102"], "ac:image") || !strings.Contains(bodies["102"], "ri:attachment") || !strings.Contains(bodies["101"], "pageId=102") {
				t.Fatalf("images or cross-page links lost: %+v", bodies)
			}
			if err := b.Publish(t.Context(), pub, "9", &state, checkpoint); err != nil {
				t.Fatal(err)
			}
			if creates != 3 || updates != 3 || uploads != 1 {
				t.Fatal("unchanged publication was not idempotent")
			}
			versions["102"]++ // Someone edits Confluence independently.
			if err := b.Publish(t.Context(), pub, "9", &state, checkpoint); err == nil || !strings.Contains(err.Error(), "remote page changed") {
				t.Fatalf("manual edits overwritten: %v", err)
			}
		})
	}
}

func TestConfluenceRejectsRedirectAndRemoteErrors(t *testing.T) {
	for _, code := range []int{302, 401, 409, 500} {
		t.Run(fmt.Sprint(code), func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Location", "https://example.org")
				w.WriteHeader(code)
				if _, err := fmt.Fprint(w, "example-secret"); err != nil {
					t.Error(err)
				}
			}))
			defer srv.Close()
			pub, err := userhelp.NewConfluence(userhelp.ConfluenceOptions{BaseURL: srv.URL, Space: "CC", Token: "example-secret"})
			if err != nil {
				t.Fatal(err)
			}
			if _, err := pub.ReadPage(t.Context(), "12"); err == nil || strings.Contains(err.Error(), "example-secret") {
				t.Fatalf("unsafe or missing error: %v", err)
			}
		})
	}
}
