package sqlcgen

import (
	"context"

	"github.com/jackc/pgx/v5/pgtype"
)

// Strategy represents a row from the strategies table.
type Strategy struct {
	ID              pgtype.UUID        `json:"id"`
	WorkspaceID     pgtype.UUID        `json:"workspace_id"`
	SellerCabinetID pgtype.UUID        `json:"seller_cabinet_id"`
	Name            string             `json:"name"`
	Type            string             `json:"type"`
	Params          []byte             `json:"params"`
	IsActive        bool               `json:"is_active"`
	CreatedAt       pgtype.Timestamptz `json:"created_at"`
	UpdatedAt       pgtype.Timestamptz `json:"updated_at"`
}

type StrategyBinding struct {
	ID         pgtype.UUID        `json:"id"`
	StrategyID pgtype.UUID        `json:"strategy_id"`
	CampaignID pgtype.UUID        `json:"campaign_id"`
	ProductID  pgtype.UUID        `json:"product_id"`
	CreatedAt  pgtype.Timestamptz `json:"created_at"`
}

type DaypartingState struct {
	StrategyID    pgtype.UUID        `json:"strategy_id"`
	CampaignID    pgtype.UUID        `json:"campaign_id"`
	ProductID     pgtype.UUID        `json:"product_id"`
	ScopeKey      string             `json:"scope_key"`
	Placement     string             `json:"placement"`
	BaselineBid   int32              `json:"baseline_bid"`
	LastTargetBid int32              `json:"last_target_bid"`
	LastSlot      string             `json:"last_slot"`
	UpdatedAt     pgtype.Timestamptz `json:"updated_at"`
}

type BidChange struct {
	ID               pgtype.UUID        `json:"id"`
	WorkspaceID      pgtype.UUID        `json:"workspace_id"`
	SellerCabinetID  pgtype.UUID        `json:"seller_cabinet_id"`
	CampaignID       pgtype.UUID        `json:"campaign_id"`
	ProductID        pgtype.UUID        `json:"product_id"`
	PhraseID         pgtype.UUID        `json:"phrase_id"`
	StrategyID       pgtype.UUID        `json:"strategy_id"`
	RecommendationID pgtype.UUID        `json:"recommendation_id"`
	Placement        string             `json:"placement"`
	OldBid           int32              `json:"old_bid"`
	NewBid           int32              `json:"new_bid"`
	Reason           string             `json:"reason"`
	Source           string             `json:"source"`
	Acos             pgtype.Float8      `json:"acos"`
	Roas             pgtype.Float8      `json:"roas"`
	WbStatus         string             `json:"wb_status"`
	CreatedAt        pgtype.Timestamptz `json:"created_at"`
}

type CampaignMinusPhrase struct {
	ID         pgtype.UUID        `json:"id"`
	CampaignID pgtype.UUID        `json:"campaign_id"`
	Phrase     string             `json:"phrase"`
	CreatedAt  pgtype.Timestamptz `json:"created_at"`
}

type CampaignPlusPhrase struct {
	ID         pgtype.UUID        `json:"id"`
	CampaignID pgtype.UUID        `json:"campaign_id"`
	Phrase     string             `json:"phrase"`
	CreatedAt  pgtype.Timestamptz `json:"created_at"`
}

// --- Strategy CRUD ---

type CreateStrategyParams struct {
	WorkspaceID     pgtype.UUID `json:"workspace_id"`
	SellerCabinetID pgtype.UUID `json:"seller_cabinet_id"`
	Name            string      `json:"name"`
	Type            string      `json:"type"`
	Params          []byte      `json:"params"`
	IsActive        bool        `json:"is_active"`
}

func (q *Queries) CreateStrategy(ctx context.Context, arg CreateStrategyParams) (Strategy, error) {
	row := q.db.QueryRow(ctx,
		`INSERT INTO strategies (workspace_id, seller_cabinet_id, name, type, params, is_active) VALUES ($1,$2,$3,$4,$5,$6) RETURNING id, workspace_id, seller_cabinet_id, name, type, params, is_active, created_at, updated_at`,
		arg.WorkspaceID, arg.SellerCabinetID, arg.Name, arg.Type, arg.Params, arg.IsActive)
	var i Strategy
	err := row.Scan(&i.ID, &i.WorkspaceID, &i.SellerCabinetID, &i.Name, &i.Type, &i.Params, &i.IsActive, &i.CreatedAt, &i.UpdatedAt)
	return i, err
}

func (q *Queries) GetStrategyByID(ctx context.Context, id pgtype.UUID) (Strategy, error) {
	row := q.db.QueryRow(ctx, `SELECT id, workspace_id, seller_cabinet_id, name, type, params, is_active, created_at, updated_at FROM strategies WHERE id = $1`, id)
	var i Strategy
	err := row.Scan(&i.ID, &i.WorkspaceID, &i.SellerCabinetID, &i.Name, &i.Type, &i.Params, &i.IsActive, &i.CreatedAt, &i.UpdatedAt)
	return i, err
}

type GetStrategyByIDAndWorkspaceParams struct {
	ID          pgtype.UUID
	WorkspaceID pgtype.UUID
}

func (q *Queries) GetStrategyByIDAndWorkspace(ctx context.Context, arg GetStrategyByIDAndWorkspaceParams) (Strategy, error) {
	row := q.db.QueryRow(ctx, `SELECT id, workspace_id, seller_cabinet_id, name, type, params, is_active, created_at, updated_at FROM strategies WHERE id = $1 AND workspace_id = $2`, arg.ID, arg.WorkspaceID)
	var i Strategy
	err := row.Scan(&i.ID, &i.WorkspaceID, &i.SellerCabinetID, &i.Name, &i.Type, &i.Params, &i.IsActive, &i.CreatedAt, &i.UpdatedAt)
	return i, err
}

type ListStrategiesByWorkspaceParams struct {
	WorkspaceID           pgtype.UUID
	SellerCabinetIDFilter pgtype.UUID
	Limit                 int32
	Offset                int32
}

func (q *Queries) ListStrategiesByWorkspace(ctx context.Context, arg ListStrategiesByWorkspaceParams) ([]Strategy, error) {
	rows, err := q.db.Query(ctx, `SELECT id, workspace_id, seller_cabinet_id, name, type, params, is_active, created_at, updated_at FROM strategies WHERE workspace_id = $1 AND ($2::uuid IS NULL OR seller_cabinet_id = $2::uuid) ORDER BY created_at DESC LIMIT $3 OFFSET $4`, arg.WorkspaceID, arg.SellerCabinetIDFilter, arg.Limit, arg.Offset)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var items []Strategy
	for rows.Next() {
		var i Strategy
		if err := rows.Scan(&i.ID, &i.WorkspaceID, &i.SellerCabinetID, &i.Name, &i.Type, &i.Params, &i.IsActive, &i.CreatedAt, &i.UpdatedAt); err != nil {
			return nil, err
		}
		items = append(items, i)
	}
	return items, rows.Err()
}

func (q *Queries) ListActiveStrategiesByWorkspace(ctx context.Context, workspaceID pgtype.UUID) ([]Strategy, error) {
	rows, err := q.db.Query(ctx, `SELECT id, workspace_id, seller_cabinet_id, name, type, params, is_active, created_at, updated_at FROM strategies WHERE workspace_id = $1 AND is_active = true ORDER BY created_at DESC`, workspaceID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var items []Strategy
	for rows.Next() {
		var i Strategy
		if err := rows.Scan(&i.ID, &i.WorkspaceID, &i.SellerCabinetID, &i.Name, &i.Type, &i.Params, &i.IsActive, &i.CreatedAt, &i.UpdatedAt); err != nil {
			return nil, err
		}
		items = append(items, i)
	}
	return items, rows.Err()
}

type UpdateStrategyParams struct {
	ID       pgtype.UUID
	Name     string
	Type     string
	Params   []byte
	IsActive bool
}

type UpdateStrategyInWorkspaceParams struct {
	ID          pgtype.UUID
	WorkspaceID pgtype.UUID
	Name        string
	Type        string
	Params      []byte
	IsActive    bool
}

func (q *Queries) UpdateStrategy(ctx context.Context, arg UpdateStrategyParams) (Strategy, error) {
	row := q.db.QueryRow(ctx,
		`UPDATE strategies SET name=$2, type=$3, params=$4, is_active=$5, updated_at=now() WHERE id=$1 RETURNING id, workspace_id, seller_cabinet_id, name, type, params, is_active, created_at, updated_at`,
		arg.ID, arg.Name, arg.Type, arg.Params, arg.IsActive)
	var i Strategy
	err := row.Scan(&i.ID, &i.WorkspaceID, &i.SellerCabinetID, &i.Name, &i.Type, &i.Params, &i.IsActive, &i.CreatedAt, &i.UpdatedAt)
	return i, err
}

func (q *Queries) UpdateStrategyInWorkspace(ctx context.Context, arg UpdateStrategyInWorkspaceParams) (Strategy, error) {
	row := q.db.QueryRow(ctx,
		`UPDATE strategies SET name=$3, type=$4, params=$5, is_active=$6, updated_at=now() WHERE id=$1 AND workspace_id=$2 RETURNING id, workspace_id, seller_cabinet_id, name, type, params, is_active, created_at, updated_at`,
		arg.ID, arg.WorkspaceID, arg.Name, arg.Type, arg.Params, arg.IsActive)
	var i Strategy
	err := row.Scan(&i.ID, &i.WorkspaceID, &i.SellerCabinetID, &i.Name, &i.Type, &i.Params, &i.IsActive, &i.CreatedAt, &i.UpdatedAt)
	return i, err
}

func (q *Queries) DeleteStrategy(ctx context.Context, id pgtype.UUID) error {
	_, err := q.db.Exec(ctx, `DELETE FROM strategies WHERE id = $1`, id)
	return err
}

type DeleteStrategyInWorkspaceParams struct {
	ID          pgtype.UUID
	WorkspaceID pgtype.UUID
}

func (q *Queries) DeleteStrategyInWorkspace(ctx context.Context, arg DeleteStrategyInWorkspaceParams) error {
	_, err := q.db.Exec(ctx, `DELETE FROM strategies WHERE id = $1 AND workspace_id = $2`, arg.ID, arg.WorkspaceID)
	return err
}

// --- Bindings ---

type CreateStrategyBindingParams struct {
	StrategyID pgtype.UUID
	CampaignID pgtype.UUID
	ProductID  pgtype.UUID
}

type CreateStrategyBindingInWorkspaceParams struct {
	WorkspaceID pgtype.UUID
	StrategyID  pgtype.UUID
	CampaignID  pgtype.UUID
	ProductID   pgtype.UUID
}

func (q *Queries) CreateStrategyBinding(ctx context.Context, arg CreateStrategyBindingParams) (StrategyBinding, error) {
	row := q.db.QueryRow(ctx,
		`INSERT INTO strategy_bindings (strategy_id, campaign_id, product_id) VALUES ($1,$2,$3) RETURNING id, strategy_id, campaign_id, product_id, created_at`,
		arg.StrategyID, arg.CampaignID, arg.ProductID)
	var i StrategyBinding
	err := row.Scan(&i.ID, &i.StrategyID, &i.CampaignID, &i.ProductID, &i.CreatedAt)
	return i, err
}

func (q *Queries) CreateStrategyBindingInWorkspace(ctx context.Context, arg CreateStrategyBindingInWorkspaceParams) (StrategyBinding, error) {
	row := q.db.QueryRow(ctx, `
			INSERT INTO strategy_bindings (strategy_id, campaign_id, product_id)
			SELECT s.id, $3, $4
			FROM strategies s
			WHERE s.id = $2 AND s.workspace_id = $1
			AND (
				$3::uuid IS NULL OR EXISTS (
					SELECT 1 FROM campaigns c
					WHERE c.id = $3 AND c.workspace_id = $1
					  AND c.seller_cabinet_id = s.seller_cabinet_id
				)
			)
			AND (
				$4::uuid IS NULL OR EXISTS (
					SELECT 1 FROM products p
					WHERE p.id = $4 AND p.workspace_id = $1
					  AND p.seller_cabinet_id = s.seller_cabinet_id
				)
			)
			AND (
				$3::uuid IS NULL OR $4::uuid IS NULL OR EXISTS (
					SELECT 1 FROM campaign_products cp
					WHERE cp.campaign_id = $3 AND cp.product_id = $4
					  AND cp.workspace_id = $1
					  AND cp.seller_cabinet_id = s.seller_cabinet_id
				)
			)
			RETURNING id, strategy_id, campaign_id, product_id, created_at`,
		arg.WorkspaceID, arg.StrategyID, arg.CampaignID, arg.ProductID)
	var i StrategyBinding
	err := row.Scan(&i.ID, &i.StrategyID, &i.CampaignID, &i.ProductID, &i.CreatedAt)
	return i, err
}

type GetDaypartingStateParams struct {
	StrategyID pgtype.UUID
	CampaignID pgtype.UUID
	ScopeKey   string
	Placement  string
}

func (q *Queries) GetDaypartingState(ctx context.Context, arg GetDaypartingStateParams) (DaypartingState, error) {
	row := q.db.QueryRow(ctx, `SELECT strategy_id, campaign_id, product_id, scope_key, placement, baseline_bid, last_target_bid, last_slot, updated_at
		FROM dayparting_states
		WHERE strategy_id = $1 AND campaign_id = $2 AND scope_key = $3 AND placement = $4`,
		arg.StrategyID, arg.CampaignID, arg.ScopeKey, arg.Placement)
	var state DaypartingState
	err := row.Scan(&state.StrategyID, &state.CampaignID, &state.ProductID, &state.ScopeKey, &state.Placement,
		&state.BaselineBid, &state.LastTargetBid, &state.LastSlot, &state.UpdatedAt)
	return state, err
}

type UpsertDaypartingStateParams struct {
	StrategyID    pgtype.UUID
	CampaignID    pgtype.UUID
	ProductID     pgtype.UUID
	ScopeKey      string
	Placement     string
	BaselineBid   int32
	LastTargetBid int32
	LastSlot      string
}

func (q *Queries) UpsertDaypartingState(ctx context.Context, arg UpsertDaypartingStateParams) error {
	_, err := q.db.Exec(ctx, `INSERT INTO dayparting_states
		(strategy_id, campaign_id, product_id, scope_key, placement, baseline_bid, last_target_bid, last_slot)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8)
		ON CONFLICT (strategy_id, campaign_id, scope_key, placement) DO UPDATE SET
		product_id = EXCLUDED.product_id,
		baseline_bid = EXCLUDED.baseline_bid,
		last_target_bid = EXCLUDED.last_target_bid,
		last_slot = EXCLUDED.last_slot,
		updated_at = now()`,
		arg.StrategyID, arg.CampaignID, arg.ProductID, arg.ScopeKey, arg.Placement,
		arg.BaselineBid, arg.LastTargetBid, arg.LastSlot)
	return err
}

func (q *Queries) ListStrategyBindings(ctx context.Context, strategyID pgtype.UUID) ([]StrategyBinding, error) {
	rows, err := q.db.Query(ctx, `SELECT id, strategy_id, campaign_id, product_id, created_at FROM strategy_bindings WHERE strategy_id = $1`, strategyID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var items []StrategyBinding
	for rows.Next() {
		var i StrategyBinding
		if err := rows.Scan(&i.ID, &i.StrategyID, &i.CampaignID, &i.ProductID, &i.CreatedAt); err != nil {
			return nil, err
		}
		items = append(items, i)
	}
	return items, rows.Err()
}

func (q *Queries) ListActiveStrategyBindingsByWorkspace(ctx context.Context, workspaceID pgtype.UUID) ([]StrategyBinding, error) {
	rows, err := q.db.Query(ctx, `SELECT b.id, b.strategy_id, b.campaign_id, b.product_id, b.created_at FROM strategy_bindings b JOIN strategies s ON s.id = b.strategy_id WHERE s.workspace_id = $1 AND s.is_active = true`, workspaceID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var items []StrategyBinding
	for rows.Next() {
		var i StrategyBinding
		if err := rows.Scan(&i.ID, &i.StrategyID, &i.CampaignID, &i.ProductID, &i.CreatedAt); err != nil {
			return nil, err
		}
		items = append(items, i)
	}
	return items, rows.Err()
}

func (q *Queries) DeleteStrategyBinding(ctx context.Context, id pgtype.UUID) error {
	_, err := q.db.Exec(ctx, `DELETE FROM strategy_bindings WHERE id = $1`, id)
	return err
}

type DeleteStrategyBindingInWorkspaceParams struct {
	ID          pgtype.UUID
	WorkspaceID pgtype.UUID
}

func (q *Queries) DeleteStrategyBindingInWorkspace(ctx context.Context, arg DeleteStrategyBindingInWorkspaceParams) error {
	_, err := q.db.Exec(ctx, `DELETE FROM strategy_bindings b USING strategies s WHERE b.id = $1 AND b.strategy_id = s.id AND s.workspace_id = $2`, arg.ID, arg.WorkspaceID)
	return err
}

// --- Bid Changes ---

type CreateBidChangeParams struct {
	WorkspaceID      pgtype.UUID
	SellerCabinetID  pgtype.UUID
	CampaignID       pgtype.UUID
	ProductID        pgtype.UUID
	PhraseID         pgtype.UUID
	StrategyID       pgtype.UUID
	RecommendationID pgtype.UUID
	Placement        string
	OldBid           int32
	NewBid           int32
	Reason           string
	Source           string
	Acos             pgtype.Float8
	Roas             pgtype.Float8
	WbStatus         string
}

func (q *Queries) CreateBidChange(ctx context.Context, arg CreateBidChangeParams) (BidChange, error) {
	row := q.db.QueryRow(ctx,
		`INSERT INTO bid_changes (workspace_id, seller_cabinet_id, campaign_id, product_id, phrase_id, strategy_id, recommendation_id, placement, old_bid, new_bid, reason, source, acos, roas, wb_status)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15)
		RETURNING id, workspace_id, seller_cabinet_id, campaign_id, product_id, phrase_id, strategy_id, recommendation_id, placement, old_bid, new_bid, reason, source, acos, roas, wb_status, created_at`,
		arg.WorkspaceID, arg.SellerCabinetID, arg.CampaignID, arg.ProductID, arg.PhraseID,
		arg.StrategyID, arg.RecommendationID, arg.Placement, arg.OldBid, arg.NewBid,
		arg.Reason, arg.Source, arg.Acos, arg.Roas, arg.WbStatus)
	var i BidChange
	err := row.Scan(&i.ID, &i.WorkspaceID, &i.SellerCabinetID, &i.CampaignID, &i.ProductID, &i.PhraseID, &i.StrategyID, &i.RecommendationID, &i.Placement, &i.OldBid, &i.NewBid, &i.Reason, &i.Source, &i.Acos, &i.Roas, &i.WbStatus, &i.CreatedAt)
	return i, err
}

type ListBidChangesByCampaignParams struct {
	CampaignID pgtype.UUID
	Limit      int32
	Offset     int32
}

func (q *Queries) ListBidChangesByCampaign(ctx context.Context, arg ListBidChangesByCampaignParams) ([]BidChange, error) {
	rows, err := q.db.Query(ctx, `SELECT id, workspace_id, seller_cabinet_id, campaign_id, product_id, phrase_id, strategy_id, recommendation_id, placement, old_bid, new_bid, reason, source, acos, roas, wb_status, created_at FROM bid_changes WHERE campaign_id = $1 ORDER BY created_at DESC LIMIT $2 OFFSET $3`, arg.CampaignID, arg.Limit, arg.Offset)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var items []BidChange
	for rows.Next() {
		var i BidChange
		if err := rows.Scan(&i.ID, &i.WorkspaceID, &i.SellerCabinetID, &i.CampaignID, &i.ProductID, &i.PhraseID, &i.StrategyID, &i.RecommendationID, &i.Placement, &i.OldBid, &i.NewBid, &i.Reason, &i.Source, &i.Acos, &i.Roas, &i.WbStatus, &i.CreatedAt); err != nil {
			return nil, err
		}
		items = append(items, i)
	}
	return items, rows.Err()
}

type ListBidChangesByWorkspaceParams struct {
	WorkspaceID pgtype.UUID
	Limit       int32
	Offset      int32
}

func (q *Queries) ListBidChangesByWorkspace(ctx context.Context, arg ListBidChangesByWorkspaceParams) ([]BidChange, error) {
	rows, err := q.db.Query(ctx, `SELECT id, workspace_id, seller_cabinet_id, campaign_id, product_id, phrase_id, strategy_id, recommendation_id, placement, old_bid, new_bid, reason, source, acos, roas, wb_status, created_at FROM bid_changes WHERE workspace_id = $1 ORDER BY created_at DESC LIMIT $2 OFFSET $3`, arg.WorkspaceID, arg.Limit, arg.Offset)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var items []BidChange
	for rows.Next() {
		var i BidChange
		if err := rows.Scan(&i.ID, &i.WorkspaceID, &i.SellerCabinetID, &i.CampaignID, &i.ProductID, &i.PhraseID, &i.StrategyID, &i.RecommendationID, &i.Placement, &i.OldBid, &i.NewBid, &i.Reason, &i.Source, &i.Acos, &i.Roas, &i.WbStatus, &i.CreatedAt); err != nil {
			return nil, err
		}
		items = append(items, i)
	}
	return items, rows.Err()
}

type ListBidChangesByWorkspaceSinceParams struct {
	WorkspaceID pgtype.UUID
	Since       pgtype.Timestamptz
	Limit       int32
	Offset      int32
}

func (q *Queries) ListBidChangesByWorkspaceSince(ctx context.Context, arg ListBidChangesByWorkspaceSinceParams) ([]BidChange, error) {
	rows, err := q.db.Query(ctx, `SELECT id, workspace_id, seller_cabinet_id, campaign_id, product_id, phrase_id, strategy_id, recommendation_id, placement, old_bid, new_bid, reason, source, acos, roas, wb_status, created_at FROM bid_changes WHERE workspace_id = $1 AND created_at >= $2 ORDER BY created_at DESC LIMIT $3 OFFSET $4`, arg.WorkspaceID, arg.Since, arg.Limit, arg.Offset)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var items []BidChange
	for rows.Next() {
		var i BidChange
		if err := rows.Scan(&i.ID, &i.WorkspaceID, &i.SellerCabinetID, &i.CampaignID, &i.ProductID, &i.PhraseID, &i.StrategyID, &i.RecommendationID, &i.Placement, &i.OldBid, &i.NewBid, &i.Reason, &i.Source, &i.Acos, &i.Roas, &i.WbStatus, &i.CreatedAt); err != nil {
			return nil, err
		}
		items = append(items, i)
	}
	return items, rows.Err()
}

type GetBidChangeByIDAndWorkspaceParams struct {
	ID          pgtype.UUID
	WorkspaceID pgtype.UUID
}

func (q *Queries) GetBidChangeByIDAndWorkspace(ctx context.Context, arg GetBidChangeByIDAndWorkspaceParams) (BidChange, error) {
	row := q.db.QueryRow(ctx, `SELECT id, workspace_id, seller_cabinet_id, campaign_id, product_id, phrase_id, strategy_id, recommendation_id, placement, old_bid, new_bid, reason, source, acos, roas, wb_status, created_at FROM bid_changes WHERE id = $1 AND workspace_id = $2`, arg.ID, arg.WorkspaceID)
	var i BidChange
	err := row.Scan(&i.ID, &i.WorkspaceID, &i.SellerCabinetID, &i.CampaignID, &i.ProductID, &i.PhraseID, &i.StrategyID, &i.RecommendationID, &i.Placement, &i.OldBid, &i.NewBid, &i.Reason, &i.Source, &i.Acos, &i.Roas, &i.WbStatus, &i.CreatedAt)
	return i, err
}

// --- Phrases ---

func (q *Queries) CreateMinusPhrase(ctx context.Context, campaignID pgtype.UUID, phrase string) (CampaignMinusPhrase, error) {
	row := q.db.QueryRow(ctx, `INSERT INTO campaign_minus_phrases (campaign_id, phrase) VALUES ($1,$2) ON CONFLICT (campaign_id, phrase) DO UPDATE SET phrase = EXCLUDED.phrase RETURNING id, campaign_id, phrase, created_at`, campaignID, phrase)
	var i CampaignMinusPhrase
	err := row.Scan(&i.ID, &i.CampaignID, &i.Phrase, &i.CreatedAt)
	return i, err
}

func (q *Queries) ListMinusPhrases(ctx context.Context, campaignID pgtype.UUID) ([]CampaignMinusPhrase, error) {
	rows, err := q.db.Query(ctx, `SELECT id, campaign_id, phrase, created_at FROM campaign_minus_phrases WHERE campaign_id = $1 ORDER BY created_at DESC`, campaignID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var items []CampaignMinusPhrase
	for rows.Next() {
		var i CampaignMinusPhrase
		if err := rows.Scan(&i.ID, &i.CampaignID, &i.Phrase, &i.CreatedAt); err != nil {
			return nil, err
		}
		items = append(items, i)
	}
	return items, rows.Err()
}

func (q *Queries) DeleteMinusPhrase(ctx context.Context, id pgtype.UUID) error {
	_, err := q.db.Exec(ctx, `DELETE FROM campaign_minus_phrases WHERE id = $1`, id)
	return err
}

func (q *Queries) CreatePlusPhrase(ctx context.Context, campaignID pgtype.UUID, phrase string) (CampaignPlusPhrase, error) {
	row := q.db.QueryRow(ctx, `INSERT INTO campaign_plus_phrases (campaign_id, phrase) VALUES ($1,$2) ON CONFLICT (campaign_id, phrase) DO NOTHING RETURNING id, campaign_id, phrase, created_at`, campaignID, phrase)
	var i CampaignPlusPhrase
	err := row.Scan(&i.ID, &i.CampaignID, &i.Phrase, &i.CreatedAt)
	return i, err
}

func (q *Queries) ListPlusPhrases(ctx context.Context, campaignID pgtype.UUID) ([]CampaignPlusPhrase, error) {
	rows, err := q.db.Query(ctx, `SELECT id, campaign_id, phrase, created_at FROM campaign_plus_phrases WHERE campaign_id = $1 ORDER BY created_at DESC`, campaignID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var items []CampaignPlusPhrase
	for rows.Next() {
		var i CampaignPlusPhrase
		if err := rows.Scan(&i.ID, &i.CampaignID, &i.Phrase, &i.CreatedAt); err != nil {
			return nil, err
		}
		items = append(items, i)
	}
	return items, rows.Err()
}

func (q *Queries) DeletePlusPhrase(ctx context.Context, id pgtype.UUID) error {
	_, err := q.db.Exec(ctx, `DELETE FROM campaign_plus_phrases WHERE id = $1`, id)
	return err
}
