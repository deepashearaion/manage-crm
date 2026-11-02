package apitest

import (
	"net/http"
	"sync"
	"testing"
)

// Requests are served concurrently in production, so the database handle
// shared by the handlers must be safe for concurrent use.
func TestConcurrentRequests(t *testing.T) {
	s := newServer(t)
	_, token := s.userToken()
	createContact(t, s, token, map[string]any{"first_name": "Ravi"})

	const n = 20
	codes := make(chan int, n)
	var wg sync.WaitGroup
	for range n {
		wg.Add(1)
		go func() {
			defer wg.Done()
			codes <- s.do("GET", "/api/contacts", nil, token).Code
		}()
	}
	wg.Wait()
	close(codes)

	for code := range codes {
		if code != http.StatusOK {
			t.Errorf("concurrent GET /api/contacts = %d, want 200", code)
		}
	}
}
