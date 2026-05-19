package wb

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"time"

	"github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/pkg/apperror"
)

type normQueryRequest struct {
	Items []struct {
		AdvertID int64 `json:"advert_id"`
		NMID     int64 `json:"nm_id"`
	} `json:"items"`
}

type normQueryStatsRequest struct {
	From  string `json:"from"`
	To    string `json:"to"`
	Items []struct {
		AdvertID int64 `json:"advertId"`
		NMID     int64 `json:"nmId"`
	} `json:"items"`
}

const normQueryStatsBatchSize = 100

type normQueryBidsResponse struct {
	Bids []struct {
		AdvertID  int64  `json:"advert_id"`
		NMID      int64  `json:"nm_id"`
		Bid       int64  `json:"bid"`
		NormQuery string `json:"norm_query"`
	} `json:"bids"`
}

type normQueryListResponse struct {
	Items   []normQueryListItem `json:"items"`
	Queries []normQueryListItem `json:"queries"`
}

type normQueryListItem struct {
	AdvertID  int64  `json:"advert_id"`
	NMID      int64  `json:"nm_id"`
	NormQuery string `json:"norm_query"`
	Active    *bool  `json:"active"`
	Excluded  *bool  `json:"excluded"`
	Bid       int64  `json:"bid"`
	Count     int    `json:"count"`
}

type normQueryStatsResponse struct {
	Items []normQueryStatsItem `json:"items"`
}

type WBNormQueryStatsDebug struct {
	Status             int                      `json:"status"`
	ResponseItemsCount int                      `json:"responseItemsCount"`
	FirstItem          interface{}              `json:"firstItem,omitempty"`
	FirstStat          interface{}              `json:"firstStat,omitempty"`
	ParsedRows         []WBSearchClusterStatDTO `json:"parsedRows,omitempty"`
}

type normQueryStatsItem struct {
	AdvertID        int64               `json:"advertId"`
	AdvertIDSnake   int64               `json:"advert_id"`
	NMID            int64               `json:"nmId"`
	NMIDSnake       int64               `json:"nm_id"`
	DailyStats      []normQueryStatsDay `json:"dailyStats"`
	DailyStatsSnake []normQueryStatsDay `json:"daily_stats"`
}

type normQueryStatsDay struct {
	Date              string                  `json:"date"`
	Stat              normQueryStat           `json:"stat"`
	AppTypeStats      []normQueryAppTypeStats `json:"appTypeStats"`
	AppTypeStatsSnake []normQueryAppTypeStats `json:"app_type_stats"`
}

type normQueryAppTypeStats struct {
	Stats []normQueryStat `json:"stats"`
}

type normQueryStat struct {
	NormQuery      string  `json:"normQuery"`
	NormQuerySnake string  `json:"norm_query"`
	Views          int64   `json:"views"`
	Clicks         int64   `json:"clicks"`
	Atbs           int64   `json:"atbs"`
	Orders         int64   `json:"orders"`
	SHKs           int64   `json:"shks"`
	Spend          float64 `json:"spend"`
	Sum            float64 `json:"sum"`
	CPC            float64 `json:"cpc"`
	CPM            float64 `json:"cpm"`
	CTR            float64 `json:"ctr"`
	AvgPos         float64 `json:"avgPos"`
	AvgPosSnake    float64 `json:"avg_pos"`
}

type aggregatedNormQuery struct {
	keyword string
	nmID    int64
	bid     int64
	views   int64
	clicks  int64
	atbs    int64
	orders  int64
	spend   float64
	posSum  float64
	posN    int64
	daily   map[string]*aggregatedNormQueryDay
}

type aggregatedNormQueryDay struct {
	date   string
	views  int64
	clicks int64
	atbs   int64
	orders int64
	spend  float64
	posSum float64
	posN   int64
}

// ListSearchClusters fetches search clusters for a campaign from the current normquery API.
func (c *Client) ListSearchClusters(ctx context.Context, token string, campaignID int) ([]WBSearchClusterDTO, error) {
	aggregated, dateTo, err := c.loadNormQueryAggregates(ctx, token, campaignID)
	if err != nil {
		return nil, err
	}

	_ = dateTo
	return aggregatedToClusters(campaignID, aggregated), nil
}

// GetSearchClusterStats fetches phrase statistics for a campaign from the current normquery API.
func (c *Client) GetSearchClusterStats(ctx context.Context, token string, campaignID int) ([]WBSearchClusterStatDTO, error) {
	aggregated, dateTo, err := c.loadNormQueryAggregates(ctx, token, campaignID)
	if err != nil {
		return nil, err
	}

	return aggregatedToClusterStats(campaignID, dateTo, aggregated), nil
}

// ListSearchClustersWithNMIDs fetches search clusters without calling ListCampaigns internally.
// Caller provides nmIDs directly (from DB or pre-fetched campaign data).
func (c *Client) ListSearchClustersWithNMIDs(ctx context.Context, token string, campaignID int, nmIDs []int64) ([]WBSearchClusterDTO, error) {
	aggregated, _, err := c.loadNormQueryAggregatesWithNMIDs(ctx, token, campaignID, nmIDs)
	if err != nil {
		return nil, err
	}
	return aggregatedToClusters(campaignID, aggregated), nil
}

// ListLegacyNormQueryClusters fetches WB cluster inventory for active/excluded state.
// These rows are management state only; advertising analytics must still come from /adv/v1/normquery/stats.
func (c *Client) ListLegacyNormQueryClusters(ctx context.Context, token string, campaignID int, nmIDs []int64) ([]WBSearchClusterDTO, error) {
	if len(nmIDs) == 0 {
		return nil, nil
	}
	body, err := json.Marshal(buildNormQueryRequest(campaignID, nmIDs))
	if err != nil {
		return nil, apperror.New(apperror.ErrWBAPIError, fmt.Sprintf("marshal normquery list request: %v", err))
	}
	_, raw, err := c.doRequest(ctx, "POST", "/adv/v0/normquery/list", token, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	var response normQueryListResponse
	if err := json.Unmarshal(raw, &response); err != nil {
		return nil, apperror.New(apperror.ErrWBAPIError, fmt.Sprintf("unmarshal normquery list: %v", err))
	}
	items := response.Items
	if len(items) == 0 {
		items = response.Queries
	}
	result := make([]WBSearchClusterDTO, 0, len(items))
	for _, item := range items {
		if item.NormQuery == "" {
			continue
		}
		stateKeywords := []string{item.NormQuery}
		if item.Excluded != nil && *item.Excluded {
			stateKeywords = append(stateKeywords, "excluded")
		} else if item.Active != nil && !*item.Active {
			stateKeywords = append(stateKeywords, "inactive")
		}
		result = append(result, WBSearchClusterDTO{
			NmID:      item.NMID,
			NormQuery: item.NormQuery,
			Keywords:  stateKeywords,
			Count:     item.Count,
			Bid:       item.Bid,
		})
	}
	return result, nil
}

// GetSearchClusterStatsWithNMIDs fetches phrase stats without calling ListCampaigns internally.
func (c *Client) GetSearchClusterStatsWithNMIDs(ctx context.Context, token string, campaignID int, nmIDs []int64) ([]WBSearchClusterStatDTO, error) {
	aggregated, dateTo, err := c.loadNormQueryAggregatesWithNMIDs(ctx, token, campaignID, nmIDs)
	if err != nil {
		return nil, err
	}
	return aggregatedToClusterStats(campaignID, dateTo, aggregated), nil
}

func (c *Client) DebugNormQueryStats(ctx context.Context, token string, campaignID int, nmIDs []int64, dateFrom, dateTo string) (WBNormQueryStatsDebug, error) {
	if len(nmIDs) > normQueryStatsBatchSize {
		nmIDs = nmIDs[:normQueryStatsBatchSize]
	}
	if len(nmIDs) == 0 {
		return WBNormQueryStatsDebug{}, nil
	}

	statsBody, err := json.Marshal(buildNormQueryStatsRequest(campaignID, nmIDs, dateFrom, dateTo))
	if err != nil {
		return WBNormQueryStatsDebug{}, apperror.New(apperror.ErrWBAPIError, fmt.Sprintf("marshal normquery stats request: %v", err))
	}

	resp, statsRaw, err := c.doRequest(ctx, "POST", "/adv/v1/normquery/stats", token, bytes.NewReader(statsBody))
	if err != nil {
		return WBNormQueryStatsDebug{}, err
	}

	var batch normQueryStatsResponse
	if err := json.Unmarshal(statsRaw, &batch); err != nil {
		return WBNormQueryStatsDebug{}, apperror.New(apperror.ErrWBAPIError, fmt.Sprintf("unmarshal normquery stats: %v", err))
	}

	aggregated := make(map[string]*aggregatedNormQuery)
	for _, item := range batch.Items {
		nmID := item.nmID()
		for _, day := range item.dailyStats() {
			for _, stat := range day.stats() {
				addNormQueryStat(aggregated, nil, nmID, day.Date, stat)
			}
		}
	}

	status := 0
	if resp != nil {
		status = resp.StatusCode
	}

	return WBNormQueryStatsDebug{
		Status:             status,
		ResponseItemsCount: len(batch.Items),
		FirstItem:          firstNormQueryStatsItem(batch.Items),
		FirstStat:          firstNormQueryStatsStat(batch.Items),
		ParsedRows:         aggregatedToClusterStats(campaignID, dateTo, aggregated),
	}, nil
}

// loadNormQueryAggregatesWithNMIDs is the optimized version that doesn't call ListCampaigns.
func (c *Client) loadNormQueryAggregatesWithNMIDs(ctx context.Context, token string, campaignID int, nmIDs []int64) (map[string]*aggregatedNormQuery, string, error) {
	if len(nmIDs) == 0 {
		_, dateTo := wbAdvertStatsDateRange(time.Now())
		return map[string]*aggregatedNormQuery{}, dateTo, nil
	}

	dateFrom, dateTo := wbAdvertStatsDateRange(time.Now())

	c.logger.Info().
		Int("campaign_id", campaignID).
		Int("pairs_count", len(nmIDs)).
		Interface("first_pairs", sampleNormQueryPairs(campaignID, nmIDs, 10)).
		Msg("[NQ] pairs count")

	statsResponse, err := c.fetchNormQueryStats(ctx, token, campaignID, nmIDs, dateFrom, dateTo)
	if err != nil && len(statsResponse.Items) == 0 {
		return nil, "", err
	}

	// Debug: log response structure to diagnose empty phrases
	totalStatsItems := 0
	totalKeywords := 0
	for _, item := range statsResponse.Items {
		totalStatsItems++
		for _, day := range item.dailyStats() {
			for _, stat := range day.stats() {
				if stat.normQuery() != "" {
					totalKeywords++
				}
			}
		}
	}
	if totalKeywords == 0 {
		c.logger.Warn().
			Int("campaign_id", campaignID).
			Int("stats_items", totalStatsItems).
			Msg("normquery stats returned 0 keywords")
	} else {
		c.logger.Debug().
			Int("campaign_id", campaignID).
			Int("stats_items", totalStatsItems).
			Int("keywords", totalKeywords).
			Msg("normquery stats parsed")
	}

	bidsBody, err := json.Marshal(buildNormQueryRequest(campaignID, nmIDs))
	if err != nil {
		return nil, "", apperror.New(apperror.ErrWBAPIError, fmt.Sprintf("marshal normquery bids request: %v", err))
	}

	_, bidsRaw, err := c.doRequest(ctx, "POST", "/adv/v0/normquery/get-bids", token, bytes.NewReader(bidsBody))
	if err != nil {
		c.logger.Warn().Err(err).Int("campaign_id", campaignID).Msg("normquery get-bids failed, falling back without explicit bids")
	}

	bidsByQuery := make(map[string]int64)
	if len(bidsRaw) > 0 {
		var bidsResponse normQueryBidsResponse
		if err := json.Unmarshal(bidsRaw, &bidsResponse); err == nil {
			for _, bid := range bidsResponse.Bids {
				if bid.NormQuery == "" {
					continue
				}
				key := normQueryKey(bid.NMID, bid.NormQuery)
				if bid.Bid > bidsByQuery[key] {
					bidsByQuery[key] = bid.Bid
				}
			}
		}
	}

	aggregated := make(map[string]*aggregatedNormQuery)
	for _, item := range statsResponse.Items {
		nmID := item.nmID()
		for _, day := range item.dailyStats() {
			for _, stat := range day.stats() {
				addNormQueryStat(aggregated, bidsByQuery, nmID, day.Date, stat)
			}
		}
	}

	parsedRows := aggregatedToClusterStats(campaignID, dateTo, aggregated)
	c.logger.Info().
		Int("campaign_id", campaignID).
		Int("totalRows", len(parsedRows)).
		Int("rowsWithViews", countNormQueryRowsWith(parsedRows, func(row WBSearchClusterStatDTO) bool { return row.Views > 0 })).
		Int("rowsWithClicks", countNormQueryRowsWith(parsedRows, func(row WBSearchClusterStatDTO) bool { return row.Clicks > 0 })).
		Int("rowsWithSpend", countNormQueryRowsWith(parsedRows, func(row WBSearchClusterStatDTO) bool { return row.Sum > 0 })).
		Interface("sample", sampleParsedNormQueryRows(parsedRows, 5)).
		Msg("[NQ] parsed rows")

	return aggregated, dateTo, nil
}

func (c *Client) fetchNormQueryStats(ctx context.Context, token string, campaignID int, nmIDs []int64, dateFrom, dateTo string) (normQueryStatsResponse, error) {
	var combined normQueryStatsResponse
	var firstErr error
	for start := 0; start < len(nmIDs); start += normQueryStatsBatchSize {
		end := start + normQueryStatsBatchSize
		if end > len(nmIDs) {
			end = len(nmIDs)
		}
		if start > 0 && c.normQueryInterBatchDelay > 0 {
			if err := sleepWithContext(ctx, c.normQueryInterBatchDelay); err != nil {
				return combined, err
			}
		}

		statsBody, err := json.Marshal(buildNormQueryStatsRequest(campaignID, nmIDs[start:end], dateFrom, dateTo))
		if err != nil {
			return combined, apperror.New(apperror.ErrWBAPIError, fmt.Sprintf("marshal normquery stats request: %v", err))
		}

		c.logger.Info().
			Str("endpoint", c.baseURL+"/adv/v1/normquery/stats").
			Str("from", dateFrom).
			Str("to", dateTo).
			Int("itemsCount", end-start).
			Interface("firstItems", sampleNormQueryPairs(campaignID, nmIDs[start:end], 3)).
			Msg("[NQ] request")

		resp, statsRaw, err := c.doRequest(ctx, "POST", "/adv/v1/normquery/stats", token, bytes.NewReader(statsBody))
		if err != nil {
			if firstErr == nil {
				firstErr = err
			}
			c.logger.Warn().
				Err(err).
				Int("campaign_id", campaignID).
				Int("items", end-start).
				Msg("skipping normquery stats batch after WB error")
			continue
		}

		var batch normQueryStatsResponse
		if err := json.Unmarshal(statsRaw, &batch); err != nil {
			preview := string(statsRaw)
			if len(preview) > 500 {
				preview = preview[:500]
			}
			c.logger.Error().
				Int("campaign_id", campaignID).
				Str("raw_preview", preview).
				Msg("normquery stats unmarshal failed")
			return combined, apperror.New(apperror.ErrWBAPIError, fmt.Sprintf("unmarshal normquery stats: %v", err))
		}
		status := 0
		if resp != nil {
			status = resp.StatusCode
		}
		c.logger.Info().
			Int("status", status).
			Int("itemsCount", len(batch.Items)).
			Interface("firstItem", firstNormQueryStatsItem(batch.Items)).
			Interface("firstStat", firstNormQueryStatsStat(batch.Items)).
			Msg("[NQ] response")
		combined.Items = append(combined.Items, batch.Items...)
	}
	return combined, firstErr
}

func wbAdvertStatsDateRange(now time.Time) (string, string) {
	location, err := time.LoadLocation("Europe/Moscow")
	if err != nil {
		location = time.FixedZone("MSK", 3*60*60)
	}
	nowMSK := now.In(location)
	yesterday := time.Date(nowMSK.Year(), nowMSK.Month(), nowMSK.Day(), 0, 0, 0, 0, location).AddDate(0, 0, -1)
	return yesterday.AddDate(0, 0, -30).Format(dateFmt), yesterday.Format(dateFmt)
}

func (item normQueryStatsItem) dailyStats() []normQueryStatsDay {
	if len(item.DailyStats) > 0 {
		return item.DailyStats
	}
	return item.DailyStatsSnake
}

func (item normQueryStatsItem) nmID() int64 {
	if item.NMID != 0 {
		return item.NMID
	}
	return item.NMIDSnake
}

func (day normQueryStatsDay) stats() []normQueryStat {
	stats := make([]normQueryStat, 0, 1)
	if day.Stat.normQuery() != "" {
		stats = append(stats, day.Stat)
	}
	for _, app := range day.AppTypeStats {
		stats = append(stats, app.Stats...)
	}
	for _, app := range day.AppTypeStatsSnake {
		stats = append(stats, app.Stats...)
	}
	return stats
}

func (stat normQueryStat) normQuery() string {
	if stat.NormQuery != "" {
		return stat.NormQuery
	}
	return stat.NormQuerySnake
}

func addNormQueryStat(aggregated map[string]*aggregatedNormQuery, bidsByQuery map[string]int64, nmID int64, date string, stat normQueryStat) {
	keyword := stat.normQuery()
	if keyword == "" {
		return
	}
	key := normQueryKey(nmID, keyword)
	entry, ok := aggregated[key]
	if !ok {
		entry = &aggregatedNormQuery{keyword: keyword, nmID: nmID, daily: make(map[string]*aggregatedNormQueryDay)}
		aggregated[key] = entry
	}
	spend := normQueryStatSpend(stat)
	entry.views += stat.Views
	entry.clicks += stat.Clicks
	entry.atbs += stat.Atbs
	entry.orders += stat.Orders
	if entry.bid == 0 {
		entry.bid = bidsByQuery[key]
	}
	entry.spend += spend
	if avgPos := stat.avgPos(); avgPos > 0 {
		weight := stat.Views
		if weight == 0 {
			weight = stat.Clicks
		}
		if weight == 0 {
			weight = 1
		}
		entry.posSum += avgPos * float64(weight)
		entry.posN += weight
	}
	if date == "" {
		return
	}
	day := entry.daily[date]
	if day == nil {
		day = &aggregatedNormQueryDay{date: date}
		entry.daily[date] = day
	}
	day.views += stat.Views
	day.clicks += stat.Clicks
	day.atbs += stat.Atbs
	day.orders += stat.Orders
	day.spend += spend
	if avgPos := stat.avgPos(); avgPos > 0 {
		weight := stat.Views
		if weight == 0 {
			weight = stat.Clicks
		}
		if weight == 0 {
			weight = 1
		}
		day.posSum += avgPos * float64(weight)
		day.posN += weight
	}
}

// loadNormQueryAggregates resolves the NM IDs for a campaign via ListCampaigns,
// then defers to loadNormQueryAggregatesWithNMIDs. The split exists so callers
// that already have NM IDs (e.g. SearchClusterStats with explicit input) can
// skip the ListCampaigns round-trip.
func (c *Client) loadNormQueryAggregates(ctx context.Context, token string, campaignID int) (map[string]*aggregatedNormQuery, string, error) {
	campaigns, err := c.ListCampaigns(ctx, token)
	if err != nil {
		return nil, "", err
	}
	return c.loadNormQueryAggregatesWithNMIDs(ctx, token, campaignID, campaignNMIDs(campaigns, campaignID))
}

func buildNormQueryRequest(campaignID int, nmIDs []int64) normQueryRequest {
	req := normQueryRequest{
		Items: make([]struct {
			AdvertID int64 `json:"advert_id"`
			NMID     int64 `json:"nm_id"`
		}, 0, len(nmIDs)),
	}
	for _, nmID := range nmIDs {
		req.Items = append(req.Items, struct {
			AdvertID int64 `json:"advert_id"`
			NMID     int64 `json:"nm_id"`
		}{
			AdvertID: int64(campaignID),
			NMID:     nmID,
		})
	}
	return req
}

func buildNormQueryStatsRequest(campaignID int, nmIDs []int64, dateFrom, dateTo string) normQueryStatsRequest {
	req := normQueryStatsRequest{
		From: dateFrom,
		To:   dateTo,
		Items: make([]struct {
			AdvertID int64 `json:"advertId"`
			NMID     int64 `json:"nmId"`
		}, 0, len(nmIDs)),
	}
	for _, nmID := range nmIDs {
		req.Items = append(req.Items, struct {
			AdvertID int64 `json:"advertId"`
			NMID     int64 `json:"nmId"`
		}{
			AdvertID: int64(campaignID),
			NMID:     nmID,
		})
	}
	return req
}

func sampleNormQueryPairs(campaignID int, nmIDs []int64, limit int) []map[string]int64 {
	if len(nmIDs) < limit {
		limit = len(nmIDs)
	}
	items := make([]map[string]int64, 0, limit)
	for i := 0; i < limit; i++ {
		items = append(items, map[string]int64{
			"advertId": int64(campaignID),
			"nmId":     nmIDs[i],
		})
	}
	return items
}

func firstNormQueryStatsItem(items []normQueryStatsItem) interface{} {
	if len(items) == 0 {
		return nil
	}
	return items[0]
}

func firstNormQueryStatsStat(items []normQueryStatsItem) interface{} {
	if len(items) == 0 {
		return nil
	}
	for _, day := range items[0].dailyStats() {
		for _, stat := range day.stats() {
			if stat.normQuery() != "" || stat.Views > 0 || stat.Clicks > 0 || stat.Spend > 0 || stat.Sum > 0 {
				return stat
			}
		}
	}
	return nil
}

func countNormQueryRowsWith(rows []WBSearchClusterStatDTO, matches func(WBSearchClusterStatDTO) bool) int {
	count := 0
	for _, row := range rows {
		if matches(row) {
			count++
		}
	}
	return count
}

func sampleParsedNormQueryRows(rows []WBSearchClusterStatDTO, limit int) []map[string]interface{} {
	if len(rows) < limit {
		limit = len(rows)
	}
	sample := make([]map[string]interface{}, 0, limit)
	for i := 0; i < limit; i++ {
		row := rows[i]
		sample = append(sample, map[string]interface{}{
			"advertId":  row.AdvertID,
			"nmId":      row.NmID,
			"normQuery": row.NormQuery,
			"date":      row.Date,
			"views":     row.Views,
			"clicks":    row.Clicks,
			"spend":     row.Sum,
			"atbs":      row.Atbs,
			"orders":    row.Orders,
			"cpc":       row.CPC,
			"cpm":       row.CPM,
			"avgPos":    row.AvgPos,
		})
	}
	return sample
}

func campaignNMIDs(campaigns []WBCampaignDTO, campaignID int) []int64 {
	for _, campaign := range campaigns {
		if campaign.AdvertID == campaignID {
			return campaign.NMIDs
		}
	}
	return nil
}

func aggregatedToClusters(campaignID int, aggregated map[string]*aggregatedNormQuery) []WBSearchClusterDTO {
	keys := sortedNormQueries(aggregated)
	result := make([]WBSearchClusterDTO, 0, len(keys))
	for _, key := range keys {
		entry := aggregated[key]
		// For CPC campaigns, views=0 → use clicks as count fallback
		count := int(entry.views)
		if count == 0 {
			count = int(entry.clicks)
		}
		result = append(result, WBSearchClusterDTO{
			NmID:      entry.nmID,
			NormQuery: entry.keyword,
			Keywords:  []string{entry.keyword},
			Count:     count,
			Bid:       entry.bid,
		})
	}
	return result
}

func aggregatedToClusterStats(campaignID int, date string, aggregated map[string]*aggregatedNormQuery) []WBSearchClusterStatDTO {
	keys := sortedNormQueries(aggregated)
	result := make([]WBSearchClusterStatDTO, 0, len(keys))
	for _, key := range keys {
		entry := aggregated[key]
		dates := make([]string, 0, len(entry.daily))
		for date := range entry.daily {
			dates = append(dates, date)
		}
		sort.Strings(dates)
		for _, date := range dates {
			day := entry.daily[date]
			result = append(result, WBSearchClusterStatDTO{
				AdvertID:  int64(campaignID),
				NmID:      entry.nmID,
				NormQuery: entry.keyword,
				Date:      day.date,
				Views:     day.views,
				Clicks:    day.clicks,
				Sum:       day.spend,
				Atbs:      day.atbs,
				Orders:    day.orders,
				CPC:       cpcFromSpend(day.spend, day.clicks),
				CPM:       cpmFromSpend(day.spend, day.views),
				AvgPos:    avgPosition(day.posSum, day.posN),
			})
		}
	}
	return result
}

func sortedNormQueries(aggregated map[string]*aggregatedNormQuery) []string {
	keywords := make([]string, 0, len(aggregated))
	for keyword := range aggregated {
		keywords = append(keywords, keyword)
	}
	sort.Strings(keywords)
	return keywords
}

func normQuerySpend(views, clicks int64, cpc, cpm float64) float64 {
	if cpc > 0 && clicks > 0 {
		return (float64(clicks) * cpc) / 100
	}
	if cpm > 0 && views > 0 {
		return (float64(views) / 1000) * (cpm / 100)
	}
	return 0
}

func normQueryKey(nmID int64, keyword string) string {
	return fmt.Sprintf("%d:%s", nmID, keyword)
}

func (stat normQueryStat) avgPos() float64 {
	if stat.AvgPos > 0 {
		return stat.AvgPos
	}
	return stat.AvgPosSnake
}

func normQueryStatSpend(stat normQueryStat) float64 {
	if stat.Spend > 0 {
		return stat.Spend
	}
	if stat.Sum > 0 {
		return stat.Sum
	}
	return normQuerySpend(stat.Views, stat.Clicks, stat.CPC, stat.CPM)
}

func cpcFromSpend(spend float64, clicks int64) float64 {
	if clicks <= 0 {
		return 0
	}
	return spend / float64(clicks)
}

func cpmFromSpend(spend float64, views int64) float64 {
	if views <= 0 {
		return 0
	}
	return spend / float64(views) * 1000
}

func avgPosition(sum float64, count int64) float64 {
	if count <= 0 {
		return 0
	}
	return sum / float64(count)
}
