package ui

import (
	"context"
	"net/http"
	"sync"
	"time"

	"github.com/ivantit66/onebase/internal/auth"
)

const (
	messagesPerUserLimit = 200
	messageMaxLen        = 4000
)

type Message struct {
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
	if userKey == "" {
		userKey = "_anonymous"
	}
	if len(text) > messageMaxLen {
		text = text[:messageMaxLen] + "…"
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	list := s.m[userKey]
	list = append(list, Message{Text: text, Time: time.Now()})
	if len(list) > messagesPerUserLimit {
		list = list[len(list)-messagesPerUserLimit:]
	}
	s.m[userKey] = list
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
	respondJSONTo(w, map[string]any{"messages": msgs})
}

func (s *Server) messagesClear(w http.ResponseWriter, r *http.Request) {
	s.messages.Clear(userKeyFromRequest(r))
	w.WriteHeader(http.StatusNoContent)
}
