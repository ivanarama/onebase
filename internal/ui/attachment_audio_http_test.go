package ui

import (
	"encoding/json"
	"mime"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/ivantit66/onebase/internal/auth"
)

// Аудио-вложение (запись разговора) играет встроенный проигрыватель панели
// вложений: адрес скачивания отдаёт его inline с типом аудио и по частям
// (Range — ползунок без полной загрузки), список вложений помечает его
// полем audio. Права те же, что у скачивания: без чтения владельца — 403.
// Запись приходит и как application/octet-stream: тип по расширению .ogg
// знает только системная таблица типов, которой на сервере может не быть.
// Путь: зарегистрированные маршруты списка и скачивания вложений.
func TestAttachmentHTTP_AudioPlayer(t *testing.T) {
	s, entity, r := newAttachmentHTTPServer(t)
	alice := rowOwnerUser("alice", entity.Name, "read")
	body := "OggS0123456789"
	cases := []struct {
		filename, mimeType, wantAudio string
	}{
		{"запись.ogg", "application/octet-stream", "audio/ogg"},
		{"запись.ogg", "", "audio/ogg"},
		{"запись.opus", "audio/opus", "audio/ogg"},
		{"запись.mp3", "audio/mpeg", "audio/mpeg"},
		{"запись.WAV", "application/octet-stream", "audio/wav"},
		{"запись.wav", "audio/x-wav", "audio/wav"},
		// Явный не-аудио тип расширением не переписывается.
		{"запись.mp3", "text/html", ""},
		{"ролик.mp4", "video/mp4", ""},
		{"запись.flac", "application/octet-stream", ""},
	}
	for _, tc := range cases {
		t.Run(tc.filename+" "+tc.mimeType, func(t *testing.T) {
			att := uploadHTTPAttachment(t, s, entity, "alice", tc.filename, tc.mimeType, "", body)

			list := httptest.NewRequest(http.MethodGet, "/ui/catalog/"+entity.Name+"/"+att.OwnerID.String()+"/attachments", nil)
			list = list.WithContext(auth.ContextWithUser(list.Context(), alice))
			lw := httptest.NewRecorder()
			r.ServeHTTP(lw, list)
			var items []struct {
				ID    string `json:"id"`
				Audio string `json:"audio"`
			}
			if err := json.Unmarshal(lw.Body.Bytes(), &items); err != nil || lw.Code != http.StatusOK || len(items) != 1 {
				t.Fatalf("список вложений: %d %s %v", lw.Code, lw.Body.String(), err)
			}
			if items[0].Audio != tc.wantAudio {
				t.Fatalf("audio в списке = %q, ожидалось %q", items[0].Audio, tc.wantAudio)
			}

			w := downloadAttachmentHTTP(r, att, alice, nil)
			if w.Code != http.StatusOK || w.Body.String() != body {
				t.Fatalf("скачивание: %d %q", w.Code, w.Body.String())
			}
			disposition, _, _ := mime.ParseMediaType(w.Header().Get("Content-Disposition"))
			if tc.wantAudio == "" {
				// Не аудио — прежняя политика: ни типа аудио, ни inline.
				if got := w.Header().Get("Content-Type"); strings.HasPrefix(got, "audio/") || disposition == "inline" {
					t.Fatalf("не-аудио отдано проигрывателю: %q %q", got, w.Header().Get("Content-Disposition"))
				}
				return
			}
			if got := w.Header().Get("Content-Type"); got != tc.wantAudio || disposition != "inline" {
				t.Fatalf("Content-Type=%q Content-Disposition=%q", got, w.Header().Get("Content-Disposition"))
			}
			if w.Header().Get("Content-Security-Policy") != "default-src 'none'; sandbox" || w.Header().Get("X-Content-Type-Options") != "nosniff" {
				t.Fatalf("защитные заголовки потеряны: %v", w.Header())
			}
			part := downloadAttachmentHTTP(r, att, alice, http.Header{"Range": {"bytes=4-7"}})
			if part.Code != http.StatusPartialContent || part.Body.String() != "0123" || part.Header().Get("Content-Type") != tc.wantAudio {
				t.Fatalf("Range: %d %q %v", part.Code, part.Body.String(), part.Header())
			}
			denied := downloadAttachmentHTTP(r, att, rowOwnerUser("bob", entity.Name, "read"), nil)
			if denied.Code != http.StatusForbidden || denied.Body.String() == body {
				t.Fatalf("без права чтения владельца: %d", denied.Code)
			}
		})
	}
}
