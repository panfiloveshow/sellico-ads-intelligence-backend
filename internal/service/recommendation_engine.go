package service

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/rs/zerolog"

	"github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/domain"
	"github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/pkg/metrics"
	sqlcgen "github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/repository/sqlc"
)

// Notifier is called after recommendations are generated.
type Notifier interface {
	NotifyNewRecommendations(ctx context.Context, workspaceID uuid.UUID, recommendations []domain.Recommendation)
}

// RecommendationEngine generates explainable recommendations from stored analytics.
type RecommendationEngine struct {
	queries         *sqlcgen.Queries
	recommendations *RecommendationService
	notifier        Notifier
	logger          zerolog.Logger
}

func NewRecommendationEngine(queries *sqlcgen.Queries, recommendations *RecommendationService, notifier Notifier, logger zerolog.Logger) *RecommendationEngine {
	return &RecommendationEngine{
		queries:         queries,
		recommendations: recommendations,
		notifier:        notifier,
		logger:          logger,
	}
}

// loadThresholds loads per-workspace thresholds, falling back to defaults.
func (e *RecommendationEngine) loadThresholds(ctx context.Context, workspaceID uuid.UUID) domain.RecommendationThresholds {
	raw, err := e.queries.GetWorkspaceSettings(ctx, uuidToPgtype(workspaceID))
	if err != nil || len(raw) == 0 || string(raw) == "{}" {
		return domain.DefaultThresholds()
	}

	var settings domain.WorkspaceSettings
	if err := json.Unmarshal(raw, &settings); err != nil {
		return domain.DefaultThresholds()
	}

	return settings.RecommendationThresholds.Merged()
}

func (e *RecommendationEngine) GenerateForWorkspace(ctx context.Context, workspaceID uuid.UUID) ([]domain.Recommendation, error) {
	var generated []domain.Recommendation
	th := e.loadThresholds(ctx, workspaceID)

	campaigns, err := e.queries.ListCampaignsByWorkspace(ctx, sqlcgen.ListCampaignsByWorkspaceParams{
		WorkspaceID: uuidToPgtype(workspaceID),
		Limit:       1000,
		Offset:      0,
	})
	if err != nil {
		return nil, err
	}

	// Preload latest stats for all campaigns in one query (avoid N+1).
	campaignStatsBatch, err := e.queries.GetLatestCampaignStatsBatch(ctx, uuidToPgtype(workspaceID))
	if err != nil {
		return nil, err
	}
	campaignStatMap := make(map[uuid.UUID]sqlcgen.CampaignStat, len(campaignStatsBatch))
	for _, cs := range campaignStatsBatch {
		campaignStatMap[uuidFromPgtype(cs.CampaignID)] = cs
	}

	for _, campaign := range campaigns {
		stat, ok := campaignStatMap[uuidFromPgtype(campaign.ID)]
		if !ok {
			continue
		}

		campaignID := nullableUUID(uuidFromPgtype(campaign.ID))

		if stat.Impressions >= int64(th.CampaignHighImpressions) && stat.Clicks == 0 {
			rec, upsertErr := e.recommendations.UpsertActive(ctx, RecommendationUpsertInput{
				WorkspaceID: workspaceID,
				CampaignID:  campaignID,
				Title:       "Много показов без кликов",
				Description: fmt.Sprintf("Кампания %q собрала %d показов, но 0 кликов за %s.", campaign.Name, stat.Impressions, stat.Date.Time.Format("2006-01-02")),
				Type:        domain.RecommendationTypeLowCTR,
				Severity:    domain.SeverityHigh,
				Confidence:  0.84,
				SourceMetrics: map[string]any{
					"impressions": stat.Impressions,
					"clicks":      stat.Clicks,
					"date":        stat.Date.Time.Format("2006-01-02"),
				},
				NextAction: strPtr("Проверьте релевантность листинга и улучшите CTR прежде чем увеличивать расход."),
			})
			if upsertErr != nil {
				return nil, upsertErr
			}
			generated = append(generated, *rec)
		} else if closeErr := e.recommendations.CloseActive(ctx, workspaceID, domain.RecommendationTypeLowCTR, campaignID, nil, nil); closeErr != nil {
			return nil, closeErr
		}

		orders := nullableInt64(stat.Orders)
		if stat.Clicks >= int64(th.CampaignZeroOrdersClick) && (orders == nil || *orders == 0) {
			rec, upsertErr := e.recommendations.UpsertActive(ctx, RecommendationUpsertInput{
				WorkspaceID: workspaceID,
				CampaignID:  campaignID,
				Title:       "Клики не конвертируются в заказы",
				Description: fmt.Sprintf("Кампания %q получила %d кликов, но 0 заказов за %s.", campaign.Name, stat.Clicks, stat.Date.Time.Format("2006-01-02")),
				Type:        domain.RecommendationTypeHighSpendLowOrders,
				Severity:    domain.SeverityHigh,
				Confidence:  0.81,
				SourceMetrics: map[string]any{
					"clicks": stat.Clicks,
					"orders": 0,
					"spend":  stat.Spend,
					"date":   stat.Date.Time.Format("2006-01-02"),
				},
				NextAction: strPtr("Проверьте поисковые запросы и качество конверсии; рассмотрите паузу слабого трафика."),
			})
			if upsertErr != nil {
				return nil, upsertErr
			}
			generated = append(generated, *rec)
		} else if closeErr := e.recommendations.CloseActive(ctx, workspaceID, domain.RecommendationTypeHighSpendLowOrders, campaignID, nil, nil); closeErr != nil {
			return nil, closeErr
		}

		// Mutual exclusion: evaluate lower_bid vs raise_bid — only one can be active per campaign.
		lowerBidFired := false
		raiseBidFired := false

		if stat.Clicks > 0 {
			cpc := float64(stat.Spend) / float64(stat.Clicks)
			cpo := th.CampaignPoorCPO + 1
			if orders != nil && *orders > 0 {
				cpo = float64(stat.Spend) / float64(*orders)
			}
			if cpc >= th.CampaignHighCPC && cpo >= th.CampaignPoorCPO {
				rec, upsertErr := e.recommendations.UpsertActive(ctx, RecommendationUpsertInput{
					WorkspaceID: workspaceID,
					CampaignID:  campaignID,
					Title:       "Ставка кампании слишком высока для текущей отдачи",
					Description: fmt.Sprintf("Кампания %q: CPC %.2f, CPO %.2f — неэффективный расход при текущей ставке.", campaign.Name, cpc, cpo),
					Type:        domain.RecommendationTypeLowerBid,
					Severity:    domain.SeverityMedium,
					Confidence:  0.76,
					SourceMetrics: map[string]any{
						"cpc":    cpc,
						"cpo":    cpo,
						"clicks": stat.Clicks,
						"spend":  stat.Spend,
					},
					NextAction: strPtr("Снизьте ставки или приостановите дорогие размещения с низкой отдачей."),
				})
				if upsertErr != nil {
					return nil, upsertErr
				}
				generated = append(generated, *rec)
				lowerBidFired = true
			} else if closeErr := e.recommendations.CloseActive(ctx, workspaceID, domain.RecommendationTypeLowerBid, campaignID, nil, nil); closeErr != nil {
				return nil, closeErr
			}
		} else if closeErr := e.recommendations.CloseActive(ctx, workspaceID, domain.RecommendationTypeLowerBid, campaignID, nil, nil); closeErr != nil {
			return nil, closeErr
		}

		revenue := nullableInt64(stat.Revenue)
		if !lowerBidFired && stat.Spend > 0 && stat.Clicks >= int64(th.CampaignRaiseBidClicks) && orders != nil && *orders >= int64(th.CampaignRaiseBidOrders) && revenue != nil {
			roas := float64(*revenue) / float64(stat.Spend)
			if roas >= th.CampaignStrongROAS {
				severity := domain.SeverityMedium
				if roas >= th.CampaignStrongROAS*1.5 {
					severity = domain.SeverityHigh
				}

				rec, upsertErr := e.recommendations.UpsertActive(ctx, RecommendationUpsertInput{
					WorkspaceID: workspaceID,
					CampaignID:  campaignID,
					Title:       "Кампания конвертирует эффективно — можно масштабировать",
					Description: fmt.Sprintf("Кампания %q: %d заказов, ROAS %.2f за %s — есть потенциал для масштабирования.", campaign.Name, *orders, roas, stat.Date.Time.Format("2006-01-02")),
					Type:        domain.RecommendationTypeRaiseBid,
					Severity:    severity,
					Confidence:  0.75,
					SourceMetrics: map[string]any{
						"clicks":  stat.Clicks,
						"orders":  *orders,
						"spend":   stat.Spend,
						"revenue": *revenue,
						"roas":    roas,
					},
					NextAction: strPtr("Осторожно повышайте ставки на лучших размещениях, контролируя CPC и качество конверсии."),
				})
				if upsertErr != nil {
					return nil, upsertErr
				}
				generated = append(generated, *rec)
				raiseBidFired = true
			} else if closeErr := e.recommendations.CloseActive(ctx, workspaceID, domain.RecommendationTypeRaiseBid, campaignID, nil, nil); closeErr != nil {
				return nil, closeErr
			}
		} else if closeErr := e.recommendations.CloseActive(ctx, workspaceID, domain.RecommendationTypeRaiseBid, campaignID, nil, nil); closeErr != nil {
			return nil, closeErr
		}

		// Mutual exclusion enforcement: if lower_bid fired, close any stale raise_bid (and vice versa)
		if lowerBidFired {
			if closeErr := e.recommendations.CloseActive(ctx, workspaceID, domain.RecommendationTypeRaiseBid, campaignID, nil, nil); closeErr != nil {
				return nil, closeErr
			}
		}
		if raiseBidFired {
			if closeErr := e.recommendations.CloseActive(ctx, workspaceID, domain.RecommendationTypeLowerBid, campaignID, nil, nil); closeErr != nil {
				return nil, closeErr
			}
		}
		_ = raiseBidFired // prevent unused warning
	}

	phrases, err := e.queries.ListPhrasesByWorkspace(ctx, sqlcgen.ListPhrasesByWorkspaceParams{
		WorkspaceID: uuidToPgtype(workspaceID),
		Limit:       1000,
		Offset:      0,
	})
	if err != nil {
		return nil, err
	}

	// Preload latest stats and bids for all phrases in batch (avoid 2N+1).
	phraseStatsBatch, err := e.queries.GetLatestPhraseStatsBatch(ctx, uuidToPgtype(workspaceID))
	if err != nil {
		return nil, err
	}
	phraseStatMap := make(map[uuid.UUID]sqlcgen.PhraseStat, len(phraseStatsBatch))
	for _, ps := range phraseStatsBatch {
		phraseStatMap[uuidFromPgtype(ps.PhraseID)] = ps
	}

	bidsBatch, err := e.queries.GetLatestBidSnapshotsBatch(ctx, uuidToPgtype(workspaceID))
	if err != nil {
		return nil, err
	}
	bidMap := make(map[uuid.UUID]sqlcgen.BidSnapshot, len(bidsBatch))
	for _, bs := range bidsBatch {
		bidMap[uuidFromPgtype(bs.PhraseID)] = bs
	}

	for _, phrase := range phrases {
		phraseID := nullableUUID(uuidFromPgtype(phrase.ID))
		stat, hasStat := phraseStatMap[uuidFromPgtype(phrase.ID)]
		if !hasStat {
			if closeErr := e.recommendations.CloseActive(ctx, workspaceID, domain.RecommendationTypeLowCTR, nil, phraseID, nil); closeErr != nil {
				return nil, closeErr
			}
			if closeErr := e.recommendations.CloseActive(ctx, workspaceID, domain.RecommendationTypeLowerBid, nil, phraseID, nil); closeErr != nil {
				return nil, closeErr
			}
			if closeErr := e.recommendations.CloseActive(ctx, workspaceID, domain.RecommendationTypeRaiseBid, nil, phraseID, nil); closeErr != nil {
				return nil, closeErr
			}
			continue
		}
		if stat.Impressions >= int64(th.PhraseHighImpressions) && stat.Clicks == 0 {
			severity := domain.SeverityMedium
			if stat.Impressions >= int64(th.PhraseHighImpressions)*2 {
				severity = domain.SeverityHigh
			}

			rec, upsertErr := e.recommendations.UpsertActive(ctx, RecommendationUpsertInput{
				WorkspaceID: workspaceID,
				PhraseID:    phraseID,
				Title:       "Фраза собирает показы без кликов",
				Description: fmt.Sprintf("Фраза %q: %d показов без кликов за %s.", phrase.Keyword, stat.Impressions, stat.Date.Time.Format("2006-01-02")),
				Type:        domain.RecommendationTypeLowCTR,
				Severity:    severity,
				Confidence:  0.79,
				SourceMetrics: map[string]any{
					"keyword":     phrase.Keyword,
					"impressions": stat.Impressions,
					"clicks":      stat.Clicks,
					"count":       nullableInt32(phrase.Count),
				},
				NextAction: strPtr("Проверьте релевантность кластера запросов, улучшите CTR листинга или снизьте ставку."),
			})
			if upsertErr != nil {
				return nil, upsertErr
			}
			generated = append(generated, *rec)
		} else if closeErr := e.recommendations.CloseActive(ctx, workspaceID, domain.RecommendationTypeLowCTR, nil, phraseID, nil); closeErr != nil {
			return nil, closeErr
		}

		bid, hasBid := bidMap[uuidFromPgtype(phrase.ID)]
		if !hasBid {
			if closeErr := e.recommendations.CloseActive(ctx, workspaceID, domain.RecommendationTypeLowerBid, nil, phraseID, nil); closeErr != nil {
				return nil, closeErr
			}
			if closeErr := e.recommendations.CloseActive(ctx, workspaceID, domain.RecommendationTypeRaiseBid, nil, phraseID, nil); closeErr != nil {
				return nil, closeErr
			}
			continue
		}
		currentBid := int64(0)
		if phrase.CurrentBid.Valid {
			currentBid = phrase.CurrentBid.Int64
		}

		// Phrase-level mutual exclusion: lower_bid vs raise_bid
		phraseLowerFired := false
		phraseRaiseFired := false

		if stat.Impressions >= int64(th.PhraseHighImpressions) && stat.Clicks == 0 && currentBid > 0 && bid.CompetitiveBid > 0 && currentBid > bid.CompetitiveBid {
			rec, upsertErr := e.recommendations.UpsertActive(ctx, RecommendationUpsertInput{
				WorkspaceID: workspaceID,
				PhraseID:    phraseID,
				Title:       "Ставка фразы слишком высока при низкой вовлечённости",
				Description: fmt.Sprintf("Фраза %q: %d показов, 0 кликов, текущая ставка (%d) выше конкурентной ставки (%d).", phrase.Keyword, stat.Impressions, currentBid, bid.CompetitiveBid),
				Type:        domain.RecommendationTypeLowerBid,
				Severity:    domain.SeverityMedium,
				Confidence:  0.77,
				SourceMetrics: map[string]any{
					"keyword":         phrase.Keyword,
					"impressions":     stat.Impressions,
					"clicks":          stat.Clicks,
					"current_bid":     currentBid,
					"competitive_bid": bid.CompetitiveBid,
					"leadership_bid":  bid.LeadershipBid,
				},
				NextAction: strPtr("Снизьте ставку фразы или приостановите кластер до улучшения релевантности."),
			})
			if upsertErr != nil {
				return nil, upsertErr
			}
			generated = append(generated, *rec)
			phraseLowerFired = true
		} else if closeErr := e.recommendations.CloseActive(ctx, workspaceID, domain.RecommendationTypeLowerBid, nil, phraseID, nil); closeErr != nil {
			return nil, closeErr
		}

		if !phraseLowerFired && stat.Clicks >= int64(th.PhraseBidRaiseClicks) && bid.CompetitiveBid > currentBid {
			rec, upsertErr := e.recommendations.UpsertActive(ctx, RecommendationUpsertInput{
				WorkspaceID: workspaceID,
				PhraseID:    phraseID,
				Title:       "Конкурентная ставка выше текущей ставки фразы",
				Description: fmt.Sprintf("Фраза %q даёт клики, но конкурентная ставка (%d) выше текущей ставки (%d).", phrase.Keyword, bid.CompetitiveBid, currentBid),
				Type:        domain.RecommendationTypeRaiseBid,
				Severity:    domain.SeverityMedium,
				Confidence:  0.72,
				SourceMetrics: map[string]any{
					"keyword":         phrase.Keyword,
					"clicks":          stat.Clicks,
					"current_bid":     currentBid,
					"competitive_bid": bid.CompetitiveBid,
					"leadership_bid":  bid.LeadershipBid,
				},
				NextAction: strPtr("Повысьте ставку фразы до конкурентного диапазона, если трафик прибыльный."),
			})
			if upsertErr != nil {
				return nil, upsertErr
			}
			generated = append(generated, *rec)
			phraseRaiseFired = true
		} else if closeErr := e.recommendations.CloseActive(ctx, workspaceID, domain.RecommendationTypeRaiseBid, nil, phraseID, nil); closeErr != nil {
			return nil, closeErr
		}

		// Mutual exclusion: close conflicting recommendation
		if phraseLowerFired {
			if closeErr := e.recommendations.CloseActive(ctx, workspaceID, domain.RecommendationTypeRaiseBid, nil, phraseID, nil); closeErr != nil {
				return nil, closeErr
			}
		}
		if phraseRaiseFired {
			if closeErr := e.recommendations.CloseActive(ctx, workspaceID, domain.RecommendationTypeLowerBid, nil, phraseID, nil); closeErr != nil {
				return nil, closeErr
			}
		}
	}

	products, err := e.queries.ListProductsByWorkspace(ctx, sqlcgen.ListProductsByWorkspaceParams{
		WorkspaceID: uuidToPgtype(workspaceID),
		Limit:       1000,
		Offset:      0,
	})
	if err != nil {
		return nil, err
	}
	productByID := make(map[uuid.UUID]sqlcgen.Product, len(products))
	for _, product := range products {
		productByID[uuidFromPgtype(product.ID)] = product
	}

	snapshots, err := e.queries.ListSERPSnapshotsByWorkspace(ctx, sqlcgen.ListSERPSnapshotsByWorkspaceParams{
		WorkspaceID: uuidToPgtype(workspaceID),
		Limit:       500,
		Offset:      0,
	})
	if err != nil {
		return nil, err
	}

	type serpPair struct {
		latest   sqlcgen.SerpSnapshot
		previous sqlcgen.SerpSnapshot
	}
	pairs := make(map[string]serpPair)
	for _, snapshot := range snapshots {
		key := snapshot.Query + "|" + snapshot.Region
		existing, ok := pairs[key]
		if !ok {
			pairs[key] = serpPair{latest: snapshot}
			continue
		}
		if !existing.previous.ID.Valid {
			existing.previous = snapshot
			pairs[key] = existing
		}
	}

	serpItemsCache := make(map[uuid.UUID][]sqlcgen.SerpResultItem)
	loadSERPItems := func(snapshotID pgtype.UUID) ([]sqlcgen.SerpResultItem, error) {
		id := uuidFromPgtype(snapshotID)
		if items, ok := serpItemsCache[id]; ok {
			return items, nil
		}
		items, loadErr := e.queries.ListSERPResultItemsBySnapshot(ctx, snapshotID)
		if loadErr != nil {
			return nil, loadErr
		}
		serpItemsCache[id] = items
		return items, nil
	}

	type positionDropCandidate struct {
		input RecommendationUpsertInput
		score int
	}

	targets, err := e.queries.ListPositionTrackingTargetsFiltered(ctx, sqlcgen.ListPositionTrackingTargetsFilteredParams{
		WorkspaceID:     uuidToPgtype(workspaceID),
		Limit:           5000,
		Offset:          0,
		ProductIDFilter: pgtype.UUID{},
		QueryFilter:     pgtype.Text{},
		RegionFilter:    pgtype.Text{},
		ActiveFilter:    pgtype.Bool{Bool: true, Valid: true},
	})
	if err != nil {
		return nil, err
	}

	bestPositionDropByProduct := make(map[uuid.UUID]positionDropCandidate)
	for _, target := range targets {
		productUUID := uuidFromPgtype(target.ProductID)
		product, ok := productByID[productUUID]
		if !ok || !target.BaselinePosition.Valid {
			continue
		}

		positions, posErr := e.queries.ListPositionsFiltered(ctx, sqlcgen.ListPositionsFilteredParams{
			WorkspaceID:     uuidToPgtype(workspaceID),
			Limit:           20,
			Offset:          0,
			ProductIDFilter: target.ProductID,
			QueryFilter:     textToPgtype(target.Query),
			RegionFilter:    textToPgtype(target.Region),
			DateFrom:        pgtype.Timestamptz{},
			DateTo:          pgtype.Timestamptz{},
		})
		if posErr != nil {
			return nil, posErr
		}
		if len(positions) == 0 {
			continue
		}

		latest := positions[0]
		baselinePosition := int(target.BaselinePosition.Int32)
		currentPosition := int(latest.Position)
		delta := currentPosition - baselinePosition
		if delta < th.PositionDropThreshold {
			continue
		}

		serpPressure := false
		serpOwnLost := false
		topCompetitorTitle := ""
		var latestSERPSnapshotID uuid.UUID
		var previousSERPSnapshotID uuid.UUID
		if pair, ok := pairs[target.Query+"|"+target.Region]; ok && pair.previous.ID.Valid {
			latestSERPSnapshotID = uuidFromPgtype(pair.latest.ID)
			previousSERPSnapshotID = uuidFromPgtype(pair.previous.ID)

			latestItems, itemsErr := loadSERPItems(pair.latest.ID)
			if itemsErr != nil {
				return nil, itemsErr
			}
			previousItems, itemsErr := loadSERPItems(pair.previous.ID)
			if itemsErr != nil {
				return nil, itemsErr
			}

			previousSERPPosition, foundPreviously := findSERPItemPosition(previousItems, product.WbProductID)
			currentSERPPosition, foundNow := findSERPItemPosition(latestItems, product.WbProductID)
			serpOwnLost = foundPreviously && !foundNow
			serpPressure = serpOwnLost || (foundPreviously && foundNow && currentSERPPosition > previousSERPPosition)
			if len(latestItems) > 0 {
				topCompetitorTitle = latestItems[0].Title
			}
		}

		severity := domain.SeverityMedium
		confidence := 0.78
		score := delta
		if delta >= 20 || serpPressure {
			severity = domain.SeverityHigh
			confidence = 0.86
			score += 20
		}
		if serpOwnLost {
			score += 30
		}

		description := fmt.Sprintf("Товар %q упал с позиции %d до %d по запросу %q в регионе %q.", product.Title, baselinePosition, currentPosition, target.Query, target.Region)
		if serpOwnLost {
			description = fmt.Sprintf("%s Товар также исчез из последнего снимка выдачи.", description)
		} else if serpPressure && topCompetitorTitle != "" {
			description = fmt.Sprintf("%s Лидер выдачи сейчас: %q.", description, topCompetitorTitle)
		}

		candidate := positionDropCandidate{
			input: RecommendationUpsertInput{
				WorkspaceID: workspaceID,
				ProductID:   nullableUUID(productUUID),
				Title:       "Позиция в поиске упала",
				Description: description,
				Type:        domain.RecommendationTypePositionDrop,
				Severity:    severity,
				Confidence:  confidence,
				SourceMetrics: map[string]any{
					"product_title":          product.Title,
					"query":                  target.Query,
					"region":                 target.Region,
					"baseline_position":      baselinePosition,
					"current_position":       currentPosition,
					"delta":                  delta,
					"baseline_checked_at":    target.BaselineCheckedAt.Time,
					"latest_checked_at":      latest.CheckedAt.Time,
					"sample_count":           len(positions),
					"serp_pressure":          serpPressure,
					"serp_own_lost":          serpOwnLost,
					"top_competitor_title":   topCompetitorTitle,
					"latest_serp_snapshot":   latestSERPSnapshotID,
					"previous_serp_snapshot": previousSERPSnapshotID,
				},
				NextAction: strPtr("Откройте позиции и сравнение SERP, определите причину: давление конкурентов, слабая ставка или проблема с релевантностью."),
			},
			score: score,
		}

		existing, ok := bestPositionDropByProduct[productUUID]
		if !ok || candidate.score > existing.score {
			bestPositionDropByProduct[productUUID] = candidate
		}
	}

	for _, product := range products {
		productID := nullableUUID(uuidFromPgtype(product.ID))
		candidate, ok := bestPositionDropByProduct[uuidFromPgtype(product.ID)]
		if !ok {
			if closeErr := e.recommendations.CloseActive(ctx, workspaceID, domain.RecommendationTypePositionDrop, nil, nil, productID); closeErr != nil {
				return nil, closeErr
			}
			continue
		}
		rec, upsertErr := e.recommendations.UpsertActive(ctx, candidate.input)
		if upsertErr != nil {
			return nil, upsertErr
		}
		generated = append(generated, *rec)
	}

	for _, product := range products {
		productID := nullableUUID(uuidFromPgtype(product.ID))
		competitorDetected := false

		for _, pair := range pairs {
			if !pair.previous.ID.Valid {
				continue
			}

			latestItems, itemsErr := loadSERPItems(pair.latest.ID)
			if itemsErr != nil {
				return nil, itemsErr
			}
			previousItems, itemsErr := loadSERPItems(pair.previous.ID)
			if itemsErr != nil {
				return nil, itemsErr
			}

			previousPosition, foundPreviously := findSERPItemPosition(previousItems, product.WbProductID)
			if !foundPreviously || previousPosition > th.SERPCompetitorTop {
				continue
			}
			if _, foundNow := findSERPItemPosition(latestItems, product.WbProductID); foundNow {
				continue
			}

			competitorTitle := ""
			competitorProductID := int64(0)
			if len(latestItems) > 0 {
				competitorTitle = latestItems[0].Title
				competitorProductID = latestItems[0].WbProductID
			}

			severity := domain.SeverityMedium
			if previousPosition <= 3 {
				severity = domain.SeverityHigh
			}

			description := fmt.Sprintf("Товар %q исчез из выдачи по запросу %q в регионе %q, ранее занимал позицию %d.", product.Title, pair.latest.Query, pair.latest.Region, previousPosition)
			if competitorTitle != "" {
				description = fmt.Sprintf("%s Лидер выдачи сейчас: %q.", description, competitorTitle)
			}

			rec, upsertErr := e.recommendations.UpsertActive(ctx, RecommendationUpsertInput{
				WorkspaceID: workspaceID,
				ProductID:   productID,
				Title:       "Новый конкурент вытеснил товар из выдачи",
				Description: description,
				Type:        domain.RecommendationTypeNewCompetitor,
				Severity:    severity,
				Confidence:  0.74,
				SourceMetrics: map[string]any{
					"product_title":          product.Title,
					"query":                  pair.latest.Query,
					"region":                 pair.latest.Region,
					"previous_position":      previousPosition,
					"latest_snapshot_id":     uuidFromPgtype(pair.latest.ID),
					"previous_snapshot_id":   uuidFromPgtype(pair.previous.ID),
					"top_competitor_title":   competitorTitle,
					"top_competitor_nm_id":   competitorProductID,
					"latest_total_results":   pair.latest.TotalResults,
					"previous_total_results": pair.previous.TotalResults,
				},
				NextAction: strPtr("Откройте сравнение SERP по этому запросу, изучите нового конкурента и решите: повысить ставку, улучшить контент или скорректировать ассортимент."),
			})
			if upsertErr != nil {
				return nil, upsertErr
			}
			generated = append(generated, *rec)
			competitorDetected = true
			break
		}

		if !competitorDetected {
			if closeErr := e.recommendations.CloseActive(ctx, workspaceID, domain.RecommendationTypeNewCompetitor, nil, nil, productID); closeErr != nil {
				return nil, closeErr
			}
		}
	}

	e.logger.Info().
		Str("workspace_id", workspaceID.String()).
		Int("recommendations", len(generated)).
		Msg("generated recommendations")

	metrics.RecommendationsGenerated.Add(float64(len(generated)))

	if e.notifier != nil && len(generated) > 0 {
		e.notifier.NotifyNewRecommendations(ctx, workspaceID, generated)
	}

	return generated, nil
}

func strPtr(v string) *string {
	return &v
}

func findSERPItemPosition(items []sqlcgen.SerpResultItem, wbProductID int64) (int, bool) {
	for _, item := range items {
		if item.WbProductID == wbProductID {
			return int(item.Position), true
		}
	}
	return 0, false
}

func nullableInt32(value pgtype.Int4) *int32 {
	if !value.Valid {
		return nil
	}
	result := value.Int32
	return &result
}
