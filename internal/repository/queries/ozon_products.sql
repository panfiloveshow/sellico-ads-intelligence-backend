-- Ozon product catalog mapping (product_id ↔ sales SKU + name/offer_id/image).

-- name: UpsertOzonProduct :exec
INSERT INTO ozon_products (seller_cabinet_id, product_id, sku, offer_id, name, primary_image)
VALUES (
    sqlc.arg('seller_cabinet_id'), sqlc.arg('product_id'), sqlc.narg('sku'),
    sqlc.narg('offer_id'), sqlc.narg('name'), sqlc.narg('primary_image')
)
ON CONFLICT (seller_cabinet_id, product_id) DO UPDATE SET
    sku = COALESCE(EXCLUDED.sku, ozon_products.sku),
    offer_id = COALESCE(EXCLUDED.offer_id, ozon_products.offer_id),
    name = COALESCE(EXCLUDED.name, ozon_products.name),
    primary_image = COALESCE(EXCLUDED.primary_image, ozon_products.primary_image),
    updated_at = now();

-- ListOzonProductsByCabinet feeds the price-sync name fill (map by product_id).
-- name: ListOzonProductsByCabinet :many
SELECT product_id, sku, offer_id, name, primary_image FROM ozon_products
WHERE seller_cabinet_id = $1;

-- ListOzonProductsBySkus feeds batched read enrichment (campaign detail, CPO,
-- bid changes) — one query per page, no N+1.
-- name: ListOzonProductsBySkus :many
SELECT sku, offer_id, name, primary_image FROM ozon_products
WHERE seller_cabinet_id = sqlc.arg('seller_cabinet_id')
  AND sku = ANY(sqlc.arg('skus')::bigint[]);
