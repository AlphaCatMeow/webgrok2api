package grok

import (
	"sync"
	"time"
)

// ConversationState holds the active conversation context for one account token.
type ConversationState struct {
	ConversationID string
	LastResponseID string // user response ID of the last message (parent for next reply)
	CreatedAt      time.Time
}

// ConversationTracker stores active conversation contexts per account token.
// Entries expire after inactivity (default TTL = 5 min).
type ConversationTracker struct {
	mu   sync.RWMutex
	conv map[string]*ConversationState
	ttl  time.Duration
}

// NewConversationTracker creates a tracker with the given TTL.
func NewConversationTracker(ttl time.Duration) *ConversationTracker {
	ct := &ConversationTracker{
		conv: map[string]*ConversationState{},
		ttl:  ttl,
	}
	// Background cleanup every 2× TTL.
	go func() {
		ticker := time.NewTicker(ttl)
		defer ticker.Stop()
		for range ticker.C {
			ct.evictStale()
		}
	}()
	return ct
}

// Get retrieves the conversation state for a token. Returns nil if none or stale.
func (ct *ConversationTracker) Get(token string) *ConversationState {
	ct.mu.RLock()
	defer ct.mu.RUnlock()
	s, ok := ct.conv[token]
	if !ok {
		return nil
	}
	if time.Since(s.CreatedAt) > ct.ttl {
		return nil
	}
	return s
}

// Set stores or updates the conversation state for a token.
func (ct *ConversationTracker) Set(token, conversationID, responseID string) {
	ct.mu.Lock()
	defer ct.mu.Unlock()
	ct.conv[token] = &ConversationState{
		ConversationID: conversationID,
		LastResponseID: responseID,
		CreatedAt:      time.Now(),
	}
}

// Clear removes the conversation state for a token (e.g. on error).
func (ct *ConversationTracker) Clear(token string) {
	ct.mu.Lock()
	defer ct.mu.Unlock()
	delete(ct.conv, token)
}

func (ct *ConversationTracker) evictStale() {
	ct.mu.Lock()
	defer ct.mu.Unlock()
	for k, s := range ct.conv {
		if time.Since(s.CreatedAt) > ct.ttl {
			delete(ct.conv, k)
		}
	}
}
