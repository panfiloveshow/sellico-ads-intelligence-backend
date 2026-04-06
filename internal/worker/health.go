package worker

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"github.com/rs/zerolog"
)

// HealthServer exposes a minimal HTTP health endpoint for the worker process.
type HealthServer struct {
	server *http.Server
	logger zerolog.Logger
}

// NewHealthServer creates a health check server on the given port.
func NewHealthServer(port int, logger zerolog.Logger) *HealthServer {
	mux := http.NewServeMux()
	mux.HandleFunc("/health/live", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"status":"ok"}`))
	})

	return &HealthServer{
		server: &http.Server{
			Addr:              fmt.Sprintf(":%d", port),
			Handler:           mux,
			ReadHeaderTimeout: 3 * time.Second,
		},
		logger: logger,
	}
}

// Start begins listening in a goroutine.
func (h *HealthServer) Start() {
	go func() {
		h.logger.Info().Str("addr", h.server.Addr).Msg("worker health server started")
		if err := h.server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			h.logger.Error().Err(err).Msg("worker health server error")
		}
	}()
}

// Shutdown gracefully stops the health server.
func (h *HealthServer) Shutdown() {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	h.server.Shutdown(ctx)
}
