package sqlcgen

import (
	"context"

	"github.com/jackc/pgx/v5/pgtype"
)

type WBAPIRateLimit struct {
	SellerCabinetID   pgtype.UUID
	EndpointKey       string
	NextAllowedAt     pgtype.Timestamptz
	RetryAfterSeconds int32
	LastStatus        int32
	LastError         pgtype.Text
	UpdatedAt         pgtype.Timestamptz
}

type UpsertWBAPIRateLimitParams struct {
	SellerCabinetID   pgtype.UUID
	EndpointKey       string
	NextAllowedAt     pgtype.Timestamptz
	RetryAfterSeconds int32
	LastStatus        int32
	LastError         pgtype.Text
}

func (q *Queries) UpsertWBAPIRateLimit(ctx context.Context, arg UpsertWBAPIRateLimitParams) error {
	_, err := q.db.Exec(ctx, `
INSERT INTO wb_api_rate_limits (
  seller_cabinet_id, endpoint_key, next_allowed_at, retry_after_seconds, last_status, last_error
) VALUES ($1,$2,$3,$4,$5,$6)
ON CONFLICT (seller_cabinet_id, endpoint_key) DO UPDATE SET
  next_allowed_at = EXCLUDED.next_allowed_at,
  retry_after_seconds = EXCLUDED.retry_after_seconds,
  last_status = EXCLUDED.last_status,
  last_error = EXCLUDED.last_error,
  updated_at = now()`,
		arg.SellerCabinetID, arg.EndpointKey, arg.NextAllowedAt, arg.RetryAfterSeconds, arg.LastStatus, arg.LastError)
	return err
}

func (q *Queries) GetWBAPIRateLimit(ctx context.Context, sellerCabinetID pgtype.UUID, endpointKey string) (WBAPIRateLimit, error) {
	row := q.db.QueryRow(ctx, `
SELECT seller_cabinet_id, endpoint_key, next_allowed_at, retry_after_seconds, last_status, last_error, updated_at
FROM wb_api_rate_limits
WHERE seller_cabinet_id = $1 AND endpoint_key = $2`,
		sellerCabinetID, endpointKey)
	var item WBAPIRateLimit
	err := row.Scan(
		&item.SellerCabinetID,
		&item.EndpointKey,
		&item.NextAllowedAt,
		&item.RetryAfterSeconds,
		&item.LastStatus,
		&item.LastError,
		&item.UpdatedAt,
	)
	return item, err
}

func (q *Queries) ListActiveWBAPIRateLimitsByCabinet(ctx context.Context, sellerCabinetID pgtype.UUID, now pgtype.Timestamptz) ([]WBAPIRateLimit, error) {
	rows, err := q.db.Query(ctx, `
SELECT seller_cabinet_id, endpoint_key, next_allowed_at, retry_after_seconds, last_status, last_error, updated_at
FROM wb_api_rate_limits
WHERE seller_cabinet_id = $1 AND next_allowed_at > $2
ORDER BY next_allowed_at ASC`,
		sellerCabinetID, now)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	items := []WBAPIRateLimit{}
	for rows.Next() {
		var item WBAPIRateLimit
		if err := rows.Scan(
			&item.SellerCabinetID,
			&item.EndpointKey,
			&item.NextAllowedAt,
			&item.RetryAfterSeconds,
			&item.LastStatus,
			&item.LastError,
			&item.UpdatedAt,
		); err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}
