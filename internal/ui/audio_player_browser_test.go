package ui

import (
	"bytes"
	"context"
	"encoding/binary"
	"fmt"
	"github.com/go-chi/chi/v5"
	"github.com/ivantit66/onebase/internal/auth"
	"github.com/ivantit66/onebase/internal/metadata"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// Explicit opt-in: this test owns exactly one Chromium and only synthetic WAVs.
// It requires the audio attachment MIME/Range prerequisite (#2313).
func TestAudioPlayerBrowserHTTP(t *testing.T) {
	if os.Getenv("ONEBASE_AUDIO_BROWSER") != "1" {
		t.Skip("set ONEBASE_AUDIO_BROWSER=1 for the single-browser HTTP smoke")
	}
	python, err := exec.LookPath("python")
	if err != nil {
		t.Skip("Python/Playwright is unavailable")
	}
	recordings := ownerCatalog("Recordings")
	recordings.AudioPlayer, recordings.Title = true, "Recordings"
	calls := &metadata.Entity{Name: "Calls", Kind: metadata.KindCatalog, Fields: []metadata.Field{{Name: "Name", Type: metadata.FieldTypeString}}}
	ordinary := &metadata.Entity{Name: "Ordinary", Kind: metadata.KindCatalog}
	s, ctx := newSubmitTestServer(t, []*metadata.Entity{recordings, calls, ordinary})
	s.store.SetFilesDir(t.TempDir())
	if err := s.store.EnsureAttachmentTable(ctx); err != nil {
		t.Fatal(err)
	}
	wav := make([]byte, 32044)
	copy(wav, "RIFF")
	binary.LittleEndian.PutUint32(wav[4:], uint32(len(wav)-8))
	copy(wav[8:], "WAVEfmt ")
	binary.LittleEndian.PutUint32(wav[16:], 16)
	binary.LittleEndian.PutUint16(wav[20:], 1)
	binary.LittleEndian.PutUint16(wav[22:], 1)
	binary.LittleEndian.PutUint32(wav[24:], 8000)
	binary.LittleEndian.PutUint32(wav[28:], 16000)
	binary.LittleEndian.PutUint16(wav[32:], 2)
	binary.LittleEndian.PutUint16(wav[34:], 16)
	copy(wav[36:], "data")
	binary.LittleEndian.PutUint32(wav[40:], uint32(len(wav)-44))
	first := uploadHTTPAttachment(t, s, recordings, "alice", "call-a.wav", "audio/wav", "", string(wav))
	second := uploadHTTPAttachment(t, s, recordings, "alice", "call-b.wav", "audio/wav", "", string(wav))
	calls.Forms = []*metadata.FormModule{{Name: "Form", Kind: "object", EntityName: calls.Name, LayoutKind: metadata.FormLayoutManaged,
		Attributes: []*metadata.FormAttribute{{Name: "Source", TypeRef: "string"}, {Name: "Caption", TypeRef: "string"}, {Name: "Choice", TypeRef: "string"}},
		Elements: []*metadata.FormElement{
			{Kind: metadata.FormElementAudio, Name: "Player", DataPath: "Объект.Source", TitleFrom: "Объект.Caption", TitleMap: map[string]string{"ru": "Call A"}, Handlers: map[metadata.FormEventType]string{metadata.FormEventAudioPlay: "Prepare"}},
			{Kind: metadata.FormElementButton, Name: "SelectB", TitleMap: map[string]string{"ru": "Select B"}, Handlers: map[metadata.FormEventType]string{metadata.FormEventOnClick: "SelectB"}},
		},
		ProgramAST: mustParse(t, fmt.Sprintf(`
Процедура Prepare(Элемент)
 Если Объект.Choice = "B" Тогда Объект.Source = "%s"; Объект.Caption = "Call B";
 Иначе Объект.Source = "%s"; Объект.Caption = "Call A"; КонецЕсли;
КонецПроцедуры
Процедура SelectB(Элемент)
 ОстановитьАудио("Player"); Объект.Source = ""; Объект.Caption = "Call B"; Объект.Choice = "B";
КонецПроцедуры
`, second.ID.String(), first.ID.String())),
	}}
	router := chi.NewRouter()
	s.Mount(router)
	var playRequests, metadataRequests, mediaRequests atomic.Int64
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "POST" && strings.HasSuffix(r.URL.Path, "/form-event") {
			_ = r.ParseForm()
			if r.Form.Get("_event") == string(metadata.FormEventAudioPlay) {
				playRequests.Add(1)
			}
		}
		if strings.HasSuffix(r.URL.Path, "/attachments") {
			metadataRequests.Add(1)
		}
		if strings.HasSuffix(r.URL.Path, "/download") {
			mediaRequests.Add(1)
		}
		router.ServeHTTP(w, r.WithContext(auth.ContextWithUser(r.Context(), &auth.User{Login: "alice", IsAdmin: true})))
	})
	server := httptest.NewServer(handler)
	defer server.Close()
	testCtx, cancel := context.WithTimeout(t.Context(), 55*time.Second)
	defer cancel()
	cmd := exec.CommandContext(testCtx, python, "static/audio_browser_test.py", server.URL, first.OwnerID.String(), first.ID.String(), second.ID.String())
	hideAudioTestProcess(cmd)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("browser HTTP smoke: %v\n%s", err, out)
	}
	t.Log(string(bytes.TrimSpace(out)))
	if playRequests.Load() != 2 || metadataRequests.Load() != 1 || mediaRequests.Load() < 3 {
		t.Fatalf("HTTP counts play=%d metadata=%d media=%d", playRequests.Load(), metadataRequests.Load(), mediaRequests.Load())
	}
}
