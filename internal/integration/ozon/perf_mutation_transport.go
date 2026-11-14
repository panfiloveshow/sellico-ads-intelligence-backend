package ozon

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"github.com/google/uuid"
	"io"
	"net/http"
	"time"

	"github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/pkg/adguard"
)

func (c *PerfClient) WithAdGuard(guard adguard.Guard) *PerfClient { c.adGuard = guard; return c }

func (c *PerfClient) sendAdvertisingMutation(ctx context.Context, creds Credentials, method, path, endpoint string, payload []byte) ([]byte, error) {
	if adguard.ApprovedCommand(ctx) != uuid.Nil && c.adGuard == nil {
		return nil, adguard.BeforeSendError(errors.New("approved advertising command requires its native journal"))
	}
	token, err := c.token(ctx, creds)
	if err != nil {
		return nil, adguard.BeforeSendError(err)
	}
	if err = c.limiters.get(creds.PerfClientID).Wait(ctx); err != nil {
		return nil, adguard.BeforeSendError(err)
	}
	req, err := http.NewRequestWithContext(ctx, method, endpoint, bytes.NewReader(payload))
	if err != nil {
		return nil, adguard.BeforeSendError(err)
	}
	// net/http may retry a GET on a reused connection. These GET actions
	// change advertising state, so make their empty body non-replayable too.
	if method == http.MethodGet && len(payload) == 0 {
		req.Body = io.NopCloser(bytes.NewReader(nil))
		req.GetBody = nil
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Accept", "application/json")
	if payload != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	var lease adguard.Lease
	if c.adGuard != nil {
		r, err := adguard.NewOzonRequest(method, endpoint, creds.PerfClientID, payload)
		if err != nil {
			return nil, adguard.BeforeSendError(err)
		}
		lease, err = c.adGuard.BeginAdvertisingMutation(ctx, r)
		if err != nil {
			return nil, adguard.BeforeSendError(err)
		}
	}
	client := *c.httpClient
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	resp, sendErr := client.Do(req)
	status := 0
	var raw []byte
	if resp != nil {
		status = resp.StatusCode
		var readErr error
		raw, readErr = io.ReadAll(io.LimitReader(resp.Body, 2*1024*1024+1))
		resp.Body.Close()
		if readErr != nil || len(raw) > 2*1024*1024 {
			sendErr = errors.New("Ozon advertising response unreadable or oversized")
			status = 0
		}
	}
	if sendErr == nil && (status < 200 || status >= 300) {
		sendErr = &APIError{StatusCode: status, URL: endpoint, Message: fmt.Sprintf("ozon advertising: unverified response (%d) on %s", status, path)}
		if status == 401 {
			c.tokenMu.Lock()
			delete(c.tokens, creds.PerfClientID)
			c.tokenMu.Unlock()
		}
		if status == 429 && perfQuotaCategory(ctx) != "" {
			until := time.Now().Truncate(time.Hour).Add(time.Hour)
			if resp.Header.Get("Retry-After") != "" {
				until = time.Now().Add(parseRetryAfter(resp.Header.Get("Retry-After")))
			}
			sendErr = c.quota.block(creds.PerfClientID, perfQuotaCategory(ctx), until)
		}
	}
	if c.adGuard != nil {
		persist, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
		defer cancel()
		if err = c.adGuard.CompleteAdvertisingMutation(persist, lease, adguard.Result(status, raw, sendErr)); err != nil {
			return nil, &adguard.MutationError{Err: errors.Join(sendErr, errors.New("advertising transport receipt unavailable")), OutcomeUnknown: true}
		}
	}
	return raw, adguard.AfterSendError(sendErr, status)
}
