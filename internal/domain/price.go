package domain

import (
	"time"

	"github.com/google/uuid"
)

// Price change sources.
const (
	PriceSourceStrategy = "strategy"
	PriceSourceManual   = "manual"
	PriceSourceRollback = "rollback"
	PriceSourceSchedule = "schedule"
)

// Price change WB statuses.
const (
	PriceStatusRecommended = "recommended"
	PriceStatusPending     = "pending"
	PriceStatusUploaded    = "uploaded"
	PriceStatusApplied     = "applied"
	PriceStatusFailed      = "failed"
	PriceStatusRolledBack  = "rolled_back"
)

// Price upload task statuses.
const (
	PriceTaskUploaded   = "uploaded"
	PriceTaskProcessing = "processing"
	PriceTaskApplied    = "applied"
	PriceTaskPartial    = "partial"
	PriceTaskFailed     = "failed"
)

// Cabinet prices-scope statuses.
const (
	PricesScopeUnknown = "unknown"
	PricesScopeOK      = "ok"
	PricesScopeMissing = "missing"
)

// Schedule entry statuses / scope / adjustment.
const (
	PriceSchedulePlanned    = "planned"
	PriceScheduleExecuting  = "executing"
	PriceScheduleDone       = "done"
	PriceScheduleFailed     = "failed"
	PriceScheduleCanceled   = "canceled"
	PriceScopeProduct       = "product"
	PriceScopeList          = "list"
	PriceScopeAll           = "all"
	PriceAdjustTargetRub    = "target_rub"
	PriceAdjustDeltaPercent = "delta_percent"
	PriceDirectionUp        = "up"
	PriceDirectionDown      = "down"
)

// ProductPrice is a product's current WB price/discount (all *Rub = integer rubles).
type ProductPrice struct {
	ID                  uuid.UUID `json:"id"`
	WorkspaceID         uuid.UUID `json:"workspace_id"`
	SellerCabinetID     uuid.UUID `json:"seller_cabinet_id"`
	WBProductID         int64     `json:"wb_product_id"`
	PriceRub            int64     `json:"price_rub"`
	DiscountPercent     int       `json:"discount_percent"`
	ClubDiscountPercent int       `json:"club_discount_percent"`
	DiscountedPriceRub  *int64    `json:"discounted_price_rub,omitempty"`
	EditableSizePrice   bool      `json:"editable_size_price"`
	SyncedAt            time.Time `json:"synced_at"`
	UpdatedAt           time.Time `json:"updated_at"`
}

// EffectivePriceRub returns the discounted price if known, else base price.
func (p ProductPrice) EffectivePriceRub() int64 {
	if p.DiscountedPriceRub != nil {
		return *p.DiscountedPriceRub
	}
	return p.PriceRub
}

// PriceChange records a single recommended/applied/rolled-back price change.
type PriceChange struct {
	ID                 uuid.UUID                   `json:"id"`
	WorkspaceID        uuid.UUID                   `json:"workspace_id"`
	SellerCabinetID    uuid.UUID                   `json:"seller_cabinet_id"`
	StrategyID         *uuid.UUID                  `json:"strategy_id,omitempty"`
	ScheduleEntryID    *uuid.UUID                  `json:"schedule_entry_id,omitempty"`
	UploadTaskID       *uuid.UUID                  `json:"upload_task_id,omitempty"`
	WBProductID        int64                       `json:"wb_product_id"`
	OldPriceRub        int64                       `json:"old_price_rub"`
	NewPriceRub        int64                       `json:"new_price_rub"`
	OldDiscountPercent int                         `json:"old_discount_percent"`
	NewDiscountPercent int                         `json:"new_discount_percent"`
	MinPriceRub        *int64                      `json:"min_price_rub,omitempty"`
	Reason             string                      `json:"reason"`
	Source             string                      `json:"source"`
	WBStatus           string                      `json:"wb_status"`
	Error              *string                     `json:"error,omitempty"`
	CanRollback        bool                        `json:"can_rollback"`
	RollbackOf         *uuid.UUID                  `json:"rollback_of,omitempty"`
	DecisionContext    *PriceChangeDecisionContext `json:"decision_context,omitempty"`
	CreatedBy          *uuid.UUID                  `json:"created_by,omitempty"`
	CreatedAt          time.Time                   `json:"created_at"`
	UpdatedAt          time.Time                   `json:"updated_at"`
}

// PriceChangeDecisionContext captures why a price decision was made (audit).
type PriceChangeDecisionContext struct {
	ActorType       string   `json:"actor_type"`
	StrategyType    string   `json:"strategy_type,omitempty"`
	Direction       string   `json:"direction,omitempty"`
	Reason          string   `json:"reason,omitempty"`
	MinPriceRub     *int64   `json:"min_price_rub,omitempty"`
	TargetPriceRub  *int64   `json:"target_price_rub,omitempty"`
	ExternalChange  bool     `json:"external_change,omitempty"`
	SkipReason      string   `json:"skip_reason,omitempty"`
	MissingEvidence []string `json:"missing_evidence,omitempty"`
}

// PriceUploadTask tracks an async WB price-upload task.
type PriceUploadTask struct {
	ID              uuid.UUID  `json:"id"`
	WorkspaceID     uuid.UUID  `json:"workspace_id"`
	SellerCabinetID uuid.UUID  `json:"seller_cabinet_id"`
	WBTaskID        int64      `json:"wb_task_id"`
	Status          string     `json:"status"`
	ItemsCount      int        `json:"items_count"`
	PollCount       int        `json:"poll_count"`
	LastPolledAt    *time.Time `json:"last_polled_at,omitempty"`
	CompletedAt     *time.Time `json:"completed_at,omitempty"`
	Error           *string    `json:"error,omitempty"`
	CreatedAt       time.Time  `json:"created_at"`
	UpdatedAt       time.Time  `json:"updated_at"`
}

// PriceQuarantineGood is a product held in WB price quarantine.
type PriceQuarantineGood struct {
	ID              uuid.UUID  `json:"id"`
	WorkspaceID     uuid.UUID  `json:"workspace_id"`
	SellerCabinetID uuid.UUID  `json:"seller_cabinet_id"`
	WBProductID     int64      `json:"wb_product_id"`
	OldPriceRub     *int64     `json:"old_price_rub,omitempty"`
	NewPriceRub     *int64     `json:"new_price_rub,omitempty"`
	DetectedAt      time.Time  `json:"detected_at"`
	ResolvedAt      *time.Time `json:"resolved_at,omitempty"`
	Notified        bool       `json:"notified"`
}

// PriceScheduleEntry is a planned (calendar) price change.
type PriceScheduleEntry struct {
	ID               uuid.UUID   `json:"id"`
	WorkspaceID      uuid.UUID   `json:"workspace_id"`
	SellerCabinetID  uuid.UUID   `json:"seller_cabinet_id"`
	ScopeType        string      `json:"scope_type"`
	ProductIDs       []int64     `json:"product_ids,omitempty"`
	AdjustmentType   string      `json:"adjustment_type"`
	AdjustmentValue  float64     `json:"adjustment_value"`
	Direction        string      `json:"direction,omitempty"`
	ScheduledAt      time.Time   `json:"scheduled_at"`
	RevertAt         *time.Time  `json:"revert_at,omitempty"`
	RevertToPrevious bool        `json:"revert_to_previous"`
	RevertOf         *uuid.UUID  `json:"revert_of,omitempty"`
	Status           string      `json:"status"`
	ExecutedTaskIDs  []uuid.UUID `json:"executed_task_ids,omitempty"`
	Error            *string     `json:"error,omitempty"`
	Comment          *string     `json:"comment,omitempty"`
	CreatedBy        *uuid.UUID  `json:"created_by,omitempty"`
	CreatedAt        time.Time   `json:"created_at"`
	UpdatedAt        time.Time   `json:"updated_at"`
}
