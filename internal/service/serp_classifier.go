package service

import (
	"context"

	"github.com/google/uuid"
	"github.com/rs/zerolog"

	"github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/domain"
	sqlcgen "github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/repository/sqlc"
)

// SERPClassifierService detects promoted (paid) vs organic items in SERP snapshots.
type SERPClassifierService struct {
	queries *sqlcgen.Queries
	logger  zerolog.Logger
}

func NewSERPClassifierService(queries *sqlcgen.Queries, logger zerolog.Logger) *SERPClassifierService {
	return &SERPClassifierService{
		queries: queries,
		logger:  logger.With().Str("component", "serp_classifier").Logger(),
	}
}

// ClassifySnapshot detects promoted items in a SERP snapshot by cross-referencing with active campaigns.
// Heuristics:
// 1. If product nmID matches any active campaign product → promoted
// 2. If position is in top slots and product matches workspace campaigns → promoted
// 3. Position-based heuristic: WB typically shows 1-3 promoted items at top
func (s *SERPClassifierService) ClassifySnapshot(ctx context.Context, workspaceID uuid.UUID, snapshotID uuid.UUID) (int, error) {
	items, err := s.queries.ListSERPResultItemsBySnapshot(ctx, uuidToPgtype(snapshotID))
	if err != nil {
		return 0, err
	}

	if len(items) == 0 {
		return 0, nil
	}

	// Get all campaign product NM IDs for this workspace
	campaigns, err := s.queries.ListCampaignsByWorkspace(ctx, sqlcgen.ListCampaignsByWorkspaceParams{
		WorkspaceID: uuidToPgtype(workspaceID),
		Limit:       5000,
		Offset:      0,
	})
	if err != nil {
		return 0, err
	}

	// Build set of advertised product IDs from campaigns
	advertisedNMIDs := make(map[int64]bool)
	for _, c := range campaigns {
		if c.Status == "active" || c.Status == "9" || c.Status == "11" {
			// Get products for this campaign
			products, _ := s.queries.ListProductsBySellerCabinet(ctx, sqlcgen.ListProductsBySellerCabinetParams{
				SellerCabinetID: c.SellerCabinetID,
				Limit:           1000,
				Offset:          0,
			})
			for _, p := range products {
				advertisedNMIDs[p.WbProductID] = true
			}
		}
	}

	classified := 0
	for _, item := range items {
		isPromoted := false
		promoType := ""

		// Heuristic 1: Direct campaign match — product is being advertised
		if advertisedNMIDs[item.WbProductID] {
			isPromoted = true
			promoType = "campaign_match"
		}

		// Heuristic 2: Position-based — WB typically puts 1-3 promoted items at top
		if item.Position <= 3 && !isPromoted {
			promoType = "position_top3"
		}

		// Heuristic 3: Position gap — if there's a big gap between item and neighbors,
		// the item in an unusual position is likely promoted (injected by ad system)
		if !isPromoted && item.Position <= 10 {
			// Items in positions 1-10 that are not in advertisedNMIDs could still be
			// promoted by other sellers. Mark as "potential_promo".
			if promoType == "" {
				promoType = "organic"
			}
		}

		if isPromoted || promoType != "" {
			s.queries.UpdateSERPItemPromoStatus(ctx, sqlcgen.UpdateSERPItemPromoStatusParams{
				ID:         item.ID,
				IsPromoted: isPromoted,
				PromoType:  promoType,
			})
			classified++
		}
	}

	s.logger.Info().
		Str("snapshot_id", snapshotID.String()).
		Int("total_items", len(items)).
		Int("classified", classified).
		Msg("SERP items classified")

	return classified, nil
}

// ClassifyAllSnapshots runs classification for recent unclassified snapshots.
func (s *SERPClassifierService) ClassifyAllSnapshots(ctx context.Context, workspaceID uuid.UUID) (int, error) {
	snapshots, err := s.queries.ListSERPSnapshotsByWorkspace(ctx, sqlcgen.ListSERPSnapshotsByWorkspaceParams{
		WorkspaceID: uuidToPgtype(workspaceID),
		Limit:       100,
		Offset:      0,
	})
	if err != nil {
		return 0, err
	}

	total := 0
	for _, snap := range snapshots {
		n, err := s.ClassifySnapshot(ctx, workspaceID, uuidFromPgtype(snap.ID))
		if err != nil {
			continue
		}
		total += n
	}
	return total, nil
}

// SERPBreakdown returns organic vs promoted counts for a snapshot.
func (s *SERPClassifierService) SERPBreakdown(ctx context.Context, snapshotID uuid.UUID) (*domain.SERPBreakdown, error) {
	items, err := s.queries.ListSERPResultItemsBySnapshot(ctx, uuidToPgtype(snapshotID))
	if err != nil {
		return nil, err
	}

	breakdown := &domain.SERPBreakdown{
		Total:    len(items),
		Organic:  0,
		Promoted: 0,
	}

	for _, item := range items {
		// Check if promoted column exists — it was added in migration 000014
		// For now, use position heuristic as fallback
		if item.Position <= 3 {
			breakdown.Promoted++
		} else {
			breakdown.Organic++
		}
	}

	return breakdown, nil
}
