package ui

import (
	"context"
	"net/http"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/ivantit66/onebase/internal/auth"
)

const (
	messagesPerUserLimit = 200
	messageMaxLen        = 4000
)

type Message struct {
	ID   string    `json:"id"`
	Kind string    `json:"kind"`
	Text string    `json:"text"`
	Time time.Time `json:"time"`
}

type MessageStore struct {
	mu sync.Mutex
	m  map[string][]Message
}

func NewMessageStore() *MessageStore {
	return &MessageStore{m: make(map[string][]Message)}
}

func (s *MessageStore) Push(userKey, text string) {
	s.push(userKey, text, "info")
}

func (s *MessageStore) PushError(userKey, text string) {
	s.push(userKey, text, "err")
}

func (s *MessageStore) push(userKey, text, kind string) Message {
	if userKey == "" {
		userKey = "_anonymous"
	}
	if len(text) > messageMaxLen {
		text = text[:messageMaxLen] + "…"
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	list := s.m[userKey]
	msg := Message{ID: uuid.NewString(), Text: text, Time: time.Now(), Kind: kind}
	list = append(list, msg)
	if len(list) > messagesPerUserLimit {
		list = list[len(list)-messagesPerUserLimit:]
	}
	s.m[userKey] = list
	return msg
}

func (s *MessageStore) List(userKey string) []Message {
	if userKey == "" {
		userKey = "_anonymous"
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	src := s.m[userKey]
	out := make([]Message, len(src))
	copy(out, src)
	return out
}

func (s *MessageStore) Clear(userKey string) {
	if userKey == "" {
		userKey = "_anonymous"
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.m, userKey)
}

func userKeyFromRequest(r *http.Request) string {
	return userKeyFromCtx(r.Context())
}

func userKeyFromCtx(ctx context.Context) string {
	if u := auth.UserFromContext(ctx); u != nil && u.ID != "" {
		return u.ID
	}
	return "_anonymous"
}

func (s *Server) messagesList(w http.ResponseWriter, r *http.Request) {
	msgs := s.messages.List(userKeyFromRequest(r))
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	respondJSONTo(w, map[string]any{"messages": msgs, "serverTime": time.Now()})
}

func (s *Server) messagesClear(w http.ResponseWriter, r *http.Request) {
	s.messages.Clear(userKeyFromRequest(r))
	w.WriteHeader(http.StatusNoContent)
}

// Receipts bind the existing string messages to their exact store entries.
// The collector lives only for this HTTP request, including nested DSL hooks.
type messageReceipt struct{ text, id string }
type messageReceiptsKey struct{}

func withMessageReceipts(r *http.Request) *http.Request {
	if r.Context().Value(messageReceiptsKey{}) != nil {
		return r
	}
	receipts := []messageReceipt{}
	return r.WithContext(context.WithValue(r.Context(), messageReceiptsKey{}, &receipts))
}

func recordMessageReceipt(ctx context.Context, text, id string) {
	if receipts, ok := ctx.Value(messageReceiptsKey{}).(*[]messageReceipt); ok {
		*receipts = append(*receipts, messageReceipt{text: text, id: id})
	}
}

// Unstored UI warnings keep an empty ID and are still shown normally. Copies
// are consumed once so repeated identical messages retain distinct identities.
func messageReceiptIDs(ctx context.Context, texts []string) []string {
	receipts, ok := ctx.Value(messageReceiptsKey{}).(*[]messageReceipt)
	if !ok || len(*receipts) == 0 || len(texts) == 0 {
		return nil
	}
	remaining := append([]messageReceipt(nil), (*receipts)...)
	ids := make([]string, len(texts))
	for i, text := range texts {
		for j, receipt := range remaining {
			if receipt.id != "" && receipt.text == text {
				ids[i] = receipt.id
				remaining[j].id = ""
				break
			}
		}
	}
	return ids
}
