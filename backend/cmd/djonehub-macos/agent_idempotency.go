package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"net/http"
	"regexp"
	"sync"
	"time"
)

const agentIdempotencyTTL = 24 * time.Hour

var idempotencyKeyPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:-]{7,127}$`)

type idempotencyEntry struct {
	bodyHash string
	created  time.Time
	done     chan struct{}
	status   int
	header   http.Header
	body     []byte
}

type idempotencyStore struct {
	mu      sync.Mutex
	entries map[string]*idempotencyEntry
}

func newIdempotencyStore() *idempotencyStore {
	return &idempotencyStore{entries: make(map[string]*idempotencyEntry)}
}

var agentIdempotency = newIdempotencyStore()

var idempotentCLIPaths = map[string]bool{
	"POST /api/sms/send":    true,
	"POST /api/call/dial":   true,
	"POST /api/call/answer": true,
	"POST /api/call/hangup": true,
	"POST /api/esim/switch": true,
}

func (a *app) agentIdempotencyMiddleware(next http.Handler) http.Handler {
	return agentIdempotency.middleware(next)
}

func (s *idempotencyStore) middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		principal := principalFromRequest(r)
		if principal.Kind != "cli" || !idempotentCLIPaths[r.Method+" "+r.URL.Path] {
			next.ServeHTTP(w, r)
			return
		}

		requestID := r.Header.Get("Idempotency-Key")
		if !idempotencyKeyPattern.MatchString(requestID) {
			writeCodedError(w, http.StatusBadRequest, "idempotency_key_required",
				"此操作需要 8–128 位的 Idempotency-Key，重试同一操作时必须复用", false)
			return
		}
		body, err := io.ReadAll(io.LimitReader(r.Body, 1<<20+1))
		if err != nil || len(body) > 1<<20 {
			writeCodedError(w, http.StatusBadRequest, "request_body_invalid", "请求体无法读取或超过 1 MiB", false)
			return
		}
		r.Body = io.NopCloser(bytes.NewReader(body))
		hash := sha256.Sum256(body)
		bodyHash := hex.EncodeToString(hash[:])
		storeKey := principal.ID + "\x00" + r.Method + "\x00" + r.URL.Path + "\x00" + requestID

		entry, owner, conflict := s.acquire(storeKey, bodyHash, time.Now())
		if conflict {
			writeCodedError(w, http.StatusConflict, "idempotency_key_reused",
				"同一个 Idempotency-Key 不能用于不同的请求内容", false)
			return
		}
		if !owner {
			select {
			case <-entry.done:
				replayBufferedResponse(w, entry, true)
			case <-r.Context().Done():
				writeCodedError(w, http.StatusRequestTimeout, "idempotency_wait_cancelled",
					"原请求仍在执行，请使用同一 request ID 查询或重试", true)
			}
			return
		}

		completed := false
		defer func() {
			if !completed {
				s.abandon(storeKey, entry)
			}
		}()
		buffer := newBufferedResponseWriter()
		next.ServeHTTP(buffer, r)
		s.complete(entry, buffer)
		completed = true
		replayBufferedResponse(w, entry, false)
	})
}

func (s *idempotencyStore) acquire(key, bodyHash string, now time.Time) (*idempotencyEntry, bool, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for existingKey, entry := range s.entries {
		select {
		case <-entry.done:
			if now.Sub(entry.created) > agentIdempotencyTTL {
				delete(s.entries, existingKey)
			}
		default:
		}
	}
	if entry, ok := s.entries[key]; ok {
		return entry, false, entry.bodyHash != bodyHash
	}
	entry := &idempotencyEntry{bodyHash: bodyHash, created: now, done: make(chan struct{})}
	s.entries[key] = entry
	return entry, true, false
}

func (s *idempotencyStore) complete(entry *idempotencyEntry, response *bufferedResponseWriter) {
	entry.status = response.status
	if entry.status == 0 {
		entry.status = http.StatusOK
	}
	entry.header = response.header.Clone()
	entry.body = append([]byte(nil), response.body.Bytes()...)
	close(entry.done)
}

func (s *idempotencyStore) abandon(key string, entry *idempotencyEntry) {
	s.mu.Lock()
	if s.entries[key] == entry {
		delete(s.entries, key)
	}
	s.mu.Unlock()
	close(entry.done)
}

type bufferedResponseWriter struct {
	header http.Header
	status int
	body   bytes.Buffer
}

func newBufferedResponseWriter() *bufferedResponseWriter {
	return &bufferedResponseWriter{header: make(http.Header)}
}

func (w *bufferedResponseWriter) Header() http.Header { return w.header }

func (w *bufferedResponseWriter) WriteHeader(status int) {
	if w.status == 0 {
		w.status = status
	}
}

func (w *bufferedResponseWriter) Write(data []byte) (int, error) {
	if w.status == 0 {
		w.status = http.StatusOK
	}
	return w.body.Write(data)
}

func replayBufferedResponse(w http.ResponseWriter, entry *idempotencyEntry, replayed bool) {
	for key, values := range entry.header {
		for _, value := range values {
			w.Header().Add(key, value)
		}
	}
	if replayed {
		w.Header().Set("Idempotency-Replayed", "true")
	} else {
		w.Header().Set("Idempotency-Replayed", "false")
	}
	w.WriteHeader(entry.status)
	_, _ = w.Write(entry.body)
}
