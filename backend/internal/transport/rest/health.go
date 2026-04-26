package rest

import (
	"context"
	"net/http"
	"time"

	"github.com/danielgtaylor/huma/v2"
)

// readinessChecker is the consumer-side port for /readyz. The transport
// only needs "is the process able to serve traffic" — typically a DB
// ping — and intentionally has no idea how that check is implemented.
type readinessChecker interface {
	Ping(ctx context.Context) error
}

type healthOutput struct {
	Body struct {
		Status string `json:"status" example:"ok"`
		Time   string `json:"time" example:"2026-04-25T20:00:00Z"`
	}
}

type readyOutput struct {
	Body struct {
		Status string `json:"status" example:"ok"`
		Time   string `json:"time" example:"2026-04-25T20:00:00Z"`
	}
}

// RegisterHealth wires GET /healthz — pure liveness. Returns 200 as long
// as the process is responding; deliberately does NOT check downstream
// dependencies (that's /readyz). Orchestrators use this to decide
// whether to restart the pod.
func RegisterHealth(api huma.API) {
	huma.Register(api, huma.Operation{
		OperationID: "healthz",
		Method:      http.MethodGet,
		Path:        "/healthz",
		Summary:     "Liveness probe",
		Tags:        []string{"system"},
	}, func(_ context.Context, _ *struct{}) (*healthOutput, error) {
		out := &healthOutput{}
		out.Body.Status = "ok"
		out.Body.Time = time.Now().UTC().Format(time.RFC3339)
		return out, nil
	})
}

// RegisterReady wires GET /readyz — readiness. Pings the database; if
// the DB is unreachable we return 503 so the load balancer drains
// traffic away from this instance until it recovers. Bounded by a 2s
// timeout so a slow DB never holds the probe open.
func RegisterReady(api huma.API, checker readinessChecker) {
	huma.Register(api, huma.Operation{
		OperationID: "readyz",
		Method:      http.MethodGet,
		Path:        "/readyz",
		Summary:     "Readiness probe",
		Tags:        []string{"system"},
	}, func(ctx context.Context, _ *struct{}) (*readyOutput, error) {
		pingCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
		defer cancel()
		if err := checker.Ping(pingCtx); err != nil {
			return nil, huma.Error503ServiceUnavailable("database not ready", err)
		}
		out := &readyOutput{}
		out.Body.Status = "ok"
		out.Body.Time = time.Now().UTC().Format(time.RFC3339)
		return out, nil
	})
}
