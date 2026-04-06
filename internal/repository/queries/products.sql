-- name: CreateProduct :one
INSERT INTO products (workspace_id, seller_cabinet_id, wb_product_id, title, brand, category, image_url, price)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
RETURNING *;

-- name: GetProductByID :one
SELECT * FROM products WHERE id = $1;

-- name: GetProductByWBProductID :one
SELECT * FROM products
WHERE workspace_id = $1 AND wb_product_id = $2
LIMIT 1;

-- name: UpsertProduct :one
INSERT INTO products (workspace_id, seller_cabinet_id, wb_product_id, title, brand, category, image_url, price)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
ON CONFLICT (wb_product_id, seller_cabinet_id) DO UPDATE SET
    title = EXCLUDED.title,
    brand = EXCLUDED.brand,
    category = EXCLUDED.category,
    image_url = EXCLUDED.image_url,
    price = EXCLUDED.price,
    updated_at = now()
RETURNING *;

-- name: ListProductsByWorkspace :many
SELECT * FROM products
WHERE workspace_id = $1
  AND (sqlc.narg('seller_cabinet_id_filter')::uuid IS NULL OR seller_cabinet_id = sqlc.narg('seller_cabinet_id_filter')::uuid)
  AND (sqlc.narg('title_filter')::text IS NULL OR title ILIKE '%' || sqlc.narg('title_filter')::text || '%')
ORDER BY created_at DESC
LIMIT $2 OFFSET $3;

-- name: ListProductsBySellerCabinet :many
SELECT * FROM products
WHERE seller_cabinet_id = $1
ORDER BY created_at DESC
LIMIT $2 OFFSET $3;
