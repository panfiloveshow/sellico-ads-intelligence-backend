package wb

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

func (c *Client) WithAdGuard(guard adguard.Guard) *Client { c.adGuard = guard; return c }

func (c *Client) doAdvertisingRequest(ctx context.Context, method, path, token string, body io.Reader) (*http.Response, []byte, error) {
	if !adguard.IsWBMutation(method, path) {
		return c.doRequestInner(ctx, method, path, token, body)
	}
	if adguard.ApprovedCommand(ctx) != uuid.Nil && c.adGuard == nil {
		return nil, nil, adguard.BeforeSendError(errors.New("approved advertising command requires its native journal"))
	}
	var payload []byte
	var err error
	if body != nil {
		payload, err = io.ReadAll(io.LimitReader(body, 8*1024*1024+1))
		if err != nil || len(payload) > 8*1024*1024 {
			return nil, nil, adguard.BeforeSendError(errors.New("advertising request is unreadable or oversized"))
		}
	}
	if err = c.limiterForToken(token).Wait(ctx); err != nil {
		return nil, nil, adguard.BeforeSendError(err)
	}
	policy, _ := ctx.Value(requestRetryPolicyKey{}).(requestRetryPolicy)
	policy.oneAttempt = true
	policy.limiterAcquired = true
	policy.retryRateLimit = false
	policy.noRedirects = true
	policy.maxResponseBytes = 2 * 1024 * 1024
	ctx = context.WithValue(ctx, requestRetryPolicyKey{}, policy)
	var lease adguard.Lease
	if c.adGuard != nil {
		request, e := adguard.NewWBRequest(method, c.baseURL+path, token, payload)
		if e != nil {
			return nil, nil, adguard.BeforeSendError(e)
		}
		lease, err = c.adGuard.BeginAdvertisingMutation(ctx, request)
		if err != nil {
			return nil, nil, adguard.BeforeSendError(err)
		}
	}
	resp, raw, sendErr := c.doRequestInner(ctx, method, path, token, bytes.NewReader(payload))
	status := 0
	if resp != nil {
		status = resp.StatusCode
	}
	if sendErr == nil && (status < 200 || status >= 300) {
		sendErr = fmt.Errorf("WB advertising returned an unverified response (%d)", status)
	}
	if c.adGuard != nil {
		persist, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
		defer cancel()
		if e := c.adGuard.CompleteAdvertisingMutation(persist, lease, adguard.Result(status, raw, sendErr)); e != nil {
			return resp, raw, &adguard.MutationError{Err: errors.Join(sendErr, errors.New("advertising transport receipt unavailable")), OutcomeUnknown: true}
		}
	}
	return resp, raw, adguard.AfterSendError(sendErr, status)
}
