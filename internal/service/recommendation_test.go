package service

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/rs/zerolog"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/domain"
	sqlcgen "github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/repository/sqlc"
)

type recommendationInMemDB struct {
	recommendations map[uuid.UUID]sqlcgen.Recommendation
	campaigns       []sqlcgen.Campaign
	campaignStats   map[uuid.UUID]sqlcgen.CampaignStat
	phrases         []sqlcgen.Phrase
	phraseStats     map[uuid.UUID]sqlcgen.PhraseStat
	bidSnapshots    map[uuid.UUID]sqlcgen.BidSnapshot
	products        []sqlcgen.Product
	positions       map[uuid.UUID][]sqlcgen.Position
	trackingTargets []sqlcgen.PositionTrackingTarget
	serpSnapshots   []sqlcgen.SerpSnapshot
	serpItems       map[uuid.UUID][]sqlcgen.SerpResultItem
}

func newRecommendationInMemDB() *recommendationInMemDB {
	return &recommendationInMemDB{
		recommendations: make(map[uuid.UUID]sqlcgen.Recommendation),
		campaignStats:   make(map[uuid.UUID]sqlcgen.CampaignStat),
		phraseStats:     make(map[uuid.UUID]sqlcgen.PhraseStat),
		bidSnapshots:    make(map[uuid.UUID]sqlcgen.BidSnapshot),
		positions:       make(map[uuid.UUID][]sqlcgen.Position),
		serpItems:       make(map[uuid.UUID][]sqlcgen.SerpResultItem),
	}
}

func (db *recommendationInMemDB) Exec(_ context.Context, _ string, _ ...interface{}) (pgconn.CommandTag, error) {
	return pgconn.NewCommandTag(""), nil
}

func (db *recommendationInMemDB) CopyFrom(_ context.Context, _ pgx.Identifier, _ []string, _ pgx.CopyFromSource) (int64, error) {
	return 0, nil
}

func (db *recommendationInMemDB) Query(_ context.Context, sql string, args ...interface{}) (pgx.Rows, error) {
	switch {
	case containsSQL(sql, "FROM campaigns") && containsSQL(sql, "WHERE workspace_id"):
		workspaceID := uuidFromPgtype(args[0].(pgtype.UUID))
		var items []sqlcgen.Campaign
		for _, item := range db.campaigns {
			if uuidFromPgtype(item.WorkspaceID) == workspaceID {
				items = append(items, item)
			}
		}
		return &campaignRows{items: items}, nil
	case containsSQL(sql, "FROM phrases") && containsSQL(sql, "WHERE workspace_id"):
		workspaceID := uuidFromPgtype(args[0].(pgtype.UUID))
		var items []sqlcgen.Phrase
		for _, item := range db.phrases {
			if uuidFromPgtype(item.WorkspaceID) == workspaceID {
				items = append(items, item)
			}
		}
		return &phraseRows{items: items}, nil
	case containsSQL(sql, "FROM products") && containsSQL(sql, "WHERE workspace_id"):
		workspaceID := uuidFromPgtype(args[0].(pgtype.UUID))
		var items []sqlcgen.Product
		for _, item := range db.products {
			if uuidFromPgtype(item.WorkspaceID) != workspaceID {
				continue
			}
			items = append(items, item)
		}
		return &productRows{items: items}, nil
	case containsSQL(sql, "FROM recommendations") && containsSQL(sql, "ORDER BY created_at DESC"):
		workspaceID := uuidFromPgtype(args[0].(pgtype.UUID))
		campaignID := uuidFromPgtype(args[3].(pgtype.UUID))
		phraseID := uuidFromPgtype(args[4].(pgtype.UUID))
		productID := uuidFromPgtype(args[5].(pgtype.UUID))
		recType := textValue(args[6].(pgtype.Text))
		severity := textValue(args[7].(pgtype.Text))
		status := textValue(args[8].(pgtype.Text))
		var items []sqlcgen.Recommendation
		for _, item := range db.recommendations {
			if uuidFromPgtype(item.WorkspaceID) != workspaceID {
				continue
			}
			if campaignID != uuid.Nil && uuidFromPgtype(item.CampaignID) != campaignID {
				continue
			}
			if phraseID != uuid.Nil && uuidFromPgtype(item.PhraseID) != phraseID {
				continue
			}
			if productID != uuid.Nil && uuidFromPgtype(item.ProductID) != productID {
				continue
			}
			if recType != "" && item.Type != recType {
				continue
			}
			if severity != "" && item.Severity != severity {
				continue
			}
			if status != "" && item.Status != status {
				continue
			}
			items = append(items, item)
		}
		return &recommendationRows{items: items}, nil
	case containsSQL(sql, "FROM position_tracking_targets") && containsSQL(sql, "WHERE workspace_id"):
		workspaceID := uuidFromPgtype(args[0].(pgtype.UUID))
		productID := uuidFromPgtype(args[3].(pgtype.UUID))
		query := textValue(args[4].(pgtype.Text))
		region := textValue(args[5].(pgtype.Text))
		activeFilter := boolValue(args[6].(pgtype.Bool))
		var items []sqlcgen.PositionTrackingTarget
		for _, item := range db.trackingTargets {
			if uuidFromPgtype(item.WorkspaceID) != workspaceID {
				continue
			}
			if productID != uuid.Nil && uuidFromPgtype(item.ProductID) != productID {
				continue
			}
			if query != "" && item.Query != query {
				continue
			}
			if region != "" && item.Region != region {
				continue
			}
			if activeFilter != nil && item.IsActive != *activeFilter {
				continue
			}
			items = append(items, item)
		}
		return &positionTrackingTargetRows{items: items}, nil
	case containsSQL(sql, "FROM positions") && containsSQL(sql, "WHERE product_id"):
		productID := uuidFromPgtype(args[0].(pgtype.UUID))
		return &positionRows{items: db.positions[productID]}, nil
	case containsSQL(sql, "FROM positions") && containsSQL(sql, "WHERE workspace_id"):
		workspaceID := uuidFromPgtype(args[0].(pgtype.UUID))
		productID := uuidFromPgtype(args[3].(pgtype.UUID))
		query := textValue(args[4].(pgtype.Text))
		region := textValue(args[5].(pgtype.Text))
		var items []sqlcgen.Position
		for _, byProduct := range db.positions {
			for _, item := range byProduct {
				if uuidFromPgtype(item.WorkspaceID) != workspaceID {
					continue
				}
				if productID != uuid.Nil && uuidFromPgtype(item.ProductID) != productID {
					continue
				}
				if query != "" && item.Query != query {
					continue
				}
				if region != "" && item.Region != region {
					continue
				}
				items = append(items, item)
			}
		}
		return &positionRows{items: items}, nil
	case containsSQL(sql, "FROM serp_snapshots") && containsSQL(sql, "WHERE workspace_id"):
		workspaceID := uuidFromPgtype(args[0].(pgtype.UUID))
		var items []sqlcgen.SerpSnapshot
		for _, item := range db.serpSnapshots {
			if uuidFromPgtype(item.WorkspaceID) == workspaceID {
				items = append(items, item)
			}
		}
		return &serpSnapshotRows{items: items}, nil
	case containsSQL(sql, "FROM serp_result_items") && containsSQL(sql, "WHERE snapshot_id"):
		snapshotID := uuidFromPgtype(args[0].(pgtype.UUID))
		return &serpResultItemRows{items: db.serpItems[snapshotID]}, nil
	case containsSQL(sql, "DISTINCT ON") && containsSQL(sql, "campaign_stats"):
		// GetLatestCampaignStatsBatch — return all campaign stats
		var items []sqlcgen.CampaignStat
		for _, stat := range db.campaignStats {
			items = append(items, stat)
		}
		return &campaignStatBatchRows{items: items}, nil
	case containsSQL(sql, "DISTINCT ON") && containsSQL(sql, "phrase_stats"):
		// GetLatestPhraseStatsBatch — return all phrase stats
		var items []sqlcgen.PhraseStat
		for _, stat := range db.phraseStats {
			items = append(items, stat)
		}
		return &phraseStatBatchRows{items: items}, nil
	case containsSQL(sql, "DISTINCT ON") && containsSQL(sql, "bid_snapshots"):
		// GetLatestBidSnapshotsBatch — return all bid snapshots
		var items []sqlcgen.BidSnapshot
		for _, snap := range db.bidSnapshots {
			items = append(items, snap)
		}
		return &bidSnapshotBatchRows{items: items}, nil
	case containsSQL(sql, "workspace_settings"):
		return &fakeRows{}, nil
	}

	return &fakeRows{}, nil
}

func boolValue(value pgtype.Bool) *bool {
	if !value.Valid {
		return nil
	}
	result := value.Bool
	return &result
}

func (db *recommendationInMemDB) QueryRow(_ context.Context, sql string, args ...interface{}) pgx.Row {
	switch {
	case containsSQL(sql, "FROM recommendations WHERE id"):
		recommendationID := uuidFromPgtype(args[0].(pgtype.UUID))
		rec, ok := db.recommendations[recommendationID]
		if !ok {
			return &fakeRow{scanFunc: func(_ ...any) error { return pgx.ErrNoRows }}
		}
		return recommendationToRow(rec)
	case containsSQL(sql, "INSERT INTO recommendations"):
		now := time.Now().UTC()
		rec := sqlcgen.Recommendation{
			ID:            uuidToPgtype(uuid.New()),
			WorkspaceID:   args[0].(pgtype.UUID),
			CampaignID:    args[1].(pgtype.UUID),
			PhraseID:      args[2].(pgtype.UUID),
			ProductID:     args[3].(pgtype.UUID),
			Title:         args[4].(string),
			Description:   args[5].(string),
			Type:          args[6].(string),
			Severity:      args[7].(string),
			Confidence:    args[8].(pgtype.Numeric),
			SourceMetrics: args[9].([]byte),
			NextAction:    args[10].(pgtype.Text),
			Status:        domain.RecommendationStatusActive,
			CreatedAt:     pgtype.Timestamptz{Time: now, Valid: true},
			UpdatedAt:     pgtype.Timestamptz{Time: now, Valid: true},
		}
		db.recommendations[uuidFromPgtype(rec.ID)] = rec
		return recommendationToRow(rec)
	case containsSQL(sql, "FROM recommendations") && containsSQL(sql, "status = 'active'"):
		workspaceID := uuidFromPgtype(args[0].(pgtype.UUID))
		recType := args[1].(string)
		campaignID := uuidFromPgtype(args[2].(pgtype.UUID))
		phraseID := uuidFromPgtype(args[3].(pgtype.UUID))
		productID := uuidFromPgtype(args[4].(pgtype.UUID))
		for _, rec := range db.recommendations {
			if uuidFromPgtype(rec.WorkspaceID) != workspaceID || rec.Type != recType || rec.Status != domain.RecommendationStatusActive {
				continue
			}
			if campaignID != uuid.Nil && uuidFromPgtype(rec.CampaignID) == campaignID {
				return recommendationToRow(rec)
			}
			if phraseID != uuid.Nil && uuidFromPgtype(rec.PhraseID) == phraseID {
				return recommendationToRow(rec)
			}
			if productID != uuid.Nil && uuidFromPgtype(rec.ProductID) == productID {
				return recommendationToRow(rec)
			}
		}
		return &fakeRow{scanFunc: func(_ ...any) error { return pgx.ErrNoRows }}
	case containsSQL(sql, "UPDATE recommendations") && containsSQL(sql, "SET status"):
		recommendationID := uuidFromPgtype(args[0].(pgtype.UUID))
		rec, ok := db.recommendations[recommendationID]
		if !ok {
			return &fakeRow{scanFunc: func(_ ...any) error { return pgx.ErrNoRows }}
		}
		rec.Status = args[1].(string)
		rec.UpdatedAt = pgtype.Timestamptz{Time: time.Now().UTC(), Valid: true}
		db.recommendations[recommendationID] = rec
		return recommendationToRow(rec)
	case containsSQL(sql, "UPDATE recommendations") && containsSQL(sql, "SET title = $2"):
		recommendationID := uuidFromPgtype(args[0].(pgtype.UUID))
		rec, ok := db.recommendations[recommendationID]
		if !ok {
			return &fakeRow{scanFunc: func(_ ...any) error { return pgx.ErrNoRows }}
		}
		rec.Title = args[1].(string)
		rec.Description = args[2].(string)
		rec.Severity = args[3].(string)
		rec.Confidence = args[4].(pgtype.Numeric)
		rec.SourceMetrics = args[5].([]byte)
		rec.NextAction = args[6].(pgtype.Text)
		rec.UpdatedAt = pgtype.Timestamptz{Time: time.Now().UTC(), Valid: true}
		db.recommendations[recommendationID] = rec
		return recommendationToRow(rec)
	case containsSQL(sql, "FROM campaign_stats") && containsSQL(sql, "ORDER BY date DESC"):
		campaignID := uuidFromPgtype(args[0].(pgtype.UUID))
		stat, ok := db.campaignStats[campaignID]
		if !ok {
			return &fakeRow{scanFunc: func(_ ...any) error { return pgx.ErrNoRows }}
		}
		return campaignStatToRow(stat)
	case containsSQL(sql, "FROM phrase_stats") && containsSQL(sql, "ORDER BY date DESC"):
		phraseID := uuidFromPgtype(args[0].(pgtype.UUID))
		stat, ok := db.phraseStats[phraseID]
		if !ok {
			return &fakeRow{scanFunc: func(_ ...any) error { return pgx.ErrNoRows }}
		}
		return phraseStatToRow(stat)
	case containsSQL(sql, "FROM bid_snapshots") && containsSQL(sql, "ORDER BY captured_at DESC"):
		phraseID := uuidFromPgtype(args[0].(pgtype.UUID))
		bid, ok := db.bidSnapshots[phraseID]
		if !ok {
			return &fakeRow{scanFunc: func(_ ...any) error { return pgx.ErrNoRows }}
		}
		return bidSnapshotToRow(bid)
	}

	return &fakeRow{scanFunc: func(_ ...any) error { return pgx.ErrNoRows }}
}

type campaignRows struct {
	items []sqlcgen.Campaign
	idx   int
}

func (r *campaignRows) Close()                                       {}
func (r *campaignRows) Err() error                                   { return nil }
func (r *campaignRows) CommandTag() pgconn.CommandTag                { return pgconn.CommandTag{} }
func (r *campaignRows) FieldDescriptions() []pgconn.FieldDescription { return nil }
func (r *campaignRows) RawValues() [][]byte                          { return nil }
func (r *campaignRows) Conn() *pgx.Conn                              { return nil }
func (r *campaignRows) Values() ([]any, error)                       { return nil, nil }
func (r *campaignRows) Next() bool {
	if r.idx < len(r.items) {
		r.idx++
		return true
	}
	return false
}
func (r *campaignRows) Scan(dest ...any) error {
	item := r.items[r.idx-1]
	*dest[0].(*pgtype.UUID) = item.ID
	*dest[1].(*pgtype.UUID) = item.WorkspaceID
	*dest[2].(*pgtype.UUID) = item.SellerCabinetID
	*dest[3].(*int64) = item.WbCampaignID
	*dest[4].(*string) = item.Name
	*dest[5].(*string) = item.Status
	*dest[6].(*int32) = item.CampaignType
	*dest[7].(*string) = item.BidType
	*dest[8].(*string) = item.PaymentType
	*dest[9].(*pgtype.Int8) = item.DailyBudget
	*dest[10].(*pgtype.Timestamptz) = item.CreatedAt
	*dest[11].(*pgtype.Timestamptz) = item.UpdatedAt
	return nil
}

type phraseRows struct {
	items []sqlcgen.Phrase
	idx   int
}

func (r *phraseRows) Close()                                       {}
func (r *phraseRows) Err() error                                   { return nil }
func (r *phraseRows) CommandTag() pgconn.CommandTag                { return pgconn.CommandTag{} }
func (r *phraseRows) FieldDescriptions() []pgconn.FieldDescription { return nil }
func (r *phraseRows) RawValues() [][]byte                          { return nil }
func (r *phraseRows) Conn() *pgx.Conn                              { return nil }
func (r *phraseRows) Values() ([]any, error)                       { return nil, nil }
func (r *phraseRows) Next() bool {
	if r.idx < len(r.items) {
		r.idx++
		return true
	}
	return false
}
func (r *phraseRows) Scan(dest ...any) error {
	item := r.items[r.idx-1]
	*dest[0].(*pgtype.UUID) = item.ID
	*dest[1].(*pgtype.UUID) = item.CampaignID
	*dest[2].(*pgtype.UUID) = item.WorkspaceID
	*dest[3].(*int64) = item.WbClusterID
	*dest[4].(*string) = item.Keyword
	*dest[5].(*pgtype.Int4) = item.Count
	*dest[6].(*pgtype.Int8) = item.CurrentBid
	*dest[7].(*pgtype.Timestamptz) = item.CreatedAt
	*dest[8].(*pgtype.Timestamptz) = item.UpdatedAt
	return nil
}

type productRows struct {
	items []sqlcgen.Product
	idx   int
}

func (r *productRows) Close()                                       {}
func (r *productRows) Err() error                                   { return nil }
func (r *productRows) CommandTag() pgconn.CommandTag                { return pgconn.CommandTag{} }
func (r *productRows) FieldDescriptions() []pgconn.FieldDescription { return nil }
func (r *productRows) RawValues() [][]byte                          { return nil }
func (r *productRows) Conn() *pgx.Conn                              { return nil }
func (r *productRows) Values() ([]any, error)                       { return nil, nil }
func (r *productRows) Next() bool {
	if r.idx < len(r.items) {
		r.idx++
		return true
	}
	return false
}
func (r *productRows) Scan(dest ...any) error {
	item := r.items[r.idx-1]
	*dest[0].(*pgtype.UUID) = item.ID
	*dest[1].(*pgtype.UUID) = item.WorkspaceID
	*dest[2].(*pgtype.UUID) = item.SellerCabinetID
	*dest[3].(*int64) = item.WbProductID
	*dest[4].(*string) = item.Title
	*dest[5].(*pgtype.Text) = item.Brand
	*dest[6].(*pgtype.Text) = item.Category
	*dest[7].(*pgtype.Text) = item.ImageUrl
	*dest[8].(*pgtype.Int8) = item.Price
	*dest[9].(*pgtype.Timestamptz) = item.CreatedAt
	*dest[10].(*pgtype.Timestamptz) = item.UpdatedAt
	return nil
}

type positionRows struct {
	items []sqlcgen.Position
	idx   int
}

func (r *positionRows) Close()                                       {}
func (r *positionRows) Err() error                                   { return nil }
func (r *positionRows) CommandTag() pgconn.CommandTag                { return pgconn.CommandTag{} }
func (r *positionRows) FieldDescriptions() []pgconn.FieldDescription { return nil }
func (r *positionRows) RawValues() [][]byte                          { return nil }
func (r *positionRows) Conn() *pgx.Conn                              { return nil }
func (r *positionRows) Values() ([]any, error)                       { return nil, nil }
func (r *positionRows) Next() bool {
	if r.idx < len(r.items) {
		r.idx++
		return true
	}
	return false
}
func (r *positionRows) Scan(dest ...any) error {
	item := r.items[r.idx-1]
	*dest[0].(*pgtype.UUID) = item.ID
	*dest[1].(*pgtype.UUID) = item.WorkspaceID
	*dest[2].(*pgtype.UUID) = item.ProductID
	*dest[3].(*string) = item.Query
	*dest[4].(*string) = item.Region
	*dest[5].(*int32) = item.Position
	*dest[6].(*int32) = item.Page
	*dest[7].(*string) = item.Source
	*dest[8].(*pgtype.Timestamptz) = item.CheckedAt
	*dest[9].(*pgtype.Timestamptz) = item.CreatedAt
	return nil
}

type positionTrackingTargetRows struct {
	items []sqlcgen.PositionTrackingTarget
	idx   int
}

func (r *positionTrackingTargetRows) Close()                                       {}
func (r *positionTrackingTargetRows) Err() error                                   { return nil }
func (r *positionTrackingTargetRows) CommandTag() pgconn.CommandTag                { return pgconn.CommandTag{} }
func (r *positionTrackingTargetRows) FieldDescriptions() []pgconn.FieldDescription { return nil }
func (r *positionTrackingTargetRows) RawValues() [][]byte                          { return nil }
func (r *positionTrackingTargetRows) Conn() *pgx.Conn                              { return nil }
func (r *positionTrackingTargetRows) Values() ([]any, error)                       { return nil, nil }
func (r *positionTrackingTargetRows) Next() bool {
	if r.idx < len(r.items) {
		r.idx++
		return true
	}
	return false
}
func (r *positionTrackingTargetRows) Scan(dest ...any) error {
	item := r.items[r.idx-1]
	*dest[0].(*pgtype.UUID) = item.ID
	*dest[1].(*pgtype.UUID) = item.WorkspaceID
	*dest[2].(*pgtype.UUID) = item.ProductID
	*dest[3].(*string) = item.Query
	*dest[4].(*string) = item.Region
	*dest[5].(*bool) = item.IsActive
	*dest[6].(*pgtype.Int4) = item.BaselinePosition
	*dest[7].(*pgtype.Timestamptz) = item.BaselineCheckedAt
	*dest[8].(*pgtype.Timestamptz) = item.CreatedAt
	*dest[9].(*pgtype.Timestamptz) = item.UpdatedAt
	return nil
}

type recommendationRows struct {
	items []sqlcgen.Recommendation
	idx   int
}

func (r *recommendationRows) Close()                                       {}
func (r *recommendationRows) Err() error                                   { return nil }
func (r *recommendationRows) CommandTag() pgconn.CommandTag                { return pgconn.CommandTag{} }
func (r *recommendationRows) FieldDescriptions() []pgconn.FieldDescription { return nil }
func (r *recommendationRows) RawValues() [][]byte                          { return nil }
func (r *recommendationRows) Conn() *pgx.Conn                              { return nil }
func (r *recommendationRows) Values() ([]any, error)                       { return nil, nil }
func (r *recommendationRows) Next() bool {
	if r.idx < len(r.items) {
		r.idx++
		return true
	}
	return false
}
func (r *recommendationRows) Scan(dest ...any) error {
	item := r.items[r.idx-1]
	*dest[0].(*pgtype.UUID) = item.ID
	*dest[1].(*pgtype.UUID) = item.WorkspaceID
	*dest[2].(*pgtype.UUID) = item.CampaignID
	*dest[3].(*pgtype.UUID) = item.PhraseID
	*dest[4].(*pgtype.UUID) = item.ProductID
	*dest[5].(*string) = item.Title
	*dest[6].(*string) = item.Description
	*dest[7].(*string) = item.Type
	*dest[8].(*string) = item.Severity
	*dest[9].(*pgtype.Numeric) = item.Confidence
	*dest[10].(*[]byte) = item.SourceMetrics
	*dest[11].(*pgtype.Text) = item.NextAction
	*dest[12].(*string) = item.Status
	*dest[13].(*pgtype.Timestamptz) = item.CreatedAt
	*dest[14].(*pgtype.Timestamptz) = item.UpdatedAt
	return nil
}

func recommendationToRow(item sqlcgen.Recommendation) pgx.Row {
	return &fakeRow{scanFunc: func(dest ...any) error {
		*dest[0].(*pgtype.UUID) = item.ID
		*dest[1].(*pgtype.UUID) = item.WorkspaceID
		*dest[2].(*pgtype.UUID) = item.CampaignID
		*dest[3].(*pgtype.UUID) = item.PhraseID
		*dest[4].(*pgtype.UUID) = item.ProductID
		*dest[5].(*string) = item.Title
		*dest[6].(*string) = item.Description
		*dest[7].(*string) = item.Type
		*dest[8].(*string) = item.Severity
		*dest[9].(*pgtype.Numeric) = item.Confidence
		*dest[10].(*[]byte) = item.SourceMetrics
		*dest[11].(*pgtype.Text) = item.NextAction
		*dest[12].(*string) = item.Status
		*dest[13].(*pgtype.Timestamptz) = item.CreatedAt
		*dest[14].(*pgtype.Timestamptz) = item.UpdatedAt
		return nil
	}}
}

func campaignStatToRow(item sqlcgen.CampaignStat) pgx.Row {
	return &fakeRow{scanFunc: func(dest ...any) error {
		*dest[0].(*pgtype.UUID) = item.ID
		*dest[1].(*pgtype.UUID) = item.CampaignID
		*dest[2].(*pgtype.Date) = item.Date
		*dest[3].(*int64) = item.Impressions
		*dest[4].(*int64) = item.Clicks
		*dest[5].(*int64) = item.Spend
		*dest[6].(*pgtype.Int8) = item.Orders
		*dest[7].(*pgtype.Int8) = item.Revenue
		*dest[8].(*pgtype.Timestamptz) = item.CreatedAt
		*dest[9].(*pgtype.Timestamptz) = item.UpdatedAt
		return nil
	}}
}

func phraseStatToRow(item sqlcgen.PhraseStat) pgx.Row {
	return &fakeRow{scanFunc: func(dest ...any) error {
		*dest[0].(*pgtype.UUID) = item.ID
		*dest[1].(*pgtype.UUID) = item.PhraseID
		*dest[2].(*pgtype.Date) = item.Date
		*dest[3].(*int64) = item.Impressions
		*dest[4].(*int64) = item.Clicks
		*dest[5].(*int64) = item.Spend
		*dest[6].(*pgtype.Timestamptz) = item.CreatedAt
		*dest[7].(*pgtype.Timestamptz) = item.UpdatedAt
		return nil
	}}
}

func bidSnapshotToRow(item sqlcgen.BidSnapshot) pgx.Row {
	return &fakeRow{scanFunc: func(dest ...any) error {
		*dest[0].(*pgtype.UUID) = item.ID
		*dest[1].(*pgtype.UUID) = item.PhraseID
		*dest[2].(*pgtype.UUID) = item.WorkspaceID
		*dest[3].(*int64) = item.CompetitiveBid
		*dest[4].(*int64) = item.LeadershipBid
		*dest[5].(*int64) = item.CpmMin
		*dest[6].(*pgtype.Timestamptz) = item.CapturedAt
		*dest[7].(*pgtype.Timestamptz) = item.CreatedAt
		return nil
	}}
}

type serpSnapshotRows struct {
	items []sqlcgen.SerpSnapshot
	idx   int
}

func (r *serpSnapshotRows) Close()                                       {}
func (r *serpSnapshotRows) Err() error                                   { return nil }
func (r *serpSnapshotRows) CommandTag() pgconn.CommandTag                { return pgconn.CommandTag{} }
func (r *serpSnapshotRows) FieldDescriptions() []pgconn.FieldDescription { return nil }
func (r *serpSnapshotRows) RawValues() [][]byte                          { return nil }
func (r *serpSnapshotRows) Conn() *pgx.Conn                              { return nil }
func (r *serpSnapshotRows) Values() ([]any, error)                       { return nil, nil }
func (r *serpSnapshotRows) Next() bool {
	if r.idx < len(r.items) {
		r.idx++
		return true
	}
	return false
}
func (r *serpSnapshotRows) Scan(dest ...any) error {
	item := r.items[r.idx-1]
	*dest[0].(*pgtype.UUID) = item.ID
	*dest[1].(*pgtype.UUID) = item.WorkspaceID
	*dest[2].(*string) = item.Query
	*dest[3].(*string) = item.Region
	*dest[4].(*int32) = item.TotalResults
	*dest[5].(*pgtype.Timestamptz) = item.ScannedAt
	*dest[6].(*pgtype.Timestamptz) = item.CreatedAt
	return nil
}

type serpResultItemRows struct {
	items []sqlcgen.SerpResultItem
	idx   int
}

func (r *serpResultItemRows) Close()                                       {}
func (r *serpResultItemRows) Err() error                                   { return nil }
func (r *serpResultItemRows) CommandTag() pgconn.CommandTag                { return pgconn.CommandTag{} }
func (r *serpResultItemRows) FieldDescriptions() []pgconn.FieldDescription { return nil }
func (r *serpResultItemRows) RawValues() [][]byte                          { return nil }
func (r *serpResultItemRows) Conn() *pgx.Conn                              { return nil }
func (r *serpResultItemRows) Values() ([]any, error)                       { return nil, nil }
func (r *serpResultItemRows) Next() bool {
	if r.idx < len(r.items) {
		r.idx++
		return true
	}
	return false
}
func (r *serpResultItemRows) Scan(dest ...any) error {
	item := r.items[r.idx-1]
	*dest[0].(*pgtype.UUID) = item.ID
	*dest[1].(*pgtype.UUID) = item.SnapshotID
	*dest[2].(*int32) = item.Position
	*dest[3].(*int64) = item.WbProductID
	*dest[4].(*string) = item.Title
	*dest[5].(*pgtype.Int8) = item.Price
	*dest[6].(*pgtype.Numeric) = item.Rating
	*dest[7].(*pgtype.Int4) = item.ReviewsCount
	*dest[8].(*pgtype.Timestamptz) = item.CreatedAt
	return nil
}

func textValue(value pgtype.Text) string {
	if !value.Valid {
		return ""
	}
	return value.String
}

// Batch row types for preloaded stats (N+1 fix)

type campaignStatBatchRows struct {
	items []sqlcgen.CampaignStat
	idx   int
}

func (r *campaignStatBatchRows) Close()                                       {}
func (r *campaignStatBatchRows) Err() error                                   { return nil }
func (r *campaignStatBatchRows) CommandTag() pgconn.CommandTag                { return pgconn.CommandTag{} }
func (r *campaignStatBatchRows) FieldDescriptions() []pgconn.FieldDescription { return nil }
func (r *campaignStatBatchRows) RawValues() [][]byte                          { return nil }
func (r *campaignStatBatchRows) Conn() *pgx.Conn                              { return nil }
func (r *campaignStatBatchRows) Values() ([]any, error)                       { return nil, nil }
func (r *campaignStatBatchRows) Next() bool {
	if r.idx < len(r.items) {
		r.idx++
		return true
	}
	return false
}
func (r *campaignStatBatchRows) Scan(dest ...any) error {
	item := r.items[r.idx-1]
	*dest[0].(*pgtype.UUID) = item.ID
	*dest[1].(*pgtype.UUID) = item.CampaignID
	*dest[2].(*pgtype.Date) = item.Date
	*dest[3].(*int64) = item.Impressions
	*dest[4].(*int64) = item.Clicks
	*dest[5].(*int64) = item.Spend
	*dest[6].(*pgtype.Int8) = item.Orders
	*dest[7].(*pgtype.Int8) = item.Revenue
	*dest[8].(*pgtype.Timestamptz) = item.CreatedAt
	return nil
}

type phraseStatBatchRows struct {
	items []sqlcgen.PhraseStat
	idx   int
}

func (r *phraseStatBatchRows) Close()                                       {}
func (r *phraseStatBatchRows) Err() error                                   { return nil }
func (r *phraseStatBatchRows) CommandTag() pgconn.CommandTag                { return pgconn.CommandTag{} }
func (r *phraseStatBatchRows) FieldDescriptions() []pgconn.FieldDescription { return nil }
func (r *phraseStatBatchRows) RawValues() [][]byte                          { return nil }
func (r *phraseStatBatchRows) Conn() *pgx.Conn                              { return nil }
func (r *phraseStatBatchRows) Values() ([]any, error)                       { return nil, nil }
func (r *phraseStatBatchRows) Next() bool {
	if r.idx < len(r.items) {
		r.idx++
		return true
	}
	return false
}
func (r *phraseStatBatchRows) Scan(dest ...any) error {
	item := r.items[r.idx-1]
	*dest[0].(*pgtype.UUID) = item.ID
	*dest[1].(*pgtype.UUID) = item.PhraseID
	*dest[2].(*pgtype.Date) = item.Date
	*dest[3].(*int64) = item.Impressions
	*dest[4].(*int64) = item.Clicks
	*dest[5].(*int64) = item.Spend
	*dest[6].(*pgtype.Timestamptz) = item.CreatedAt
	return nil
}

type bidSnapshotBatchRows struct {
	items []sqlcgen.BidSnapshot
	idx   int
}

func (r *bidSnapshotBatchRows) Close()                                       {}
func (r *bidSnapshotBatchRows) Err() error                                   { return nil }
func (r *bidSnapshotBatchRows) CommandTag() pgconn.CommandTag                { return pgconn.CommandTag{} }
func (r *bidSnapshotBatchRows) FieldDescriptions() []pgconn.FieldDescription { return nil }
func (r *bidSnapshotBatchRows) RawValues() [][]byte                          { return nil }
func (r *bidSnapshotBatchRows) Conn() *pgx.Conn                              { return nil }
func (r *bidSnapshotBatchRows) Values() ([]any, error)                       { return nil, nil }
func (r *bidSnapshotBatchRows) Next() bool {
	if r.idx < len(r.items) {
		r.idx++
		return true
	}
	return false
}
func (r *bidSnapshotBatchRows) Scan(dest ...any) error {
	item := r.items[r.idx-1]
	*dest[0].(*pgtype.UUID) = item.ID
	*dest[1].(*pgtype.UUID) = item.PhraseID
	*dest[2].(*int64) = item.CompetitiveBid
	*dest[3].(*int64) = item.LeadershipBid
	*dest[4].(*int64) = item.CpmMin
	*dest[5].(*pgtype.Timestamptz) = item.CreatedAt
	return nil
}

func TestRecommendationServiceUpdateStatusEnforcesWorkspaceOwnership(t *testing.T) {
	db := newRecommendationInMemDB()
	queries := sqlcgen.New(db)
	svc := NewRecommendationService(queries)

	workspaceID := uuid.New()
	otherWorkspaceID := uuid.New()
	recommendationID := uuid.New()
	now := time.Now().UTC()
	db.recommendations[recommendationID] = sqlcgen.Recommendation{
		ID:          uuidToPgtype(recommendationID),
		WorkspaceID: uuidToPgtype(workspaceID),
		Title:       "High impressions with zero clicks",
		Description: "desc",
		Type:        domain.RecommendationTypeLowCTR,
		Severity:    domain.SeverityHigh,
		Status:      domain.RecommendationStatusActive,
		CreatedAt:   pgtype.Timestamptz{Time: now, Valid: true},
		UpdatedAt:   pgtype.Timestamptz{Time: now, Valid: true},
	}

	result, err := svc.UpdateStatus(context.Background(), otherWorkspaceID, recommendationID, domain.RecommendationStatusCompleted)

	require.Error(t, err)
	assert.Nil(t, result)
	assert.Equal(t, domain.RecommendationStatusActive, db.recommendations[recommendationID].Status)
}

func TestRecommendationServiceListFiltersByCampaign(t *testing.T) {
	db := newRecommendationInMemDB()
	queries := sqlcgen.New(db)
	svc := NewRecommendationService(queries)

	workspaceID := uuid.New()
	campaignID := uuid.New()
	otherCampaignID := uuid.New()
	now := time.Now().UTC()
	db.recommendations[uuid.New()] = sqlcgen.Recommendation{
		ID:          uuidToPgtype(uuid.New()),
		WorkspaceID: uuidToPgtype(workspaceID),
		CampaignID:  uuidToPgtype(campaignID),
		Title:       "Campaign recommendation",
		Description: "desc",
		Type:        domain.RecommendationTypeLowCTR,
		Severity:    domain.SeverityHigh,
		Status:      domain.RecommendationStatusActive,
		CreatedAt:   pgtype.Timestamptz{Time: now, Valid: true},
		UpdatedAt:   pgtype.Timestamptz{Time: now, Valid: true},
	}
	db.recommendations[uuid.New()] = sqlcgen.Recommendation{
		ID:          uuidToPgtype(uuid.New()),
		WorkspaceID: uuidToPgtype(workspaceID),
		CampaignID:  uuidToPgtype(otherCampaignID),
		Title:       "Other recommendation",
		Description: "desc",
		Type:        domain.RecommendationTypeLowCTR,
		Severity:    domain.SeverityHigh,
		Status:      domain.RecommendationStatusActive,
		CreatedAt:   pgtype.Timestamptz{Time: now, Valid: true},
		UpdatedAt:   pgtype.Timestamptz{Time: now, Valid: true},
	}

	result, err := svc.List(context.Background(), workspaceID, RecommendationListFilter{
		CampaignID: &campaignID,
		Status:     domain.RecommendationStatusActive,
	}, 20, 0)

	require.NoError(t, err)
	require.Len(t, result, 1)
	assert.Equal(t, campaignID, *result[0].CampaignID)
}

func TestRecommendationServiceUpsertActiveRefreshesExistingRecommendation(t *testing.T) {
	db := newRecommendationInMemDB()
	queries := sqlcgen.New(db)
	svc := NewRecommendationService(queries)

	workspaceID := uuid.New()
	campaignID := uuid.New()
	recommendationID := uuid.New()
	confidence, err := numericFromFloat64(0.5)
	require.NoError(t, err)
	now := time.Now().UTC()
	db.recommendations[recommendationID] = sqlcgen.Recommendation{
		ID:            uuidToPgtype(recommendationID),
		WorkspaceID:   uuidToPgtype(workspaceID),
		CampaignID:    uuidToPgtype(campaignID),
		Title:         "Old title",
		Description:   "Old description",
		Type:          domain.RecommendationTypeLowCTR,
		Severity:      domain.SeverityLow,
		Confidence:    confidence,
		SourceMetrics: []byte(`{"clicks":0}`),
		Status:        domain.RecommendationStatusActive,
		CreatedAt:     pgtype.Timestamptz{Time: now, Valid: true},
		UpdatedAt:     pgtype.Timestamptz{Time: now, Valid: true},
	}

	result, err := svc.UpsertActive(context.Background(), RecommendationUpsertInput{
		WorkspaceID: workspaceID,
		CampaignID:  &campaignID,
		Title:       "New title",
		Description: "New description",
		Type:        domain.RecommendationTypeLowCTR,
		Severity:    domain.SeverityHigh,
		Confidence:  0.91,
		SourceMetrics: map[string]any{
			"impressions": int64(1200),
			"clicks":      int64(0),
		},
		NextAction: strPtr("Fix CTR"),
	})

	require.NoError(t, err)
	require.NotNil(t, result)
	assert.Equal(t, recommendationID, result.ID)
	assert.Len(t, db.recommendations, 1)
	assert.Equal(t, "New title", db.recommendations[recommendationID].Title)

	var sourceMetrics map[string]any
	require.NoError(t, json.Unmarshal(db.recommendations[recommendationID].SourceMetrics, &sourceMetrics))
	assert.Equal(t, float64(1200), sourceMetrics["impressions"])
}

func TestRecommendationServiceCloseActiveCompletesRecommendation(t *testing.T) {
	db := newRecommendationInMemDB()
	queries := sqlcgen.New(db)
	svc := NewRecommendationService(queries)

	workspaceID := uuid.New()
	productID := uuid.New()
	recommendationID := uuid.New()
	now := time.Now().UTC()
	db.recommendations[recommendationID] = sqlcgen.Recommendation{
		ID:          uuidToPgtype(recommendationID),
		WorkspaceID: uuidToPgtype(workspaceID),
		ProductID:   uuidToPgtype(productID),
		Title:       "Product search position dropped",
		Description: "desc",
		Type:        domain.RecommendationTypePositionDrop,
		Severity:    domain.SeverityMedium,
		Status:      domain.RecommendationStatusActive,
		CreatedAt:   pgtype.Timestamptz{Time: now, Valid: true},
		UpdatedAt:   pgtype.Timestamptz{Time: now, Valid: true},
	}

	err := svc.CloseActive(context.Background(), workspaceID, domain.RecommendationTypePositionDrop, nil, nil, &productID)

	require.NoError(t, err)
	assert.Equal(t, domain.RecommendationStatusCompleted, db.recommendations[recommendationID].Status)
}

func TestRecommendationEngineClosesResolvedCampaignRecommendation(t *testing.T) {
	db := newRecommendationInMemDB()
	queries := sqlcgen.New(db)
	recommendationService := NewRecommendationService(queries)
	engine := NewRecommendationEngine(queries, recommendationService, nil, zerolog.Nop())

	workspaceID := uuid.New()
	campaignID := uuid.New()
	recommendationID := uuid.New()
	now := time.Now().UTC()
	db.campaigns = []sqlcgen.Campaign{{
		ID:          uuidToPgtype(campaignID),
		WorkspaceID: uuidToPgtype(workspaceID),
		Name:        "Campaign A",
		CreatedAt:   pgtype.Timestamptz{Time: now, Valid: true},
		UpdatedAt:   pgtype.Timestamptz{Time: now, Valid: true},
	}}
	db.campaignStats[campaignID] = sqlcgen.CampaignStat{
		ID:          uuidToPgtype(uuid.New()),
		CampaignID:  uuidToPgtype(campaignID),
		Date:        pgtype.Date{Time: now, Valid: true},
		Impressions: 1500,
		Clicks:      10,
		Spend:       300,
		Orders:      pgtype.Int8{Int64: 3, Valid: true},
		CreatedAt:   pgtype.Timestamptz{Time: now, Valid: true},
		UpdatedAt:   pgtype.Timestamptz{Time: now, Valid: true},
	}
	db.recommendations[recommendationID] = sqlcgen.Recommendation{
		ID:          uuidToPgtype(recommendationID),
		WorkspaceID: uuidToPgtype(workspaceID),
		CampaignID:  uuidToPgtype(campaignID),
		Title:       "High impressions with zero clicks",
		Description: "stale",
		Type:        domain.RecommendationTypeLowCTR,
		Severity:    domain.SeverityHigh,
		Status:      domain.RecommendationStatusActive,
		CreatedAt:   pgtype.Timestamptz{Time: now, Valid: true},
		UpdatedAt:   pgtype.Timestamptz{Time: now, Valid: true},
	}

	result, err := engine.GenerateForWorkspace(context.Background(), workspaceID)

	require.NoError(t, err)
	assert.Empty(t, result)
	assert.Equal(t, domain.RecommendationStatusCompleted, db.recommendations[recommendationID].Status)
}

func TestRecommendationEngineCreatesCampaignLowCTRRecommendation(t *testing.T) {
	db := newRecommendationInMemDB()
	queries := sqlcgen.New(db)
	recommendationService := NewRecommendationService(queries)
	engine := NewRecommendationEngine(queries, recommendationService, nil, zerolog.Nop())

	workspaceID := uuid.New()
	campaignID := uuid.New()
	now := time.Now().UTC()
	db.campaigns = []sqlcgen.Campaign{{
		ID:          uuidToPgtype(campaignID),
		WorkspaceID: uuidToPgtype(workspaceID),
		Name:        "Campaign Zero CTR",
		CreatedAt:   pgtype.Timestamptz{Time: now, Valid: true},
		UpdatedAt:   pgtype.Timestamptz{Time: now, Valid: true},
	}}
	db.campaignStats[campaignID] = sqlcgen.CampaignStat{
		ID:          uuidToPgtype(uuid.New()),
		CampaignID:  uuidToPgtype(campaignID),
		Date:        pgtype.Date{Time: now, Valid: true},
		Impressions: 1800,
		Clicks:      0,
		Spend:       0,
		CreatedAt:   pgtype.Timestamptz{Time: now, Valid: true},
		UpdatedAt:   pgtype.Timestamptz{Time: now, Valid: true},
	}

	result, err := engine.GenerateForWorkspace(context.Background(), workspaceID)

	require.NoError(t, err)
	require.Len(t, result, 1)
	assert.Equal(t, domain.RecommendationTypeLowCTR, result[0].Type)
	require.NotNil(t, result[0].CampaignID)
	assert.Equal(t, campaignID, *result[0].CampaignID)
	assert.Contains(t, result[0].Description, "1800 показов, но 0 кликов")
}

func TestRecommendationEngineCreatesCampaignHighSpendLowOrdersRecommendation(t *testing.T) {
	db := newRecommendationInMemDB()
	queries := sqlcgen.New(db)
	recommendationService := NewRecommendationService(queries)
	engine := NewRecommendationEngine(queries, recommendationService, nil, zerolog.Nop())

	workspaceID := uuid.New()
	campaignID := uuid.New()
	now := time.Now().UTC()
	db.campaigns = []sqlcgen.Campaign{{
		ID:          uuidToPgtype(campaignID),
		WorkspaceID: uuidToPgtype(workspaceID),
		Name:        "Campaign Weak Conversion",
		CreatedAt:   pgtype.Timestamptz{Time: now, Valid: true},
		UpdatedAt:   pgtype.Timestamptz{Time: now, Valid: true},
	}}
	db.campaignStats[campaignID] = sqlcgen.CampaignStat{
		ID:          uuidToPgtype(uuid.New()),
		CampaignID:  uuidToPgtype(campaignID),
		Date:        pgtype.Date{Time: now, Valid: true},
		Impressions: 1200,
		Clicks:      65,
		Spend:       11000,
		Orders:      pgtype.Int8{Int64: 0, Valid: true},
		CreatedAt:   pgtype.Timestamptz{Time: now, Valid: true},
		UpdatedAt:   pgtype.Timestamptz{Time: now, Valid: true},
	}

	result, err := engine.GenerateForWorkspace(context.Background(), workspaceID)

	require.NoError(t, err)
	types := make([]string, 0, len(result))
	for _, recommendation := range result {
		types = append(types, recommendation.Type)
		if recommendation.Type == domain.RecommendationTypeHighSpendLowOrders {
			require.NotNil(t, recommendation.CampaignID)
			assert.Equal(t, campaignID, *recommendation.CampaignID)
			assert.Contains(t, recommendation.Description, "65 кликов, но 0 заказов")
		}
	}
	assert.Contains(t, types, domain.RecommendationTypeHighSpendLowOrders)
}

func TestRecommendationEngineClosesResolvedCampaignHighSpendLowOrdersRecommendation(t *testing.T) {
	db := newRecommendationInMemDB()
	queries := sqlcgen.New(db)
	recommendationService := NewRecommendationService(queries)
	engine := NewRecommendationEngine(queries, recommendationService, nil, zerolog.Nop())

	workspaceID := uuid.New()
	campaignID := uuid.New()
	recommendationID := uuid.New()
	now := time.Now().UTC()
	db.campaigns = []sqlcgen.Campaign{{
		ID:          uuidToPgtype(campaignID),
		WorkspaceID: uuidToPgtype(workspaceID),
		Name:        "Campaign Weak Conversion",
		CreatedAt:   pgtype.Timestamptz{Time: now, Valid: true},
		UpdatedAt:   pgtype.Timestamptz{Time: now, Valid: true},
	}}
	db.campaignStats[campaignID] = sqlcgen.CampaignStat{
		ID:          uuidToPgtype(uuid.New()),
		CampaignID:  uuidToPgtype(campaignID),
		Date:        pgtype.Date{Time: now, Valid: true},
		Impressions: 1200,
		Clicks:      65,
		Spend:       11000,
		Orders:      pgtype.Int8{Int64: 4, Valid: true},
		CreatedAt:   pgtype.Timestamptz{Time: now, Valid: true},
		UpdatedAt:   pgtype.Timestamptz{Time: now, Valid: true},
	}
	db.recommendations[recommendationID] = sqlcgen.Recommendation{
		ID:          uuidToPgtype(recommendationID),
		WorkspaceID: uuidToPgtype(workspaceID),
		CampaignID:  uuidToPgtype(campaignID),
		Title:       "Clicks are not converting into orders",
		Description: "stale",
		Type:        domain.RecommendationTypeHighSpendLowOrders,
		Severity:    domain.SeverityHigh,
		Status:      domain.RecommendationStatusActive,
		CreatedAt:   pgtype.Timestamptz{Time: now, Valid: true},
		UpdatedAt:   pgtype.Timestamptz{Time: now, Valid: true},
	}

	result, err := engine.GenerateForWorkspace(context.Background(), workspaceID)

	require.NoError(t, err)
	types := make([]string, 0, len(result))
	for _, recommendation := range result {
		types = append(types, recommendation.Type)
	}
	assert.NotContains(t, types, domain.RecommendationTypeHighSpendLowOrders)
	assert.Equal(t, domain.RecommendationStatusCompleted, db.recommendations[recommendationID].Status)
}

func TestRecommendationEngineCreatesCampaignRaiseBidRecommendation(t *testing.T) {
	db := newRecommendationInMemDB()
	queries := sqlcgen.New(db)
	recommendationService := NewRecommendationService(queries)
	engine := NewRecommendationEngine(queries, recommendationService, nil, zerolog.Nop())

	workspaceID := uuid.New()
	campaignID := uuid.New()
	now := time.Now().UTC()
	db.campaigns = []sqlcgen.Campaign{{
		ID:          uuidToPgtype(campaignID),
		WorkspaceID: uuidToPgtype(workspaceID),
		Name:        "Campaign Ready To Scale",
		CreatedAt:   pgtype.Timestamptz{Time: now, Valid: true},
		UpdatedAt:   pgtype.Timestamptz{Time: now, Valid: true},
	}}
	// Spend/clicks gives CPC=28.5 < 50(threshold), CPO=200 < 1500(threshold) → lower_bid does NOT fire.
	// ROAS = 72000/2000 = 36 >= 3.0(threshold) → raise_bid fires.
	db.campaignStats[campaignID] = sqlcgen.CampaignStat{
		ID:          uuidToPgtype(uuid.New()),
		CampaignID:  uuidToPgtype(campaignID),
		Date:        pgtype.Date{Time: now, Valid: true},
		Impressions: 3500,
		Clicks:      70,
		Spend:       2000,
		Orders:      pgtype.Int8{Int64: 10, Valid: true},
		Revenue:     pgtype.Int8{Int64: 72000, Valid: true},
		CreatedAt:   pgtype.Timestamptz{Time: now, Valid: true},
		UpdatedAt:   pgtype.Timestamptz{Time: now, Valid: true},
	}

	result, err := engine.GenerateForWorkspace(context.Background(), workspaceID)

	require.NoError(t, err)
	types := make([]string, 0, len(result))
	for _, recommendation := range result {
		types = append(types, recommendation.Type)
		if recommendation.Type == domain.RecommendationTypeRaiseBid {
			require.NotNil(t, recommendation.CampaignID)
			assert.Equal(t, campaignID, *recommendation.CampaignID)
			assert.Contains(t, recommendation.Description, "масштабирования")
		}
	}
	assert.Contains(t, types, domain.RecommendationTypeRaiseBid)
}

func TestRecommendationEngineClosesResolvedCampaignRaiseBidRecommendation(t *testing.T) {
	db := newRecommendationInMemDB()
	queries := sqlcgen.New(db)
	recommendationService := NewRecommendationService(queries)
	engine := NewRecommendationEngine(queries, recommendationService, nil, zerolog.Nop())

	workspaceID := uuid.New()
	campaignID := uuid.New()
	recommendationID := uuid.New()
	now := time.Now().UTC()
	db.campaigns = []sqlcgen.Campaign{{
		ID:          uuidToPgtype(campaignID),
		WorkspaceID: uuidToPgtype(workspaceID),
		Name:        "Campaign Ready To Scale",
		CreatedAt:   pgtype.Timestamptz{Time: now, Valid: true},
		UpdatedAt:   pgtype.Timestamptz{Time: now, Valid: true},
	}}
	db.campaignStats[campaignID] = sqlcgen.CampaignStat{
		ID:          uuidToPgtype(uuid.New()),
		CampaignID:  uuidToPgtype(campaignID),
		Date:        pgtype.Date{Time: now, Valid: true},
		Impressions: 3500,
		Clicks:      70,
		Spend:       12000,
		Orders:      pgtype.Int8{Int64: 3, Valid: true},
		Revenue:     pgtype.Int8{Int64: 24000, Valid: true},
		CreatedAt:   pgtype.Timestamptz{Time: now, Valid: true},
		UpdatedAt:   pgtype.Timestamptz{Time: now, Valid: true},
	}
	db.recommendations[recommendationID] = sqlcgen.Recommendation{
		ID:          uuidToPgtype(recommendationID),
		WorkspaceID: uuidToPgtype(workspaceID),
		CampaignID:  uuidToPgtype(campaignID),
		Title:       "Campaign is converting efficiently and can scale",
		Description: "stale",
		Type:        domain.RecommendationTypeRaiseBid,
		Severity:    domain.SeverityMedium,
		Status:      domain.RecommendationStatusActive,
		CreatedAt:   pgtype.Timestamptz{Time: now, Valid: true},
		UpdatedAt:   pgtype.Timestamptz{Time: now, Valid: true},
	}

	result, err := engine.GenerateForWorkspace(context.Background(), workspaceID)

	require.NoError(t, err)
	types := make([]string, 0, len(result))
	for _, recommendation := range result {
		types = append(types, recommendation.Type)
	}
	assert.NotContains(t, types, domain.RecommendationTypeRaiseBid)
	assert.Equal(t, domain.RecommendationStatusCompleted, db.recommendations[recommendationID].Status)
}

func TestRecommendationEngineCreatesCampaignLowerBidRecommendation(t *testing.T) {
	db := newRecommendationInMemDB()
	queries := sqlcgen.New(db)
	recommendationService := NewRecommendationService(queries)
	engine := NewRecommendationEngine(queries, recommendationService, nil, zerolog.Nop())

	workspaceID := uuid.New()
	campaignID := uuid.New()
	now := time.Now().UTC()
	db.campaigns = []sqlcgen.Campaign{{
		ID:          uuidToPgtype(campaignID),
		WorkspaceID: uuidToPgtype(workspaceID),
		Name:        "Campaign CPC Pressure",
		CreatedAt:   pgtype.Timestamptz{Time: now, Valid: true},
		UpdatedAt:   pgtype.Timestamptz{Time: now, Valid: true},
	}}
	db.campaignStats[campaignID] = sqlcgen.CampaignStat{
		ID:          uuidToPgtype(uuid.New()),
		CampaignID:  uuidToPgtype(campaignID),
		Date:        pgtype.Date{Time: now, Valid: true},
		Impressions: 1800,
		Clicks:      50,
		Spend:       80000,
		Orders:      pgtype.Int8{Int64: 1, Valid: true},
		CreatedAt:   pgtype.Timestamptz{Time: now, Valid: true},
		UpdatedAt:   pgtype.Timestamptz{Time: now, Valid: true},
	}

	result, err := engine.GenerateForWorkspace(context.Background(), workspaceID)

	require.NoError(t, err)
	require.Len(t, result, 1)
	assert.Equal(t, domain.RecommendationTypeLowerBid, result[0].Type)
	require.NotNil(t, result[0].CampaignID)
	assert.Equal(t, campaignID, *result[0].CampaignID)
	assert.Contains(t, result[0].Description, "неэффективный расход")
}

func TestRecommendationEngineClosesResolvedCampaignLowerBidRecommendation(t *testing.T) {
	db := newRecommendationInMemDB()
	queries := sqlcgen.New(db)
	recommendationService := NewRecommendationService(queries)
	engine := NewRecommendationEngine(queries, recommendationService, nil, zerolog.Nop())

	workspaceID := uuid.New()
	campaignID := uuid.New()
	recommendationID := uuid.New()
	now := time.Now().UTC()
	db.campaigns = []sqlcgen.Campaign{{
		ID:          uuidToPgtype(campaignID),
		WorkspaceID: uuidToPgtype(workspaceID),
		Name:        "Campaign CPC Pressure",
		CreatedAt:   pgtype.Timestamptz{Time: now, Valid: true},
		UpdatedAt:   pgtype.Timestamptz{Time: now, Valid: true},
	}}
	db.campaignStats[campaignID] = sqlcgen.CampaignStat{
		ID:          uuidToPgtype(uuid.New()),
		CampaignID:  uuidToPgtype(campaignID),
		Date:        pgtype.Date{Time: now, Valid: true},
		Impressions: 1800,
		Clicks:      50,
		Spend:       4000,
		Orders:      pgtype.Int8{Int64: 5, Valid: true},
		CreatedAt:   pgtype.Timestamptz{Time: now, Valid: true},
		UpdatedAt:   pgtype.Timestamptz{Time: now, Valid: true},
	}
	db.recommendations[recommendationID] = sqlcgen.Recommendation{
		ID:          uuidToPgtype(recommendationID),
		WorkspaceID: uuidToPgtype(workspaceID),
		CampaignID:  uuidToPgtype(campaignID),
		Title:       "Campaign bid pressure is too high for current return",
		Description: "stale",
		Type:        domain.RecommendationTypeLowerBid,
		Severity:    domain.SeverityMedium,
		Status:      domain.RecommendationStatusActive,
		CreatedAt:   pgtype.Timestamptz{Time: now, Valid: true},
		UpdatedAt:   pgtype.Timestamptz{Time: now, Valid: true},
	}

	result, err := engine.GenerateForWorkspace(context.Background(), workspaceID)

	require.NoError(t, err)
	assert.Empty(t, result)
	assert.Equal(t, domain.RecommendationStatusCompleted, db.recommendations[recommendationID].Status)
}

func TestRecommendationEngineRefreshesExistingPhraseBidRecommendation(t *testing.T) {
	db := newRecommendationInMemDB()
	queries := sqlcgen.New(db)
	recommendationService := NewRecommendationService(queries)
	engine := NewRecommendationEngine(queries, recommendationService, nil, zerolog.Nop())

	workspaceID := uuid.New()
	phraseID := uuid.New()
	recommendationID := uuid.New()
	now := time.Now().UTC()
	confidence, err := numericFromFloat64(0.2)
	require.NoError(t, err)
	db.phrases = []sqlcgen.Phrase{{
		ID:          uuidToPgtype(phraseID),
		WorkspaceID: uuidToPgtype(workspaceID),
		Keyword:     "nike shoes",
		CurrentBid:  pgtype.Int8{Int64: 80, Valid: true},
		CreatedAt:   pgtype.Timestamptz{Time: now, Valid: true},
		UpdatedAt:   pgtype.Timestamptz{Time: now, Valid: true},
	}}
	db.phraseStats[phraseID] = sqlcgen.PhraseStat{
		ID:        uuidToPgtype(uuid.New()),
		PhraseID:  uuidToPgtype(phraseID),
		Date:      pgtype.Date{Time: now, Valid: true},
		Clicks:    25,
		Spend:     500,
		CreatedAt: pgtype.Timestamptz{Time: now, Valid: true},
		UpdatedAt: pgtype.Timestamptz{Time: now, Valid: true},
	}
	db.bidSnapshots[phraseID] = sqlcgen.BidSnapshot{
		ID:             uuidToPgtype(uuid.New()),
		PhraseID:       uuidToPgtype(phraseID),
		WorkspaceID:    uuidToPgtype(workspaceID),
		CompetitiveBid: 120,
		LeadershipBid:  140,
		CpmMin:         100,
		CapturedAt:     pgtype.Timestamptz{Time: now, Valid: true},
		CreatedAt:      pgtype.Timestamptz{Time: now, Valid: true},
	}
	db.recommendations[recommendationID] = sqlcgen.Recommendation{
		ID:            uuidToPgtype(recommendationID),
		WorkspaceID:   uuidToPgtype(workspaceID),
		PhraseID:      uuidToPgtype(phraseID),
		Title:         "Old phrase bid title",
		Description:   "old",
		Type:          domain.RecommendationTypeRaiseBid,
		Severity:      domain.SeverityLow,
		Confidence:    confidence,
		SourceMetrics: []byte(`{"clicks":1}`),
		Status:        domain.RecommendationStatusActive,
		CreatedAt:     pgtype.Timestamptz{Time: now, Valid: true},
		UpdatedAt:     pgtype.Timestamptz{Time: now, Valid: true},
	}

	result, err := engine.GenerateForWorkspace(context.Background(), workspaceID)

	require.NoError(t, err)
	require.Len(t, result, 1)
	assert.Equal(t, recommendationID, result[0].ID)
	assert.Equal(t, domain.RecommendationTypeRaiseBid, result[0].Type)
	assert.Len(t, db.recommendations, 1)
	assert.Equal(t, domain.SeverityMedium, db.recommendations[recommendationID].Severity)
	assert.Contains(t, db.recommendations[recommendationID].Title, "Конкурентная ставка")
}

func TestRecommendationEngineCreatesPhraseLowCTRRecommendation(t *testing.T) {
	db := newRecommendationInMemDB()
	queries := sqlcgen.New(db)
	recommendationService := NewRecommendationService(queries)
	engine := NewRecommendationEngine(queries, recommendationService, nil, zerolog.Nop())

	workspaceID := uuid.New()
	phraseID := uuid.New()
	now := time.Now().UTC()
	db.phrases = []sqlcgen.Phrase{{
		ID:          uuidToPgtype(phraseID),
		WorkspaceID: uuidToPgtype(workspaceID),
		Keyword:     "iphone 16 case",
		Count:       pgtype.Int4{Int32: 1200, Valid: true},
		CreatedAt:   pgtype.Timestamptz{Time: now, Valid: true},
		UpdatedAt:   pgtype.Timestamptz{Time: now, Valid: true},
	}}
	db.phraseStats[phraseID] = sqlcgen.PhraseStat{
		ID:          uuidToPgtype(uuid.New()),
		PhraseID:    uuidToPgtype(phraseID),
		Date:        pgtype.Date{Time: now, Valid: true},
		Impressions: 900,
		Clicks:      0,
		Spend:       0,
		CreatedAt:   pgtype.Timestamptz{Time: now, Valid: true},
		UpdatedAt:   pgtype.Timestamptz{Time: now, Valid: true},
	}

	result, err := engine.GenerateForWorkspace(context.Background(), workspaceID)

	require.NoError(t, err)
	require.Len(t, result, 1)
	assert.Equal(t, domain.RecommendationTypeLowCTR, result[0].Type)
	require.NotNil(t, result[0].PhraseID)
	assert.Equal(t, phraseID, *result[0].PhraseID)
	assert.Contains(t, result[0].Description, "900 показов без кликов")
}

func TestRecommendationEngineCreatesLowerBidRecommendationForOverbidPhrase(t *testing.T) {
	db := newRecommendationInMemDB()
	queries := sqlcgen.New(db)
	recommendationService := NewRecommendationService(queries)
	engine := NewRecommendationEngine(queries, recommendationService, nil, zerolog.Nop())

	workspaceID := uuid.New()
	phraseID := uuid.New()
	now := time.Now().UTC()
	db.phrases = []sqlcgen.Phrase{{
		ID:          uuidToPgtype(phraseID),
		WorkspaceID: uuidToPgtype(workspaceID),
		Keyword:     "nike running shoes",
		CurrentBid:  pgtype.Int8{Int64: 180, Valid: true},
		CreatedAt:   pgtype.Timestamptz{Time: now, Valid: true},
		UpdatedAt:   pgtype.Timestamptz{Time: now, Valid: true},
	}}
	db.phraseStats[phraseID] = sqlcgen.PhraseStat{
		ID:          uuidToPgtype(uuid.New()),
		PhraseID:    uuidToPgtype(phraseID),
		Date:        pgtype.Date{Time: now, Valid: true},
		Impressions: 700,
		Clicks:      0,
		Spend:       0,
		CreatedAt:   pgtype.Timestamptz{Time: now, Valid: true},
		UpdatedAt:   pgtype.Timestamptz{Time: now, Valid: true},
	}
	db.bidSnapshots[phraseID] = sqlcgen.BidSnapshot{
		ID:             uuidToPgtype(uuid.New()),
		PhraseID:       uuidToPgtype(phraseID),
		WorkspaceID:    uuidToPgtype(workspaceID),
		CompetitiveBid: 120,
		LeadershipBid:  150,
		CpmMin:         100,
		CapturedAt:     pgtype.Timestamptz{Time: now, Valid: true},
		CreatedAt:      pgtype.Timestamptz{Time: now, Valid: true},
	}

	result, err := engine.GenerateForWorkspace(context.Background(), workspaceID)

	require.NoError(t, err)
	require.Len(t, result, 2)
	assert.Contains(t, []string{result[0].Type, result[1].Type}, domain.RecommendationTypeLowerBid)
}

func TestRecommendationEngineClosesResolvedPhraseLowCTRRecommendation(t *testing.T) {
	db := newRecommendationInMemDB()
	queries := sqlcgen.New(db)
	recommendationService := NewRecommendationService(queries)
	engine := NewRecommendationEngine(queries, recommendationService, nil, zerolog.Nop())

	workspaceID := uuid.New()
	phraseID := uuid.New()
	recommendationID := uuid.New()
	now := time.Now().UTC()
	db.phrases = []sqlcgen.Phrase{{
		ID:          uuidToPgtype(phraseID),
		WorkspaceID: uuidToPgtype(workspaceID),
		Keyword:     "iphone 16 case",
		CreatedAt:   pgtype.Timestamptz{Time: now, Valid: true},
		UpdatedAt:   pgtype.Timestamptz{Time: now, Valid: true},
	}}
	db.phraseStats[phraseID] = sqlcgen.PhraseStat{
		ID:          uuidToPgtype(uuid.New()),
		PhraseID:    uuidToPgtype(phraseID),
		Date:        pgtype.Date{Time: now, Valid: true},
		Impressions: 900,
		Clicks:      15,
		Spend:       300,
		CreatedAt:   pgtype.Timestamptz{Time: now, Valid: true},
		UpdatedAt:   pgtype.Timestamptz{Time: now, Valid: true},
	}
	db.recommendations[recommendationID] = sqlcgen.Recommendation{
		ID:          uuidToPgtype(recommendationID),
		WorkspaceID: uuidToPgtype(workspaceID),
		PhraseID:    uuidToPgtype(phraseID),
		Title:       "Phrase collects impressions without clicks",
		Description: "stale",
		Type:        domain.RecommendationTypeLowCTR,
		Severity:    domain.SeverityHigh,
		Status:      domain.RecommendationStatusActive,
		CreatedAt:   pgtype.Timestamptz{Time: now, Valid: true},
		UpdatedAt:   pgtype.Timestamptz{Time: now, Valid: true},
	}

	result, err := engine.GenerateForWorkspace(context.Background(), workspaceID)

	require.NoError(t, err)
	assert.Empty(t, result)
	assert.Equal(t, domain.RecommendationStatusCompleted, db.recommendations[recommendationID].Status)
}

func TestRecommendationEngineClosesResolvedLowerBidRecommendation(t *testing.T) {
	db := newRecommendationInMemDB()
	queries := sqlcgen.New(db)
	recommendationService := NewRecommendationService(queries)
	engine := NewRecommendationEngine(queries, recommendationService, nil, zerolog.Nop())

	workspaceID := uuid.New()
	phraseID := uuid.New()
	recommendationID := uuid.New()
	now := time.Now().UTC()
	db.phrases = []sqlcgen.Phrase{{
		ID:          uuidToPgtype(phraseID),
		WorkspaceID: uuidToPgtype(workspaceID),
		Keyword:     "nike running shoes",
		CurrentBid:  pgtype.Int8{Int64: 110, Valid: true},
		CreatedAt:   pgtype.Timestamptz{Time: now, Valid: true},
		UpdatedAt:   pgtype.Timestamptz{Time: now, Valid: true},
	}}
	db.phraseStats[phraseID] = sqlcgen.PhraseStat{
		ID:          uuidToPgtype(uuid.New()),
		PhraseID:    uuidToPgtype(phraseID),
		Date:        pgtype.Date{Time: now, Valid: true},
		Impressions: 700,
		Clicks:      5,
		Spend:       250,
		CreatedAt:   pgtype.Timestamptz{Time: now, Valid: true},
		UpdatedAt:   pgtype.Timestamptz{Time: now, Valid: true},
	}
	db.bidSnapshots[phraseID] = sqlcgen.BidSnapshot{
		ID:             uuidToPgtype(uuid.New()),
		PhraseID:       uuidToPgtype(phraseID),
		WorkspaceID:    uuidToPgtype(workspaceID),
		CompetitiveBid: 120,
		LeadershipBid:  150,
		CpmMin:         100,
		CapturedAt:     pgtype.Timestamptz{Time: now, Valid: true},
		CreatedAt:      pgtype.Timestamptz{Time: now, Valid: true},
	}
	db.recommendations[recommendationID] = sqlcgen.Recommendation{
		ID:          uuidToPgtype(recommendationID),
		WorkspaceID: uuidToPgtype(workspaceID),
		PhraseID:    uuidToPgtype(phraseID),
		Title:       "Current phrase bid is too high for weak engagement",
		Description: "stale",
		Type:        domain.RecommendationTypeLowerBid,
		Severity:    domain.SeverityMedium,
		Status:      domain.RecommendationStatusActive,
		CreatedAt:   pgtype.Timestamptz{Time: now, Valid: true},
		UpdatedAt:   pgtype.Timestamptz{Time: now, Valid: true},
	}

	result, err := engine.GenerateForWorkspace(context.Background(), workspaceID)

	require.NoError(t, err)
	types := make([]string, 0, len(result))
	for _, recommendation := range result {
		types = append(types, recommendation.Type)
	}
	assert.NotContains(t, types, domain.RecommendationTypeLowerBid)
	assert.Equal(t, domain.RecommendationStatusCompleted, db.recommendations[recommendationID].Status)
}

func TestRecommendationEngineCreatesPositionDropRecommendationFromTrackingTarget(t *testing.T) {
	db := newRecommendationInMemDB()
	queries := sqlcgen.New(db)
	recommendationService := NewRecommendationService(queries)
	engine := NewRecommendationEngine(queries, recommendationService, nil, zerolog.Nop())

	workspaceID := uuid.New()
	productID := uuid.New()
	targetID := uuid.New()
	latestSnapshotID := uuid.New()
	previousSnapshotID := uuid.New()
	now := time.Now().UTC()

	db.products = []sqlcgen.Product{{
		ID:          uuidToPgtype(productID),
		WorkspaceID: uuidToPgtype(workspaceID),
		WbProductID: 111,
		Title:       "Our Product",
		CreatedAt:   pgtype.Timestamptz{Time: now, Valid: true},
		UpdatedAt:   pgtype.Timestamptz{Time: now, Valid: true},
	}}
	db.trackingTargets = []sqlcgen.PositionTrackingTarget{{
		ID:                uuidToPgtype(targetID),
		WorkspaceID:       uuidToPgtype(workspaceID),
		ProductID:         uuidToPgtype(productID),
		Query:             "iphone",
		Region:            "msk",
		IsActive:          true,
		BaselinePosition:  pgtype.Int4{Int32: 4, Valid: true},
		BaselineCheckedAt: pgtype.Timestamptz{Time: now.Add(-2 * time.Hour), Valid: true},
		CreatedAt:         pgtype.Timestamptz{Time: now.Add(-2 * time.Hour), Valid: true},
		UpdatedAt:         pgtype.Timestamptz{Time: now.Add(-2 * time.Hour), Valid: true},
	}}
	db.positions[productID] = []sqlcgen.Position{
		{
			ID:          uuidToPgtype(uuid.New()),
			WorkspaceID: uuidToPgtype(workspaceID),
			ProductID:   uuidToPgtype(productID),
			Query:       "iphone",
			Region:      "msk",
			Position:    18,
			Page:        2,
			Source:      "manual",
			CheckedAt:   pgtype.Timestamptz{Time: now, Valid: true},
			CreatedAt:   pgtype.Timestamptz{Time: now, Valid: true},
		},
	}
	db.serpSnapshots = []sqlcgen.SerpSnapshot{
		{
			ID:           uuidToPgtype(latestSnapshotID),
			WorkspaceID:  uuidToPgtype(workspaceID),
			Query:        "iphone",
			Region:       "msk",
			TotalResults: 100,
			ScannedAt:    pgtype.Timestamptz{Time: now, Valid: true},
			CreatedAt:    pgtype.Timestamptz{Time: now, Valid: true},
		},
		{
			ID:           uuidToPgtype(previousSnapshotID),
			WorkspaceID:  uuidToPgtype(workspaceID),
			Query:        "iphone",
			Region:       "msk",
			TotalResults: 100,
			ScannedAt:    pgtype.Timestamptz{Time: now.Add(-1 * time.Hour), Valid: true},
			CreatedAt:    pgtype.Timestamptz{Time: now.Add(-1 * time.Hour), Valid: true},
		},
	}
	db.serpItems[latestSnapshotID] = []sqlcgen.SerpResultItem{{
		ID:          uuidToPgtype(uuid.New()),
		SnapshotID:  uuidToPgtype(latestSnapshotID),
		Position:    1,
		WbProductID: 999,
		Title:       "Competitor",
		CreatedAt:   pgtype.Timestamptz{Time: now, Valid: true},
	}}
	db.serpItems[previousSnapshotID] = []sqlcgen.SerpResultItem{{
		ID:          uuidToPgtype(uuid.New()),
		SnapshotID:  uuidToPgtype(previousSnapshotID),
		Position:    3,
		WbProductID: 111,
		Title:       "Our Product",
		CreatedAt:   pgtype.Timestamptz{Time: now.Add(-1 * time.Hour), Valid: true},
	}}

	result, err := engine.GenerateForWorkspace(context.Background(), workspaceID)

	require.NoError(t, err)
	require.Len(t, result, 2)

	var positionDrop *domain.Recommendation
	for i := range result {
		if result[i].Type == domain.RecommendationTypePositionDrop {
			positionDrop = &result[i]
			break
		}
	}
	require.NotNil(t, positionDrop)
	assert.Equal(t, domain.SeverityHigh, positionDrop.Severity)
	assert.Contains(t, positionDrop.Description, "с позиции 4 до 18")

	var metrics map[string]any
	require.NoError(t, json.Unmarshal(positionDrop.SourceMetrics, &metrics))
	assert.Equal(t, "iphone", metrics["query"])
	assert.Equal(t, "msk", metrics["region"])
	assert.Equal(t, true, metrics["serp_pressure"])
	assert.Equal(t, true, metrics["serp_own_lost"])
}

func TestRecommendationEngineCreatesNewCompetitorRecommendationFromSERP(t *testing.T) {
	db := newRecommendationInMemDB()
	queries := sqlcgen.New(db)
	recommendationService := NewRecommendationService(queries)
	engine := NewRecommendationEngine(queries, recommendationService, nil, zerolog.Nop())

	workspaceID := uuid.New()
	productID := uuid.New()
	now := time.Now().UTC()
	latestSnapshotID := uuid.New()
	previousSnapshotID := uuid.New()

	db.products = []sqlcgen.Product{{
		ID:          uuidToPgtype(productID),
		WorkspaceID: uuidToPgtype(workspaceID),
		WbProductID: 111,
		Title:       "Our Product",
		CreatedAt:   pgtype.Timestamptz{Time: now, Valid: true},
		UpdatedAt:   pgtype.Timestamptz{Time: now, Valid: true},
	}}
	db.serpSnapshots = []sqlcgen.SerpSnapshot{
		{
			ID:           uuidToPgtype(latestSnapshotID),
			WorkspaceID:  uuidToPgtype(workspaceID),
			Query:        "iphone",
			Region:       "msk",
			TotalResults: 100,
			ScannedAt:    pgtype.Timestamptz{Time: now, Valid: true},
			CreatedAt:    pgtype.Timestamptz{Time: now, Valid: true},
		},
		{
			ID:           uuidToPgtype(previousSnapshotID),
			WorkspaceID:  uuidToPgtype(workspaceID),
			Query:        "iphone",
			Region:       "msk",
			TotalResults: 100,
			ScannedAt:    pgtype.Timestamptz{Time: now.Add(-1 * time.Hour), Valid: true},
			CreatedAt:    pgtype.Timestamptz{Time: now.Add(-1 * time.Hour), Valid: true},
		},
	}
	db.serpItems[latestSnapshotID] = []sqlcgen.SerpResultItem{
		{
			ID:          uuidToPgtype(uuid.New()),
			SnapshotID:  uuidToPgtype(latestSnapshotID),
			Position:    1,
			WbProductID: 999,
			Title:       "Competitor",
			CreatedAt:   pgtype.Timestamptz{Time: now, Valid: true},
		},
	}
	db.serpItems[previousSnapshotID] = []sqlcgen.SerpResultItem{
		{
			ID:          uuidToPgtype(uuid.New()),
			SnapshotID:  uuidToPgtype(previousSnapshotID),
			Position:    2,
			WbProductID: 111,
			Title:       "Our Product",
			CreatedAt:   pgtype.Timestamptz{Time: now.Add(-1 * time.Hour), Valid: true},
		},
	}

	result, err := engine.GenerateForWorkspace(context.Background(), workspaceID)

	require.NoError(t, err)
	require.Len(t, result, 1)
	assert.Equal(t, domain.RecommendationTypeNewCompetitor, result[0].Type)
	require.NotNil(t, result[0].ProductID)
	assert.Equal(t, productID, *result[0].ProductID)
	assert.Contains(t, result[0].Description, "исчез из выдачи")
}

func TestRecommendationEngineClosesResolvedNewCompetitorRecommendation(t *testing.T) {
	db := newRecommendationInMemDB()
	queries := sqlcgen.New(db)
	recommendationService := NewRecommendationService(queries)
	engine := NewRecommendationEngine(queries, recommendationService, nil, zerolog.Nop())

	workspaceID := uuid.New()
	productID := uuid.New()
	recommendationID := uuid.New()
	now := time.Now().UTC()
	latestSnapshotID := uuid.New()
	previousSnapshotID := uuid.New()

	db.products = []sqlcgen.Product{{
		ID:          uuidToPgtype(productID),
		WorkspaceID: uuidToPgtype(workspaceID),
		WbProductID: 111,
		Title:       "Our Product",
		CreatedAt:   pgtype.Timestamptz{Time: now, Valid: true},
		UpdatedAt:   pgtype.Timestamptz{Time: now, Valid: true},
	}}
	db.serpSnapshots = []sqlcgen.SerpSnapshot{
		{
			ID:           uuidToPgtype(latestSnapshotID),
			WorkspaceID:  uuidToPgtype(workspaceID),
			Query:        "iphone",
			Region:       "msk",
			TotalResults: 100,
			ScannedAt:    pgtype.Timestamptz{Time: now, Valid: true},
			CreatedAt:    pgtype.Timestamptz{Time: now, Valid: true},
		},
		{
			ID:           uuidToPgtype(previousSnapshotID),
			WorkspaceID:  uuidToPgtype(workspaceID),
			Query:        "iphone",
			Region:       "msk",
			TotalResults: 100,
			ScannedAt:    pgtype.Timestamptz{Time: now.Add(-1 * time.Hour), Valid: true},
			CreatedAt:    pgtype.Timestamptz{Time: now.Add(-1 * time.Hour), Valid: true},
		},
	}
	db.serpItems[latestSnapshotID] = []sqlcgen.SerpResultItem{
		{
			ID:          uuidToPgtype(uuid.New()),
			SnapshotID:  uuidToPgtype(latestSnapshotID),
			Position:    2,
			WbProductID: 111,
			Title:       "Our Product",
			CreatedAt:   pgtype.Timestamptz{Time: now, Valid: true},
		},
	}
	db.serpItems[previousSnapshotID] = []sqlcgen.SerpResultItem{
		{
			ID:          uuidToPgtype(uuid.New()),
			SnapshotID:  uuidToPgtype(previousSnapshotID),
			Position:    2,
			WbProductID: 111,
			Title:       "Our Product",
			CreatedAt:   pgtype.Timestamptz{Time: now.Add(-1 * time.Hour), Valid: true},
		},
	}
	db.recommendations[recommendationID] = sqlcgen.Recommendation{
		ID:          uuidToPgtype(recommendationID),
		WorkspaceID: uuidToPgtype(workspaceID),
		ProductID:   uuidToPgtype(productID),
		Title:       "New competitor displaced the product in search",
		Description: "stale",
		Type:        domain.RecommendationTypeNewCompetitor,
		Severity:    domain.SeverityMedium,
		Status:      domain.RecommendationStatusActive,
		CreatedAt:   pgtype.Timestamptz{Time: now, Valid: true},
		UpdatedAt:   pgtype.Timestamptz{Time: now, Valid: true},
	}

	result, err := engine.GenerateForWorkspace(context.Background(), workspaceID)

	require.NoError(t, err)
	assert.Empty(t, result)
	assert.Equal(t, domain.RecommendationStatusCompleted, db.recommendations[recommendationID].Status)
}
