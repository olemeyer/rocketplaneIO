// Package enroll performs the one-time agent enrollment against the control-plane.
//
// Flow (see docs/architecture.md §6):
//
//	POST {baseURL}/api/agent/enroll
//	  {enrollToken, k8sUid, clusterName, agentVersion}
//	-> {clusterId, agentToken}
//
// The call is retried with capped exponential backoff until it succeeds or the
// context is cancelled — the agent Pod may start before the control-plane is
// reachable, and enrollment is a prerequisite for everything else.
package enroll

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"
	"time"
)

// Request is the enroll payload sent to the control-plane.
type Request struct {
	EnrollToken  string `json:"enrollToken"`
	K8sUID       string `json:"k8sUid"`
	ClusterName  string `json:"clusterName"`
	AgentVersion string `json:"agentVersion"`
}

// Response is the control-plane's reply on successful enrollment.
type Response struct {
	ClusterID  string `json:"clusterId"`
	AgentToken string `json:"agentToken"`
}

const (
	initialBackoff = 2 * time.Second
	maxBackoff     = 60 * time.Second
	requestTimeout = 15 * time.Second
)

// Enroll POSTs the enroll request, retrying with backoff until it succeeds or
// ctx is done. baseURL is the configured control-plane root (trailing slash
// tolerated). If baseURL is not reachable from the pod, Enroll auto-discovers a
// reachable control-plane among the host aliases (see resolve.go) so a local
// trial "just works". It returns the base URL that actually worked — the caller
// MUST use it for all further calls (sync, actions), since it may differ from
// the configured one.
func Enroll(ctx context.Context, baseURL string, req Request) (*Response, string, error) {
	candidates := candidateURLs(baseURL)
	if len(candidates) > 1 {
		log.Printf("enroll: control-plane candidates: %s", strings.Join(candidates, ", "))
	}
	client := &http.Client{Timeout: requestTimeout}
	backoff := initialBackoff

	for attempt := 1; ; attempt++ {
		// Pick a reachable base via a fast /healthz probe (falls back to the
		// configured URL if nothing answers yet — the control-plane may still be
		// starting, so we keep retrying).
		base := pickReachable(ctx, candidates)
		if base == "" {
			base = candidates[0]
		}

		resp, err := doEnroll(ctx, client, strings.TrimRight(base, "/")+"/api/agent/enroll", req)
		if err == nil {
			if base != baseURL {
				log.Printf("enroll: reached control-plane at %s (configured %s was not reachable from the pod)", base, baseURL)
			}
			log.Printf("enroll: success (clusterId=%s)", resp.ClusterID)
			return resp, base, nil
		}

		if ctx.Err() != nil {
			return nil, "", ctx.Err()
		}

		log.Printf("enroll: attempt %d failed (via %s): %v — retrying in %s", attempt, base, err, backoff)
		select {
		case <-ctx.Done():
			return nil, "", ctx.Err()
		case <-time.After(backoff):
		}

		if backoff < maxBackoff {
			backoff *= 2
			if backoff > maxBackoff {
				backoff = maxBackoff
			}
		}
	}
}

func doEnroll(ctx context.Context, client *http.Client, url string, req Request) (*Response, error) {
	body, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("marshal request: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("build request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")

	resp, err := client.Do(httpReq)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	payload, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<16))
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("control-plane returned %d: %s", resp.StatusCode, strings.TrimSpace(string(payload)))
	}

	var out Response
	if err := json.Unmarshal(payload, &out); err != nil {
		return nil, fmt.Errorf("decode response: %w", err)
	}
	if out.AgentToken == "" || out.ClusterID == "" {
		return nil, fmt.Errorf("control-plane returned incomplete enroll response")
	}
	return &out, nil
}
