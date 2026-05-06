package service

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	"golang.org/x/sync/errgroup"

	"github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/domain"
	"github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/pkg/apperror"
	sqlcgen "github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/repository/sqlc"
)

func (s *AdsReadService) loadWorkspaceData(ctx context.Context, workspaceID uuid.UUID, dateFrom, dateTo time.Time) (*adsWorkspaceData, error) {
	// Check cache first (30s TTL) — avoids re-loading for parallel frontend requests
	cacheKey := fmt.Sprintf("%s:%s:%s", workspaceID, dateFrom.Format("2006-01-02"), dateTo.Format("2006-01-02"))
	s.cacheMu.RLock()
	if cached, ok := s.dataCache[cacheKey]; ok && time.Since(cached.loadedAt) < 30*time.Second {
		s.cacheMu.RUnlock()
		return cached.data, nil
	}
	s.cacheMu.RUnlock()

	// Deduplicate concurrent loads for the same workspace+date range
	result, err, _ := s.loadGroup.Do(cacheKey, func() (interface{}, error) {
		return s.doLoadWorkspaceData(ctx, workspaceID, dateFrom, dateTo, cacheKey)
	})
	if err != nil {
		return nil, err
	}
	return result.(*adsWorkspaceData), nil
}

func (s *AdsReadService) doLoadWorkspaceData(ctx context.Context, workspaceID uuid.UUID, dateFrom, dateTo time.Time, cacheKey string) (*adsWorkspaceData, error) {
	// Double-check cache after winning the singleflight race
	s.cacheMu.RLock()
	if cached, ok := s.dataCache[cacheKey]; ok && time.Since(cached.loadedAt) < 30*time.Second {
		s.cacheMu.RUnlock()
		return cached.data, nil
	}
	s.cacheMu.RUnlock()

	// Run ALL DB queries in parallel
	g, gctx := errgroup.WithContext(ctx)

	var cabinetRows []sqlcgen.SellerCabinet
	var campaignRows []sqlcgen.Campaign
	var productRows []sqlcgen.Product
	var phraseRows []sqlcgen.Phrase
	var campaignStatRows []sqlcgen.CampaignStat
	var productStatRows []sqlcgen.ProductStat
	var phraseStatRows []sqlcgen.PhraseStat
	var campaignProductRows []sqlcgen.CampaignProduct
	var extensionEvidence *workspaceExtensionEvidence
	var lastAutoSync *domain.SellerCabinetAutoSyncSummary

	g.Go(func() error {
		var err error
		cabinetRows, err = s.queries.ListSellerCabinetsByWorkspace(gctx, sqlcgen.ListSellerCabinetsByWorkspaceParams{
			WorkspaceID: uuidToPgtype(workspaceID), Limit: s.entityLimit, Offset: 0,
		})
		return err
	})
	g.Go(func() error {
		var err error
		campaignRows, err = s.queries.ListCampaignsByWorkspace(gctx, sqlcgen.ListCampaignsByWorkspaceParams{
			WorkspaceID: workspaceUUID(workspaceID), Limit: s.entityLimit, Offset: 0,
		})
		return err
	})
	g.Go(func() error {
		var err error
		productRows, err = s.queries.ListProductsByWorkspace(gctx, sqlcgen.ListProductsByWorkspaceParams{
			WorkspaceID: workspaceUUID(workspaceID), Limit: s.entityLimit, Offset: 0,
		})
		return err
	})
	g.Go(func() error {
		var err error
		phraseRows, err = s.queries.ListPhrasesByWorkspace(gctx, sqlcgen.ListPhrasesByWorkspaceParams{
			WorkspaceID: workspaceUUID(workspaceID), Limit: s.entityLimit, Offset: 0,
		})
		return err
	})
	g.Go(func() error {
		var err error
		campaignStatRows, err = s.queries.ListCampaignStatsByWorkspaceDateRange(gctx, sqlcgen.ListCampaignStatsByWorkspaceDateRangeParams{
			WorkspaceID: workspaceUUID(workspaceID), DateFrom: dateFrom, DateTo: dateTo,
		})
		return err
	})
	g.Go(func() error {
		var err error
		productStatRows, err = s.queries.ListProductStatsByWorkspaceDateRange(gctx, sqlcgen.ListProductStatsByWorkspaceDateRangeParams{
			WorkspaceID: workspaceUUID(workspaceID), DateFrom: pgDate(dateFrom), DateTo: pgDate(dateTo),
		})
		return err
	})
	g.Go(func() error {
		var err error
		phraseStatRows, err = s.queries.ListPhraseStatsByWorkspaceDateRange(gctx, sqlcgen.ListPhraseStatsByWorkspaceDateRangeParams{
			WorkspaceID: workspaceUUID(workspaceID), DateFrom: dateFrom, DateTo: dateTo,
		})
		return err
	})
	g.Go(func() error {
		var err error
		campaignProductRows, err = s.queries.ListCampaignProductsByWorkspace(gctx, uuidToPgtype(workspaceID))
		return err
	})
	g.Go(func() error {
		ev, err := loadWorkspaceExtensionEvidence(gctx, s.queries, workspaceID, 5000) // reduced from 50K to 5K
		if err != nil {
			s.logger.Warn().Err(err).Str("workspace_id", workspaceID.String()).Msg("extension evidence load failed")
			return nil // non-fatal
		}
		extensionEvidence = ev
		return nil
	})
	g.Go(func() error {
		lastAutoSync = s.latestWorkspaceAutoSync(gctx, workspaceID)
		return nil
	})

	if err := g.Wait(); err != nil {
		return nil, apperror.New(apperror.ErrInternal, fmt.Sprintf("load workspace data: %v", err))
	}

	// Assemble data structure
	data := &adsWorkspaceData{
		cabinets:           make(map[uuid.UUID]domain.SellerCabinet, len(cabinetRows)),
		campaigns:          make([]domain.Campaign, 0, len(campaignRows)),
		products:           make([]domain.Product, 0, len(productRows)),
		phrases:            make([]domain.Phrase, 0, len(phraseRows)),
		campaignStatsByID:  make(map[uuid.UUID][]domain.CampaignStat, len(campaignRows)),
		productStatsByID:   make(map[uuid.UUID][]domain.ProductStat, len(productRows)),
		phraseStatsByID:    make(map[uuid.UUID][]domain.PhraseStat, len(phraseRows)),
		campaignProductIDs: make(map[uuid.UUID][]uuid.UUID),
		productCampaignIDs: make(map[uuid.UUID][]uuid.UUID),
		campaignPhrases:    make(map[uuid.UUID][]domain.Phrase),
		lastAutoSync:       lastAutoSync,
		extensionEvidence:  &workspaceExtensionEvidence{},
	}
	if extensionEvidence != nil {
		data.extensionEvidence = extensionEvidence
	}

	for _, row := range cabinetRows {
		cabinet := sellerCabinetFromSqlc(row)
		cabinet.LastAutoSync = data.lastAutoSync
		data.cabinets[cabinet.ID] = cabinet
	}
	for _, row := range campaignRows {
		data.campaigns = append(data.campaigns, campaignFromSqlc(row))
	}
	for _, row := range productRows {
		data.products = append(data.products, productFromSqlc(row))
	}
	for _, row := range phraseRows {
		phrase := phraseFromSqlc(row)
		data.phrases = append(data.phrases, phrase)
		data.campaignPhrases[phrase.CampaignID] = append(data.campaignPhrases[phrase.CampaignID], phrase)
	}
	for _, row := range campaignStatRows {
		stat := campaignStatFromSqlc(row)
		data.campaignStatsByID[stat.CampaignID] = append(data.campaignStatsByID[stat.CampaignID], stat)
	}
	for _, row := range productStatRows {
		stat := productStatFromSqlc(row)
		data.productStatsByID[stat.ProductID] = append(data.productStatsByID[stat.ProductID], stat)
	}
	for _, row := range phraseStatRows {
		stat := phraseStatFromSqlc(row)
		data.phraseStatsByID[stat.PhraseID] = append(data.phraseStatsByID[stat.PhraseID], stat)
	}

	s.attachCampaignProducts(ctx, data, campaignProductRows)

	// Store in cache
	s.cacheMu.Lock()
	s.dataCache[cacheKey] = cachedWorkspaceData{data: data, loadedAt: time.Now()}
	// Evict old entries
	if len(s.dataCache) > 50 {
		for k, v := range s.dataCache {
			if time.Since(v.loadedAt) > 60*time.Second {
				delete(s.dataCache, k)
			}
		}
	}
	s.cacheMu.Unlock()

	return data, nil
}

// attachCampaignProducts links products to campaigns using local DB data only.
// Uses seller_cabinet_id as the join key (products and campaigns from the same cabinet are related).
// This avoids live WB API calls on every read request (audit fix: CRITICAL — was O(cabinets) HTTP calls per read).
func (s *AdsReadService) attachCampaignProducts(_ context.Context, data *adsWorkspaceData, links []sqlcgen.CampaignProduct) {
	for _, link := range links {
		campaignID := uuidFromPgtype(link.CampaignID)
		productID := uuidFromPgtype(link.ProductID)
		data.campaignProductIDs[campaignID] = append(data.campaignProductIDs[campaignID], productID)
		data.productCampaignIDs[productID] = append(data.productCampaignIDs[productID], campaignID)
	}
}

func (data *adsWorkspaceData) lookupCampaignProducts(campaignID uuid.UUID) []domain.Product {
	ids := data.campaignProductIDs[campaignID]
	if len(ids) == 0 {
		return nil
	}
	productByID := data.productByIDMap()
	result := make([]domain.Product, 0, len(ids))
	seen := make(map[uuid.UUID]struct{}, len(ids))
	for _, id := range ids {
		if _, ok := seen[id]; ok {
			continue
		}
		seen[id] = struct{}{}
		if p, ok := productByID[id]; ok {
			result = append(result, p)
		}
	}
	return result
}

func (data *adsWorkspaceData) lookupProductCampaigns(productID uuid.UUID) []domain.Campaign {
	ids := data.productCampaignIDs[productID]
	if len(ids) == 0 {
		return nil
	}
	campaignByID := data.campaignByIDMap()
	result := make([]domain.Campaign, 0, len(ids))
	seen := make(map[uuid.UUID]struct{}, len(ids))
	for _, id := range ids {
		if _, ok := seen[id]; ok {
			continue
		}
		seen[id] = struct{}{}
		if c, ok := campaignByID[id]; ok {
			result = append(result, c)
		}
	}
	return result
}

func (data *adsWorkspaceData) productByIDMap() map[uuid.UUID]domain.Product {
	m := make(map[uuid.UUID]domain.Product, len(data.products))
	for _, p := range data.products {
		m[p.ID] = p
	}
	return m
}

func (data *adsWorkspaceData) campaignByIDMap() map[uuid.UUID]domain.Campaign {
	m := make(map[uuid.UUID]domain.Campaign, len(data.campaigns))
	for _, c := range data.campaigns {
		m[c.ID] = c
	}
	return m
}

func containsID(ids []uuid.UUID, target uuid.UUID) bool {
	for _, id := range ids {
		if id == target {
			return true
		}
	}
	return false
}
