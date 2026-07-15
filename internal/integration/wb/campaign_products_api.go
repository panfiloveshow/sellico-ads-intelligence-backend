package wb

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"

	"github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/pkg/apperror"
)

const maxCampaignProductUpdateItems = 20

// CampaignProductUpdatesRequest is the official payload for
// PATCH /adv/v0/auction/nms. WB limits the outer nms list to 20 campaigns.
type CampaignProductUpdatesRequest struct {
	NMs []CampaignProductUpdate `json:"nms"`
}

type CampaignProductUpdate struct {
	AdvertID int64                    `json:"advert_id"`
	NMs      CampaignProductNMChanges `json:"nms"`
}

type CampaignProductNMChanges struct {
	Add    []int64 `json:"add"`
	Delete []int64 `json:"delete"`
}

type CampaignProductUpdatesResponse struct {
	NMs []CampaignProductUpdateResult `json:"nms"`
}

type CampaignProductUpdateResult struct {
	AdvertID int64                         `json:"advert_id"`
	NMs      CampaignProductNMChangeResult `json:"nms"`
}

type CampaignProductNMChangeResult struct {
	Added   []int64 `json:"added"`
	Deleted []int64 `json:"deleted"`
}

// CampaignProductUpdateError distinguishes a WB rejection from an ambiguous
// write outcome. Transport failures, 5xx responses, and malformed 2xx bodies
// may happen after WB applied the mutation; callers must sync instead of
// recording those cases as definitively failed.
type CampaignProductUpdateError struct {
	OutcomeUnknown bool
	Err            error
}

func (e *CampaignProductUpdateError) Error() string { return e.Err.Error() }
func (e *CampaignProductUpdateError) Unwrap() error { return e.Err }

func CampaignProductUpdateOutcomeUnknown(err error) bool {
	var updateErr *CampaignProductUpdateError
	return errors.As(err, &updateErr) && updateErr.OutcomeUnknown
}

func unknownCampaignProductResponseError(err error) error {
	mapped := apperror.New(apperror.ErrWBAPIError, "invalid WB campaign product update response")
	return &CampaignProductUpdateError{
		OutcomeUnknown: true,
		Err:            fmt.Errorf("%w: %v", mapped, err),
	}
}

// UpdateCampaignProducts adds and/or removes real WB product cards from up to
// 20 campaigns. Input is normalized before any HTTP request: duplicate NMIDs
// are removed and an NMID present in both add and delete is rejected.
func (c *Client) UpdateCampaignProducts(ctx context.Context, token string, request CampaignProductUpdatesRequest) (CampaignProductUpdatesResponse, error) {
	normalized, err := NormalizeCampaignProductUpdatesRequest(request)
	if err != nil {
		return CampaignProductUpdatesResponse{}, err
	}
	body, err := json.Marshal(normalized)
	if err != nil {
		return CampaignProductUpdatesResponse{}, fmt.Errorf("marshal campaign product updates request: %w", err)
	}

	// This endpoint has a stricter official bucket than the general advertising
	// API client. The request still passes through doRequest for its shared
	// circuit breaker, retry-after handling, metrics, and error mapping.
	if err := c.campaignProductLimiterForToken(token).Wait(ctx); err != nil {
		return CampaignProductUpdatesResponse{}, fmt.Errorf("campaign products rate limiter wait: %w", err)
	}
	responseHTTP, responseBody, err := c.doRequest(ctx, http.MethodPatch, "/adv/v0/auction/nms", token, bytes.NewReader(body))
	if err != nil {
		outcomeUnknown := responseHTTP == nil || responseHTTP.StatusCode >= http.StatusInternalServerError
		var apiErr *APIError
		if errors.As(err, &apiErr) && apiErr.StatusCode == http.StatusTooManyRequests {
			outcomeUnknown = false
		}
		return CampaignProductUpdatesResponse{}, &CampaignProductUpdateError{OutcomeUnknown: outcomeUnknown, Err: err}
	}

	var response CampaignProductUpdatesResponse
	if err := json.Unmarshal(responseBody, &response); err != nil {
		return CampaignProductUpdatesResponse{}, unknownCampaignProductResponseError(fmt.Errorf("unmarshal campaign product updates response: %w", err))
	}
	results := make(map[int64]struct{}, len(response.NMs))
	for _, item := range response.NMs {
		if item.AdvertID <= 0 {
			return CampaignProductUpdatesResponse{}, unknownCampaignProductResponseError(errors.New("campaign product updates response contains invalid advert_id"))
		}
		if _, duplicate := results[item.AdvertID]; duplicate {
			return CampaignProductUpdatesResponse{}, unknownCampaignProductResponseError(fmt.Errorf("campaign product updates response contains duplicate advert_id %d", item.AdvertID))
		}
		results[item.AdvertID] = struct{}{}
	}
	for _, requested := range normalized.NMs {
		if _, found := results[requested.AdvertID]; !found {
			return CampaignProductUpdatesResponse{}, unknownCampaignProductResponseError(fmt.Errorf("campaign product updates response missing advert_id %d", requested.AdvertID))
		}
	}
	return response, nil
}

// NormalizeCampaignProductUpdatesRequest validates and canonicalizes a product
// mutation. Services may call it before resolving credentials so invalid input
// is consistently mapped to a client validation error without touching WB.
func NormalizeCampaignProductUpdatesRequest(request CampaignProductUpdatesRequest) (CampaignProductUpdatesRequest, error) {
	if len(request.NMs) == 0 {
		return CampaignProductUpdatesRequest{}, errors.New("at least one campaign product update is required")
	}
	if len(request.NMs) > maxCampaignProductUpdateItems {
		return CampaignProductUpdatesRequest{}, fmt.Errorf("campaign product update supports at most %d campaigns per request", maxCampaignProductUpdateItems)
	}

	seenCampaigns := make(map[int64]struct{}, len(request.NMs))
	normalized := CampaignProductUpdatesRequest{NMs: make([]CampaignProductUpdate, 0, len(request.NMs))}
	for _, item := range request.NMs {
		if item.AdvertID <= 0 {
			return CampaignProductUpdatesRequest{}, errors.New("advert_id must be positive")
		}
		if _, exists := seenCampaigns[item.AdvertID]; exists {
			return CampaignProductUpdatesRequest{}, fmt.Errorf("duplicate advert_id %d in campaign product update", item.AdvertID)
		}
		seenCampaigns[item.AdvertID] = struct{}{}

		add, addSet, err := normalizePositiveNMIDs(item.NMs.Add, "add")
		if err != nil {
			return CampaignProductUpdatesRequest{}, err
		}
		remove, _, err := normalizePositiveNMIDs(item.NMs.Delete, "delete")
		if err != nil {
			return CampaignProductUpdatesRequest{}, err
		}
		for _, nmID := range remove {
			if _, conflict := addSet[nmID]; conflict {
				return CampaignProductUpdatesRequest{}, fmt.Errorf("nm_id %d cannot be both added and deleted", nmID)
			}
		}
		if len(add) == 0 && len(remove) == 0 {
			return CampaignProductUpdatesRequest{}, fmt.Errorf("campaign %d has no product changes", item.AdvertID)
		}

		normalized.NMs = append(normalized.NMs, CampaignProductUpdate{
			AdvertID: item.AdvertID,
			NMs: CampaignProductNMChanges{
				Add:    add,
				Delete: remove,
			},
		})
	}
	return normalized, nil
}

func normalizePositiveNMIDs(values []int64, field string) ([]int64, map[int64]struct{}, error) {
	seen := make(map[int64]struct{}, len(values))
	result := make([]int64, 0, len(values))
	for _, nmID := range values {
		if nmID <= 0 {
			return nil, nil, fmt.Errorf("%s must contain only positive nm_ids", field)
		}
		if _, exists := seen[nmID]; exists {
			continue
		}
		seen[nmID] = struct{}{}
		result = append(result, nmID)
	}
	return result, seen, nil
}
