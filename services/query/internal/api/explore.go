package api

import (
	"errors"
	"net/http"

	"github.com/rocketplaneio/rocketplane/services/query/internal/store"
)

// GET /api/v1/services — RED-Health je Service über das Fenster.
func (s *Server) handleServices(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	start, _ := parseTime(q.Get("start"))
	end, _ := parseTime(q.Get("end"))
	res, err := s.store.Services(r.Context(), store.ServicesQuery{
		Start: start,
		End:   end,
		Step:  parseDurationSeconds(q.Get("step")),
		Env:   q.Get("env"),
		Sort:  q.Get("sort"),
	})
	if err != nil {
		s.storeError(w, err)
		return
	}
	writeSuccess(w, res)
}

// GET /api/v1/services/{name} — RED-Zeitreihen, Operationen & Abhängigkeiten.
func (s *Server) handleServiceDetail(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	if name == "" {
		writeError(w, http.StatusBadRequest, "bad_data", "service name required")
		return
	}
	q := r.URL.Query()
	start, _ := parseTime(q.Get("start"))
	end, _ := parseTime(q.Get("end"))
	res, err := s.store.Service(r.Context(), store.ServiceQuery{
		Name:  name,
		Start: start,
		End:   end,
		Step:  parseDurationSeconds(q.Get("step")),
	})
	if err != nil {
		s.storeError(w, err)
		return
	}
	writeSuccess(w, res)
}

// GET /api/v1/logs — gefilterte Log-Liste (cursor-paginiert, neueste zuerst).
func (s *Server) handleLogs(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	start, _ := parseTime(q.Get("start"))
	end, _ := parseTime(q.Get("end"))
	traceID := q.Get("traceId")
	if traceID != "" && !isHex(traceID, 32) {
		writeError(w, http.StatusBadRequest, "bad_data", "traceId must be 32 hex characters")
		return
	}
	res, err := s.store.Logs(r.Context(), store.LogsQuery{
		Service:     q.Get("service"),
		Start:       start,
		End:         end,
		MinSeverity: int32(parseInt(q.Get("minSeverity"))),
		Search:      q.Get("search"),
		TraceID:     traceID,
		Limit:       parseInt(q.Get("limit")),
		Cursor:      q.Get("cursor"),
	})
	if err != nil {
		s.storeError(w, err)
		return
	}
	writeSuccess(w, res)
}

// GET /api/v1/traces — kompakte Trace-Liste (gefiltert, cursor-paginiert).
func (s *Server) handleTraces(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	start, _ := parseTime(q.Get("start"))
	end, _ := parseTime(q.Get("end"))
	res, err := s.store.Traces(r.Context(), store.TracesQuery{
		Service:       q.Get("service"),
		Start:         start,
		End:           end,
		Status:        q.Get("status"),
		MinDurationMs: parseFloat(q.Get("minDurationMs")),
		MaxDurationMs: parseFloat(q.Get("maxDurationMs")),
		Limit:         parseInt(q.Get("limit")),
		Cursor:        q.Get("cursor"),
		Sort:          q.Get("sort"),
	})
	if err != nil {
		s.storeError(w, err)
		return
	}
	writeSuccess(w, res)
}

// GET /api/v1/traces/{traceId} — voller Trace für das Waterfall.
func (s *Server) handleTrace(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("traceId")
	if !isHex(id, 32) {
		writeError(w, http.StatusBadRequest, "bad_data", "traceId must be 32 hex characters")
		return
	}
	detail, err := s.store.Trace(r.Context(), id)
	if errors.Is(err, store.ErrNotFound) {
		writeError(w, http.StatusNotFound, "not_found", "trace not found")
		return
	}
	if err != nil {
		s.storeError(w, err)
		return
	}
	writeSuccess(w, detail)
}

// storeError bildet Store-Fehler auf HTTP ab (default 500 internal).
func (s *Server) storeError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, store.ErrNotFound):
		writeError(w, http.StatusNotFound, "not_found", err.Error())
	case errors.Is(err, store.ErrUnsupported):
		writeError(w, http.StatusNotImplemented, "not_implemented", err.Error())
	default:
		s.log.Error("store error", "err", err)
		writeError(w, http.StatusInternalServerError, "internal", "internal error")
	}
}

func isHex(s string, n int) bool {
	if len(s) != n {
		return false
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		if !((c >= '0' && c <= '9') || (c >= 'a' && c <= 'f') || (c >= 'A' && c <= 'F')) {
			return false
		}
	}
	return true
}
