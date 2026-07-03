package service

import (
	"encoding/json"
	"math"
	"strconv"
	"strings"
)

const (
	maxDerivedBidSnapshotsPerCapture      = 20
	maxDerivedPositionSnapshotsPerCapture = 20
	maxDerivedUISignalsPerCapture         = 10
)

type extensionCampaignBudgetSnapshot struct {
	cash    int64
	netting int64
	total   int64
}

var extensionBidEndpointKeys = map[string]struct{}{
	"wb.bid.estimate":        {},
	"wb.bid.min":             {},
	"wb.bid.product":         {},
	"wb.bid.recommendations": {},
	"wb.query.bids":          {},
}

var extensionPositionEndpointKeys = map[string]struct{}{
	"wb.positions":     {},
	"wb.serp.snapshot": {},
}

type extensionBidMetrics struct {
	visibleBid     *int64
	recommendedBid *int64
	competitiveBid *int64
	leadershipBid  *int64
	cpmMin         *int64
}

type extensionPositionMetrics struct {
	visiblePosition int
	visiblePage     *int
	pageSubtype     string
}

func deriveBidSnapshotsFromNetworkCapture(input CreateExtensionNetworkCaptureInput, payload json.RawMessage) []CreateExtensionBidSnapshotInput {
	endpointKey := strings.TrimSpace(strings.ToLower(input.EndpointKey))
	if _, ok := extensionBidEndpointKeys[endpointKey]; !ok {
		return nil
	}
	if len(payload) == 0 {
		return nil
	}

	var root any
	if err := json.Unmarshal(payload, &root); err != nil {
		return nil
	}

	base := newExtensionNetworkCaptureBase(input, root, endpointKey)

	candidates := collectNetworkBidCandidates(root)
	if len(candidates) == 0 {
		return nil
	}

	seen := make(map[string]struct{})
	out := make([]CreateExtensionBidSnapshotInput, 0, len(candidates))
	for _, candidate := range candidates {
		metrics := networkBidMetrics(candidate)
		if metrics.empty() {
			continue
		}

		query := textFromMap(candidate, "norm_query", "normQuery", "query", "keyword", "phrase", "searchText", "text")
		if query == "" && len(base.normQueries) == 1 {
			query = base.normQueries[0]
		}
		if query == "" {
			query = base.fallbackQuery
		}
		region := textFromMap(candidate, "region", "geo", "geo_name", "regionName")
		if region == "" {
			region = base.fallbackRegion
		}
		wbCampaignID := int64FromMap(candidate, "advert_id", "advertId", "campaign_id", "campaignId", "wb_campaign_id", "wbCampaignID")
		if wbCampaignID <= 0 {
			wbCampaignID = base.wbCampaignID
		}
		wbProductID := int64FromMap(candidate, "nm_id", "nmId", "wb_product_id", "wbProductID", "product_id", "productId", "nm")
		if wbProductID <= 0 {
			wbProductID = base.wbProductID
		}
		if query == "" && wbCampaignID <= 0 {
			continue
		}

		key := strings.Join([]string{
			query,
			region,
			strconv.FormatInt(wbCampaignID, 10),
			strconv.FormatInt(wbProductID, 10),
			int64Key(metrics.visibleBid),
			int64Key(metrics.recommendedBid),
			int64Key(metrics.competitiveBid),
			int64Key(metrics.leadershipBid),
			int64Key(metrics.cpmMin),
		}, "|")
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}

		metadata := derivedBidMetadata(base, wbCampaignID, wbProductID, candidate)
		out = append(out, CreateExtensionBidSnapshotInput{
			SellerCabinetID: input.SellerCabinetID,
			CampaignID:      input.CampaignID,
			PhraseID:        input.PhraseID,
			Query:           stringPtrOrNil(query),
			Region:          stringPtrOrNil(region),
			VisibleBid:      metrics.visibleBid,
			RecommendedBid:  metrics.recommendedBid,
			CompetitiveBid:  metrics.competitiveBid,
			LeadershipBid:   metrics.leadershipBid,
			CPMMin:          metrics.cpmMin,
			Confidence:      0.74,
			Metadata:        metadata,
			CapturedAt:      input.CapturedAt,
		})
		if len(out) >= maxDerivedBidSnapshotsPerCapture {
			break
		}
	}
	return out
}

func derivePositionSnapshotsFromNetworkCapture(input CreateExtensionNetworkCaptureInput, payload json.RawMessage) []CreateExtensionPositionSnapshotInput {
	endpointKey := strings.TrimSpace(strings.ToLower(input.EndpointKey))
	if _, ok := extensionPositionEndpointKeys[endpointKey]; !ok {
		return nil
	}
	if len(payload) == 0 {
		return nil
	}

	var root any
	if err := json.Unmarshal(payload, &root); err != nil {
		return nil
	}

	base := newExtensionNetworkCaptureBase(input, root, endpointKey)
	candidates := collectNetworkResponseCandidates(root)
	if len(candidates) == 0 {
		return nil
	}

	seen := make(map[string]struct{})
	out := make([]CreateExtensionPositionSnapshotInput, 0, len(candidates))
	for _, candidate := range candidates {
		metrics := networkPositionMetrics(candidate)
		if metrics.visiblePosition <= 0 {
			continue
		}
		query := textFromMap(candidate, "norm_query", "normQuery", "query", "keyword", "phrase", "searchText", "text")
		if query == "" && len(base.normQueries) == 1 {
			query = base.normQueries[0]
		}
		if query == "" {
			query = base.fallbackQuery
		}
		region := textFromMap(candidate, "region", "geo", "geo_name", "regionName")
		if region == "" {
			region = base.fallbackRegion
		}
		wbCampaignID := int64FromMap(candidate, "advert_id", "advertId", "campaign_id", "campaignId", "wb_campaign_id", "wbCampaignID")
		if wbCampaignID <= 0 {
			wbCampaignID = base.wbCampaignID
		}
		wbProductID := int64FromMap(candidate, "nm_id", "nmId", "wb_product_id", "wbProductID", "product_id", "productId", "nm")
		if wbProductID <= 0 {
			wbProductID = base.wbProductID
		}
		if query == "" || region == "" || wbProductID <= 0 {
			continue
		}

		key := strings.Join([]string{
			query,
			region,
			strconv.FormatInt(wbProductID, 10),
			strconv.Itoa(metrics.visiblePosition),
			intKey(metrics.visiblePage),
		}, "|")
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}

		pageSubtype := metrics.pageSubtype
		if pageSubtype == "" {
			pageSubtype = base.pageSubtype
		}
		out = append(out, CreateExtensionPositionSnapshotInput{
			SellerCabinetID: input.SellerCabinetID,
			CampaignID:      input.CampaignID,
			PhraseID:        input.PhraseID,
			ProductID:       input.ProductID,
			Query:           query,
			Region:          region,
			VisiblePosition: metrics.visiblePosition,
			VisiblePage:     metrics.visiblePage,
			PageSubtype:     stringPtrOrNil(pageSubtype),
			Confidence:      0.72,
			Metadata:        derivedNetworkMetadata(base, wbCampaignID, wbProductID, candidate, "position"),
			CapturedAt:      input.CapturedAt,
		})
		if len(out) >= maxDerivedPositionSnapshotsPerCapture {
			break
		}
	}
	return out
}

func deriveUISignalsFromNetworkCapture(input CreateExtensionNetworkCaptureInput, payload json.RawMessage) []CreateExtensionUISignalInput {
	if len(payload) == 0 {
		return nil
	}

	var root any
	if err := json.Unmarshal(payload, &root); err != nil {
		return nil
	}

	endpointKey := strings.TrimSpace(strings.ToLower(input.EndpointKey))
	base := newExtensionNetworkCaptureBase(input, root, endpointKey)
	status := int64FromJSONPath(root, "status", "payload.status")
	candidates := collectNetworkResponseCandidates(root)
	if status >= 400 {
		candidates = append([]map[string]any{{"status": float64(status), "message": stringFromJSONPath(root, "response.message", "payload.response.message", "response.error", "payload.response.error")}}, candidates...)
	}
	if len(candidates) == 0 {
		return nil
	}

	seen := make(map[string]struct{})
	out := make([]CreateExtensionUISignalInput, 0, len(candidates))
	for _, candidate := range candidates {
		title, message, signalType, severity := networkUISignalText(candidate, status)
		if title == "" || signalType == "" {
			continue
		}
		query := textFromMap(candidate, "norm_query", "normQuery", "query", "keyword", "phrase", "searchText", "text")
		if query == "" {
			query = base.fallbackQuery
		}
		region := textFromMap(candidate, "region", "geo", "geo_name", "regionName")
		if region == "" {
			region = base.fallbackRegion
		}
		wbCampaignID := int64FromMap(candidate, "advert_id", "advertId", "campaign_id", "campaignId", "wb_campaign_id", "wbCampaignID")
		if wbCampaignID <= 0 {
			wbCampaignID = base.wbCampaignID
		}
		wbProductID := int64FromMap(candidate, "nm_id", "nmId", "wb_product_id", "wbProductID", "product_id", "productId", "nm")
		if wbProductID <= 0 {
			wbProductID = base.wbProductID
		}
		key := strings.Join([]string{signalType, title, query, region, strconv.FormatInt(wbCampaignID, 10), strconv.FormatInt(wbProductID, 10)}, "|")
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, CreateExtensionUISignalInput{
			SellerCabinetID: input.SellerCabinetID,
			CampaignID:      input.CampaignID,
			PhraseID:        input.PhraseID,
			ProductID:       input.ProductID,
			Query:           stringPtrOrNil(query),
			Region:          stringPtrOrNil(region),
			SignalType:      signalType,
			Severity:        severity,
			Title:           truncateText(title, 90),
			Message:         stringPtrOrNil(message),
			Confidence:      0.7,
			Metadata:        derivedNetworkMetadata(base, wbCampaignID, wbProductID, candidate, "ui_signal"),
			CapturedAt:      input.CapturedAt,
		})
		if len(out) >= maxDerivedUISignalsPerCapture {
			break
		}
	}
	return out
}

func deriveCampaignBudgetFromNetworkCapture(input CreateExtensionNetworkCaptureInput, payload json.RawMessage) *extensionCampaignBudgetSnapshot {
	endpointKey := strings.TrimSpace(strings.ToLower(input.EndpointKey))
	if endpointKey != "wb.budget" || len(payload) == 0 {
		return nil
	}

	var root any
	if err := json.Unmarshal(payload, &root); err != nil {
		return nil
	}
	status := int64FromJSONPath(root, "status", "payload.status")
	if status >= 400 {
		return nil
	}
	response := valueFromJSONPath(root, "response", "payload.response")
	if response == nil {
		return nil
	}
	for _, candidate := range collectJSONObjects(response, 0, 20) {
		cash := float64PtrFromMap(candidate, "cash")
		netting := float64PtrFromMap(candidate, "netting")
		total := float64PtrFromMap(candidate, "total")
		if cash == nil || netting == nil || total == nil {
			continue
		}
		if *cash < 0 || *netting < 0 || *total < 0 {
			continue
		}
		return &extensionCampaignBudgetSnapshot{
			cash:    rubToKopecks(*cash),
			netting: rubToKopecks(*netting),
			total:   rubToKopecks(*total),
		}
	}
	return nil
}

type extensionNetworkCaptureBase struct {
	endpointKey        string
	url                string
	pageSubtype        string
	wbCampaignID       int64
	wbProductID        int64
	wbSellerCabinetID  int64
	fallbackCampaignID int64
	fallbackProductID  int64
	fallbackQuery      string
	fallbackRegion     string
	normQueries        []string
}

func newExtensionNetworkCaptureBase(input CreateExtensionNetworkCaptureInput, root any, endpointKey string) extensionNetworkCaptureBase {
	base := extensionNetworkCaptureBase{
		endpointKey:        endpointKey,
		url:                stringFromJSONPath(root, "url"),
		pageSubtype:        stringFromJSONPath(root, "page_subtype", "payload.page_subtype"),
		wbCampaignID:       int64FromJSONPath(root, "wb_campaign_id", "wbCampaignID"),
		wbProductID:        int64FromJSONPath(root, "wb_product_id", "wbProductID"),
		wbSellerCabinetID:  int64FromJSONPath(root, "wb_seller_cabinet_id", "wbSellerCabinetID"),
		fallbackQuery:      normalizeOptionalText(input.Query),
		fallbackRegion:     normalizeOptionalText(input.Region),
		fallbackCampaignID: int64FromJSONPath(root, "payload.wb_campaign_id", "payload.wbCampaignID"),
		fallbackProductID:  int64FromJSONPath(root, "payload.wb_product_id", "payload.wbProductID"),
		normQueries:        stringsFromJSONPath(root, "norm_queries", "payload.norm_queries"),
	}
	if base.wbCampaignID <= 0 {
		base.wbCampaignID = base.fallbackCampaignID
	}
	if base.wbProductID <= 0 {
		base.wbProductID = base.fallbackProductID
	}
	return base
}

func collectNetworkBidCandidates(root any) []map[string]any {
	var candidates []map[string]any
	for _, path := range []string{"bid_candidates", "payload.bid_candidates"} {
		if items := mapsFromJSONPath(root, path); len(items) > 0 {
			candidates = append(candidates, items...)
		}
	}
	candidates = append(candidates, collectNetworkResponseCandidates(root)...)
	return candidates
}

func collectNetworkResponseCandidates(root any) []map[string]any {
	if response := valueFromJSONPath(root, "response", "payload.response"); response != nil {
		return collectJSONObjects(response, 0, 150)
	}
	return nil
}

func collectJSONObjects(value any, depth, limit int) []map[string]any {
	if value == nil || depth > 5 || limit <= 0 {
		return nil
	}
	switch typed := value.(type) {
	case []any:
		var out []map[string]any
		for _, item := range typed {
			out = append(out, collectJSONObjects(item, depth+1, limit-len(out))...)
			if len(out) >= limit {
				break
			}
		}
		return out
	case map[string]any:
		out := []map[string]any{typed}
		for _, item := range typed {
			out = append(out, collectJSONObjects(item, depth+1, limit-len(out))...)
			if len(out) >= limit {
				break
			}
		}
		return out
	default:
		return nil
	}
}

func networkBidMetrics(candidate map[string]any) extensionBidMetrics {
	return extensionBidMetrics{
		visibleBid:     int64PtrFromMap(candidate, "bid", "cpm", "current_bid", "currentBid", "visible_bid", "visibleBid"),
		recommendedBid: int64PtrFromMap(candidate, "recommended_bid", "recommendedBid"),
		competitiveBid: int64PtrFromMap(candidate, "competitive_bid", "competitiveBid"),
		leadershipBid:  int64PtrFromMap(candidate, "leadership_bid", "leadershipBid"),
		cpmMin:         int64PtrFromMap(candidate, "cpmMin", "cpm_min", "min_bid", "minBid"),
	}
}

func networkPositionMetrics(candidate map[string]any) extensionPositionMetrics {
	visiblePosition := int64FromMap(candidate, "position", "place", "visible_position", "visiblePosition")
	visiblePage64 := int64PtrFromMap(candidate, "page", "page_num", "pageNum", "visible_page", "visiblePage")
	var visiblePage *int
	if visiblePage64 != nil {
		value := int(*visiblePage64)
		visiblePage = &value
	}
	return extensionPositionMetrics{
		visiblePosition: int(visiblePosition),
		visiblePage:     visiblePage,
		pageSubtype:     textFromMap(candidate, "page_subtype", "pageSubtype", "placement", "source"),
	}
}

func (m extensionBidMetrics) empty() bool {
	return m.visibleBid == nil && m.recommendedBid == nil && m.competitiveBid == nil && m.leadershipBid == nil && m.cpmMin == nil
}

func derivedBidMetadata(base extensionNetworkCaptureBase, wbCampaignID, wbProductID int64, candidate map[string]any) json.RawMessage {
	return derivedNetworkMetadata(base, wbCampaignID, wbProductID, candidate, "bid")
}

func derivedNetworkMetadata(base extensionNetworkCaptureBase, wbCampaignID, wbProductID int64, candidate map[string]any, kind string) json.RawMessage {
	payload := map[string]any{
		"derived_from_network_capture": true,
		"source_endpoint":              base.endpointKey,
		"derived_kind":                 kind,
	}
	if base.url != "" {
		payload["source_url"] = base.url
	}
	if base.pageSubtype != "" {
		payload["page_subtype"] = base.pageSubtype
	}
	if wbCampaignID > 0 {
		payload["wb_campaign_id"] = wbCampaignID
	}
	if wbProductID > 0 {
		payload["wb_product_id"] = wbProductID
	}
	if base.wbSellerCabinetID > 0 {
		payload["wb_seller_cabinet_id"] = base.wbSellerCabinetID
	}
	if rawID := textFromMap(candidate, "id", "uuid"); rawID != "" {
		payload["source_candidate_id"] = rawID
	}
	out, _ := json.Marshal(payload)
	return out
}

func networkUISignalText(candidate map[string]any, fallbackStatus int64) (title, message, signalType, severity string) {
	status := int64FromMap(candidate, "status", "statusCode", "code")
	if status <= 0 {
		status = fallbackStatus
	}
	text := textFromMap(candidate, "error", "warning", "notice", "message", "hint", "statusText", "description")
	if status >= 400 {
		if text == "" {
			text = "WB вернул ошибку при загрузке данных"
		}
		return "Ошибка WB API " + strconv.FormatInt(status, 10), text, "wb_api_error", statusSeverity(status)
	}
	if text == "" {
		return "", "", "", ""
	}
	normalized := strings.ToLower(text)
	if isNoiseStatusText(normalized) {
		return "", "", "", ""
	}
	switch {
	case strings.Contains(normalized, "error") ||
		strings.Contains(normalized, "ошиб") ||
		strings.Contains(normalized, "недостат") ||
		strings.Contains(normalized, "invalid") ||
		strings.Contains(normalized, "expired") ||
		strings.Contains(normalized, "unauthorized"):
		return text, text, "wb_warning", "high"
	case strings.Contains(normalized, "recommend") ||
		strings.Contains(normalized, "рекомен") ||
		strings.Contains(normalized, "ставк") ||
		strings.Contains(normalized, "bid"):
		return text, text, "wb_hint", "medium"
	default:
		return "", "", "", ""
	}
}

func statusSeverity(status int64) string {
	switch {
	case status >= 500:
		return "high"
	case status == 401 || status == 403 || status == 429:
		return "high"
	default:
		return "medium"
	}
}

func isNoiseStatusText(value string) bool {
	switch strings.TrimSpace(value) {
	case "", "ok", "success", "successful", "успешно":
		return true
	default:
		return false
	}
}

func valueFromJSONPath(root any, paths ...string) any {
	for _, path := range paths {
		current := root
		found := true
		for _, part := range strings.Split(path, ".") {
			obj, ok := current.(map[string]any)
			if !ok {
				found = false
				break
			}
			value, ok := obj[part]
			if !ok {
				found = false
				break
			}
			current = value
		}
		if found {
			return current
		}
	}
	return nil
}

func stringFromJSONPath(root any, paths ...string) string {
	return textFromAny(valueFromJSONPath(root, paths...))
}

func int64FromJSONPath(root any, paths ...string) int64 {
	value := int64FromAny(valueFromJSONPath(root, paths...))
	if value == nil {
		return 0
	}
	return *value
}

func stringsFromJSONPath(root any, paths ...string) []string {
	for _, path := range paths {
		value := valueFromJSONPath(root, path)
		switch typed := value.(type) {
		case []any:
			out := make([]string, 0, len(typed))
			for _, item := range typed {
				if text := textFromAny(item); text != "" {
					out = append(out, text)
				}
			}
			if len(out) > 0 {
				return out
			}
		case []string:
			if len(typed) > 0 {
				return typed
			}
		case string:
			var out []string
			for _, part := range strings.Split(typed, ",") {
				if text := strings.TrimSpace(part); text != "" {
					out = append(out, text)
				}
			}
			if len(out) > 0 {
				return out
			}
		}
	}
	return nil
}

func mapsFromJSONPath(root any, path string) []map[string]any {
	value := valueFromJSONPath(root, path)
	switch typed := value.(type) {
	case []any:
		out := make([]map[string]any, 0, len(typed))
		for _, item := range typed {
			if obj, ok := item.(map[string]any); ok {
				out = append(out, obj)
			}
		}
		return out
	case []map[string]any:
		return typed
	default:
		return nil
	}
}

func textFromMap(candidate map[string]any, keys ...string) string {
	for _, key := range keys {
		if value, ok := candidate[key]; ok {
			if text := textFromAny(value); text != "" {
				return text
			}
		}
	}
	return ""
}

func textFromAny(value any) string {
	switch typed := value.(type) {
	case nil:
		return ""
	case string:
		return strings.TrimSpace(typed)
	case float64:
		if typed == math.Trunc(typed) {
			return strconv.FormatInt(int64(typed), 10)
		}
		return strconv.FormatFloat(typed, 'f', -1, 64)
	case bool:
		return strconv.FormatBool(typed)
	default:
		return ""
	}
}

func int64FromMap(candidate map[string]any, keys ...string) int64 {
	value := int64PtrFromMap(candidate, keys...)
	if value == nil {
		return 0
	}
	return *value
}

func int64PtrFromMap(candidate map[string]any, keys ...string) *int64 {
	for _, key := range keys {
		if value, ok := candidate[key]; ok {
			if parsed := int64FromAny(value); parsed != nil {
				return parsed
			}
		}
	}
	return nil
}

func int64FromAny(value any) *int64 {
	switch typed := value.(type) {
	case nil:
		return nil
	case float64:
		if math.IsInf(typed, 0) || math.IsNaN(typed) {
			return nil
		}
		result := int64(math.Round(typed))
		return &result
	case int:
		result := int64(typed)
		return &result
	case int64:
		result := typed
		return &result
	case json.Number:
		if parsed, err := typed.Int64(); err == nil {
			return &parsed
		}
		if parsed, err := typed.Float64(); err == nil {
			result := int64(math.Round(parsed))
			return &result
		}
	case string:
		normalized := normalizeNumericString(typed)
		if normalized == "" {
			return nil
		}
		if parsed, err := strconv.ParseFloat(normalized, 64); err == nil {
			result := int64(math.Round(parsed))
			return &result
		}
	}
	return nil
}

func float64PtrFromMap(candidate map[string]any, keys ...string) *float64 {
	for _, key := range keys {
		if value, ok := candidate[key]; ok {
			if parsed := float64FromAny(value); parsed != nil {
				return parsed
			}
		}
	}
	return nil
}

func float64FromAny(value any) *float64 {
	switch typed := value.(type) {
	case nil:
		return nil
	case float64:
		if math.IsInf(typed, 0) || math.IsNaN(typed) {
			return nil
		}
		return &typed
	case int:
		result := float64(typed)
		return &result
	case int64:
		result := float64(typed)
		return &result
	case json.Number:
		if parsed, err := typed.Float64(); err == nil {
			return &parsed
		}
	case string:
		normalized := normalizeNumericString(typed)
		if normalized == "" {
			return nil
		}
		if parsed, err := strconv.ParseFloat(normalized, 64); err == nil {
			return &parsed
		}
	}
	return nil
}

func normalizeNumericString(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	var builder strings.Builder
	decimalWritten := false
	for _, r := range value {
		switch {
		case r >= '0' && r <= '9':
			builder.WriteRune(r)
		case (r == ',' || r == '.') && !decimalWritten:
			builder.WriteRune('.')
			decimalWritten = true
		case r == '-' && builder.Len() == 0:
			builder.WriteRune(r)
		}
	}
	out := builder.String()
	if out == "-" || out == "." || out == "-." {
		return ""
	}
	return out
}

func stringPtrOrNil(value string) *string {
	value = strings.TrimSpace(value)
	if value == "" {
		return nil
	}
	return &value
}

func int64Key(value *int64) string {
	if value == nil {
		return ""
	}
	return strconv.FormatInt(*value, 10)
}

func intKey(value *int) string {
	if value == nil {
		return ""
	}
	return strconv.Itoa(*value)
}

func truncateText(value string, limit int) string {
	value = strings.TrimSpace(value)
	if limit <= 0 || len([]rune(value)) <= limit {
		return value
	}
	runes := []rune(value)
	return string(runes[:limit])
}
