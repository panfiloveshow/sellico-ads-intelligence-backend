package service

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/integration/wb"
	"github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/pkg/apperror"
	"github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/pkg/crypto"
	sqlcgen "github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/repository/sqlc"
)

type NormQueryDebugPair struct {
	AdvertID int64 `json:"advertId"`
	NMID     int64 `json:"nmId"`
}

type NormQueryDebugReport struct {
	BackendVersion            string                   `json:"backendVersion"`
	DateFrom                  string                   `json:"dateFrom"`
	DateTo                    string                   `json:"dateTo"`
	PairsCount                int                      `json:"pairsCount"`
	FirstPairs                []NormQueryDebugPair     `json:"firstPairs"`
	WBFirstStatus             int                      `json:"wbFirstStatus"`
	WBFirstResponseItemsCount int                      `json:"wbFirstResponseItemsCount"`
	WBFirstItem               interface{}              `json:"wbFirstItem,omitempty"`
	WBFirstStat               interface{}              `json:"wbFirstStat,omitempty"`
	ParsedRowsCount           int                      `json:"parsedRowsCount"`
	SavedRowsCount            int                      `json:"savedRowsCount"`
	ReadRowsCount             int                      `json:"readRowsCount"`
	ReadSample                []map[string]interface{} `json:"readSample"`
}

type normQueryDebugCandidate struct {
	NormQueryDebugPair
	CampaignID uuid.UUID
	CabinetID  uuid.UUID
	ProductID  uuid.UUID
	Source     string
}

func isWBAdvertStatsEligibleStatus(status string) bool {
	switch strings.ToLower(strings.TrimSpace(status)) {
	case "active", "paused", "completed":
		return true
	default:
		return false
	}
}

func (s *AdsReadService) DebugNormQuery(ctx context.Context, workspaceID uuid.UUID, dateFrom, dateTo time.Time, filter QuerySummaryFilter) (*NormQueryDebugReport, error) {
	data, err := s.loadWorkspaceData(ctx, workspaceID, dateFrom, dateTo)
	if err != nil {
		return nil, err
	}

	candidates := s.normQueryDebugPairs(data, dateFrom, dateTo, filter)
	report := &NormQueryDebugReport{
		BackendVersion: s.backendVersion,
		DateFrom:       dateFrom.Format("2006-01-02"),
		DateTo:         dateTo.Format("2006-01-02"),
		PairsCount:     len(candidates),
		FirstPairs:     firstDebugPairs(candidates, 10),
		ReadSample:     []map[string]interface{}{},
	}
	if len(candidates) == 0 {
		return report, nil
	}

	firstGroup := candidatesForFirstCampaign(candidates)
	token, err := s.decryptDebugCabinetToken(ctx, data, firstGroup[0].CabinetID)
	if err != nil {
		return nil, err
	}

	nmIDs := make([]int64, 0, len(firstGroup))
	for _, candidate := range firstGroup {
		nmIDs = append(nmIDs, candidate.NMID)
	}

	wbDebug, err := s.wbClient.DebugNormQueryStats(ctx, token, int(firstGroup[0].AdvertID), nmIDs, report.DateFrom, report.DateTo)
	if err != nil {
		return nil, err
	}
	report.WBFirstStatus = wbDebug.Status
	report.WBFirstResponseItemsCount = wbDebug.ResponseItemsCount
	report.WBFirstItem = wbDebug.FirstItem
	report.WBFirstStat = wbDebug.FirstStat
	report.ParsedRowsCount = len(wbDebug.ParsedRows)

	savedRows, err := s.saveNormQueryDebugRows(ctx, workspaceID, data, wbDebug.ParsedRows)
	if err != nil {
		return nil, err
	}
	report.SavedRowsCount = savedRows

	s.invalidateWorkspaceCache(workspaceID)
	readData, err := s.loadWorkspaceData(ctx, workspaceID, dateFrom, dateTo)
	if err != nil {
		return nil, err
	}
	rows := s.buildQuerySummaries(readData, dateFrom, dateTo, filter)
	report.ReadRowsCount = len(rows)
	report.ReadSample = sampleQueryRows(rows, 5)

	return report, nil
}

func (s *AdsReadService) normQueryDebugPairs(data *adsWorkspaceData, dateFrom, dateTo time.Time, filter QuerySummaryFilter) []normQueryDebugCandidate {
	productByID := make(map[uuid.UUID]domainProductRef, len(data.products))
	for _, product := range data.products {
		productByID[product.ID] = domainProductRef{
			ID:          product.ID,
			WBProductID: product.WBProductID,
		}
	}

	candidates := make([]normQueryDebugCandidate, 0)
	seen := make(map[string]struct{})
	for _, campaign := range data.campaigns {
		if filter.SellerCabinetID != nil && campaign.SellerCabinetID != *filter.SellerCabinetID {
			continue
		}
		if !isWBAdvertStatsEligibleStatus(campaign.Status) || campaign.WBCampaignID <= 0 {
			continue
		}
		for key, stats := range data.productStatsByLink {
			if key.campaignID != campaign.ID {
				continue
			}
			product, ok := productByID[key.productID]
			if !ok || product.WBProductID <= 0 {
				continue
			}
			active := false
			for _, stat := range stats {
				if !dateInRange(stat.Date, dateFrom, dateTo) {
					continue
				}
				orders := int64(0)
				if stat.Orders != nil {
					orders = *stat.Orders
				}
				if stat.Spend > 0 || stat.Clicks > 0 || orders > 0 {
					active = true
					break
				}
			}
			if !active {
				continue
			}
			pair := normQueryDebugCandidate{
				NormQueryDebugPair: NormQueryDebugPair{AdvertID: campaign.WBCampaignID, NMID: product.WBProductID},
				CampaignID:         campaign.ID,
				CabinetID:          campaign.SellerCabinetID,
				ProductID:          product.ID,
				Source:             "fullstats",
			}
			dedupe := fmt.Sprintf("%d:%d", pair.AdvertID, pair.NMID)
			if _, ok := seen[dedupe]; ok {
				continue
			}
			seen[dedupe] = struct{}{}
			candidates = append(candidates, pair)
		}
		for _, productID := range data.campaignProductIDs[campaign.ID] {
			product, ok := productByID[productID]
			if !ok || product.WBProductID <= 0 {
				continue
			}
			key := productCampaignKey{productID: productID, campaignID: campaign.ID}
			active := false
			for _, stat := range data.productStatsByLink[key] {
				if !dateInRange(stat.Date, dateFrom, dateTo) {
					continue
				}
				orders := int64(0)
				if stat.Orders != nil {
					orders = *stat.Orders
				}
				if stat.Spend > 0 || stat.Clicks > 0 || orders > 0 {
					active = true
					break
				}
			}
			source := "advert_settings"
			if active {
				source = "fullstats"
			}
			pair := normQueryDebugCandidate{
				NormQueryDebugPair: NormQueryDebugPair{AdvertID: campaign.WBCampaignID, NMID: product.WBProductID},
				CampaignID:         campaign.ID,
				CabinetID:          campaign.SellerCabinetID,
				ProductID:          product.ID,
				Source:             source,
			}
			dedupe := fmt.Sprintf("%d:%d", pair.AdvertID, pair.NMID)
			if _, ok := seen[dedupe]; ok {
				continue
			}
			seen[dedupe] = struct{}{}
			if active {
				candidates = append([]normQueryDebugCandidate{pair}, candidates...)
			} else {
				candidates = append(candidates, pair)
			}
		}
	}
	sort.SliceStable(candidates, func(i, j int) bool {
		if candidates[i].Source != candidates[j].Source {
			return candidates[i].Source == "fullstats"
		}
		if candidates[i].AdvertID != candidates[j].AdvertID {
			return candidates[i].AdvertID < candidates[j].AdvertID
		}
		return candidates[i].NMID < candidates[j].NMID
	})
	return candidates
}

type domainProductRef struct {
	ID          uuid.UUID
	WBProductID int64
}

func firstDebugPairs(candidates []normQueryDebugCandidate, limit int) []NormQueryDebugPair {
	if len(candidates) < limit {
		limit = len(candidates)
	}
	result := make([]NormQueryDebugPair, 0, limit)
	for i := 0; i < limit; i++ {
		result = append(result, candidates[i].NormQueryDebugPair)
	}
	return result
}

func candidatesForFirstCampaign(candidates []normQueryDebugCandidate) []normQueryDebugCandidate {
	if len(candidates) == 0 {
		return nil
	}
	advertID := candidates[0].AdvertID
	cabinetID := candidates[0].CabinetID
	result := make([]normQueryDebugCandidate, 0, 100)
	for _, candidate := range candidates {
		if candidate.AdvertID != advertID || candidate.CabinetID != cabinetID {
			continue
		}
		result = append(result, candidate)
		if len(result) == 100 {
			break
		}
	}
	return result
}

func (s *AdsReadService) decryptDebugCabinetToken(ctx context.Context, data *adsWorkspaceData, cabinetID uuid.UUID) (string, error) {
	cabinet, ok := data.cabinets[cabinetID]
	if !ok {
		return "", apperror.New(apperror.ErrNotFound, "seller cabinet not found")
	}
	token, err := crypto.Decrypt(cabinet.EncryptedToken, s.encryptionKey)
	if err != nil {
		return "", apperror.New(apperror.ErrInternal, "failed to decrypt cabinet token")
	}
	return token, nil
}

func (s *AdsReadService) saveNormQueryDebugRows(ctx context.Context, workspaceID uuid.UUID, data *adsWorkspaceData, rows []wb.WBSearchClusterStatDTO) (int, error) {
	campaignByWBID := make(map[int64]uuid.UUID)
	cabinetByCampaign := make(map[int64]uuid.UUID)
	for _, campaign := range data.campaigns {
		campaignByWBID[campaign.WBCampaignID] = campaign.ID
		cabinetByCampaign[campaign.WBCampaignID] = campaign.SellerCabinetID
	}

	phraseIDs := make(map[string]uuid.UUID)
	savedRows := 0
	for _, row := range rows {
		normQuery := strings.TrimSpace(row.NormQuery)
		if row.AdvertID <= 0 || row.NmID <= 0 || normQuery == "" {
			continue
		}
		campaignID, ok := campaignByWBID[row.AdvertID]
		if !ok {
			continue
		}
		cabinetID := cabinetByCampaign[row.AdvertID]
		key := fmt.Sprintf("%d:%d:%s", row.AdvertID, row.NmID, normQuery)
		phraseID, ok := phraseIDs[key]
		if !ok {
			productRow, err := s.queries.UpsertProduct(ctx, sqlcgen.UpsertProductParams{
				WorkspaceID:     uuidToPgtype(workspaceID),
				SellerCabinetID: uuidToPgtype(cabinetID),
				WbProductID:     row.NmID,
				Title:           "",
				Brand:           pgtype.Text{},
				Category:        pgtype.Text{},
				ImageUrl:        pgtype.Text{},
				Price:           pgtype.Int8{},
			})
			if err != nil {
				return savedRows, apperror.New(apperror.ErrInternal, fmt.Sprintf("upsert product for normquery debug: %v", err))
			}
			productID := uuidFromPgtype(productRow.ID)
			count := int(row.Views)
			if count == 0 {
				count = int(row.Clicks)
			}
			wbProductID := row.NmID
			phraseRow, err := s.queries.UpsertPhrase(ctx, sqlcgen.UpsertPhraseParams{
				CampaignID:  uuidToPgtype(campaignID),
				WorkspaceID: uuidToPgtype(workspaceID),
				ProductID:   uuidToPgtypePtr(&productID),
				WbProductID: int64PtrToPgInt8(&wbProductID),
				WbClusterID: pgtype.Int8{},
				WbNormQuery: normQuery,
				Keyword:     normQuery,
				Count:       intPtrToPgInt4(&count),
				CurrentBid:  pgtype.Int8{},
			})
			if err != nil {
				return savedRows, apperror.New(apperror.ErrInternal, fmt.Sprintf("upsert phrase for normquery debug: %v", err))
			}
			phraseID = uuidFromPgtype(phraseRow.ID)
			phraseIDs[key] = phraseID
		}

		stat, err := wb.MapSearchClusterStatDTO(row, phraseID)
		if err != nil {
			continue
		}
		if _, err := s.queries.UpsertPhraseStat(ctx, sqlcgen.UpsertPhraseStatParams{
			PhraseID:    uuidToPgtype(stat.PhraseID),
			Date:        pgtype.Date{Time: stat.Date, Valid: true},
			Impressions: stat.Impressions,
			Clicks:      stat.Clicks,
			Spend:       stat.Spend,
			Atbs:        int64PtrToPgInt8(stat.Atbs),
			Orders:      int64PtrToPgInt8(stat.Orders),
			Cpc:         float64PtrToPgFloat8(stat.CPC),
			Cpm:         float64PtrToPgFloat8(stat.CPM),
			AvgPos:      float64PtrToPgFloat8(stat.AvgPos),
		}); err != nil {
			return savedRows, apperror.New(apperror.ErrInternal, fmt.Sprintf("upsert phrase stat for normquery debug: %v", err))
		}
		savedRows++
	}
	return savedRows, nil
}

func (s *AdsReadService) invalidateWorkspaceCache(workspaceID uuid.UUID) {
	s.cacheMu.Lock()
	defer s.cacheMu.Unlock()
	for key := range s.dataCache {
		if strings.HasPrefix(key, workspaceID.String()+":") {
			delete(s.dataCache, key)
		}
	}
}
