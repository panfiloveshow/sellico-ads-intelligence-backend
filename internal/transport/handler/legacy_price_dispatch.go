package handler

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/domain"
	sqlcgen "github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/repository/sqlc"
	"github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/service"
)

type legacyPriceDispatchAuthorizer interface {
	Authorize(context.Context, domain.LegacyPriceDispatchRequest) (domain.LegacyPriceDispatchGrant, error)
}

type LegacyPriceDispatchHandler struct {
	service legacyPriceDispatchAuthorizer
	secret  string
}

func NewLegacyPriceDispatchHandler(s legacyPriceDispatchAuthorizer, secret string) *LegacyPriceDispatchHandler {
	return &LegacyPriceDispatchHandler{service: s, secret: secret}
}

func (h *LegacyPriceDispatchHandler) Authorize(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Type", "application/json")
	deny := func(status int, code string) {
		w.WriteHeader(status)
		_ = json.NewEncoder(w).Encode(map[string]any{"data": nil, "error": code})
	}
	if len(h.secret) < 32 || h.service == nil {
		deny(http.StatusServiceUnavailable, "price_dispatch_unavailable")
		return
	}
	if r.Method != http.MethodPost || r.URL.RawQuery != "" || strings.Split(r.Header.Get("Content-Type"), ";")[0] != "application/json" {
		deny(http.StatusBadRequest, "invalid_price_dispatch_request")
		return
	}
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 8192))
	if err != nil {
		deny(http.StatusBadRequest, "invalid_price_dispatch_request")
		return
	}
	mac := hmac.New(sha256.New, []byte(h.secret))
	_, _ = mac.Write([]byte("legacy-price-dispatch-v1\n"))
	_, _ = mac.Write(body)
	expected := "v1=" + hex.EncodeToString(mac.Sum(nil))
	if !hmac.Equal([]byte(r.Header.Get("X-Sellico-Price-Authorization")), []byte(expected)) {
		deny(http.StatusUnauthorized, "invalid_price_dispatch_signature")
		return
	}
	d := json.NewDecoder(strings.NewReader(string(body)))
	d.DisallowUnknownFields()
	var request domain.LegacyPriceDispatchRequest
	if d.Decode(&request) != nil || d.Decode(new(any)) != io.EOF {
		deny(http.StatusBadRequest, "invalid_price_dispatch_request")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
	defer cancel()
	grant, err := h.service.Authorize(ctx, request)
	if err != nil {
		switch {
		case errors.Is(err, service.ErrInvalidLegacyPriceDispatch):
			deny(http.StatusUnprocessableEntity, "invalid_price_dispatch_request")
		case errors.Is(err, service.ErrLegacyPriceDispatchRetired):
			deny(http.StatusConflict, "price_dispatch_retired")
		case errors.Is(err, sqlcgen.ErrPriceExecutionStopped):
			deny(http.StatusConflict, "price_execution_stopped")
		case errors.Is(err, sqlcgen.ErrLegacyPriceActiveWrite):
			deny(http.StatusConflict, "price_dispatch_blocked_by_active_write")
		case errors.Is(err, sqlcgen.ErrLegacyPriceBindingUnknown):
			deny(http.StatusConflict, "price_dispatch_binding_unknown")
		case errors.Is(err, sqlcgen.ErrPriceExecutionAccountHeld), errors.Is(err, sqlcgen.ErrLegacyPriceDispatchReplay):
			deny(http.StatusConflict, "price_dispatch_not_available")
		default:
			deny(http.StatusServiceUnavailable, "price_dispatch_unavailable")
		}
		return
	}
	_ = json.NewEncoder(w).Encode(map[string]any{"data": grant})
}
