package actions

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
)

// stubCP records every action result the agent POSTs, so a test can assert what
// was reported and WHEN (running vs terminal) — the durable-snapshot path.
type stubCP struct {
	*httptest.Server
	mu      sync.Mutex
	reports []reportBody
}

type reportBody struct {
	Status    string          `json:"status"`
	Result    string          `json:"result"`
	Progress  string          `json:"progress"`
	Revert    json.RawMessage `json:"revert"`
	Snapshots json.RawMessage `json:"snapshots"`
	Steps     json.RawMessage `json:"steps"`
}

func newStubCP() *stubCP {
	s := &stubCP{}
	s.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var b reportBody
		body, _ := io.ReadAll(io.LimitReader(r.Body, 1<<20))
		_ = json.Unmarshal(body, &b)
		s.mu.Lock()
		s.reports = append(s.reports, b)
		s.mu.Unlock()
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"cancel":false}`))
	}))
	return s
}

func (s *stubCP) snapshot() []reportBody {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]reportBody(nil), s.reports...)
}
