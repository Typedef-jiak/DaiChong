-- Cici procurement is independent of customer checkout/payment records.
CREATE TABLE cici_purchases (
    id text PRIMARY KEY,
    account_hash text NOT NULL,
    product_id bigint NOT NULL CHECK(product_id > 0),
    sku_id bigint NOT NULL CHECK(sku_id > 0),
    quantity integer NOT NULL CHECK(quantity BETWEEN 1 AND 10000),
    expected_amount text NOT NULL,
    title text NOT NULL,
    request_body text NOT NULL,
    state text NOT NULL DEFAULT 'draft',
    supplier_id bigint,
    supplier_number text NOT NULL DEFAULT '',
    supplier_status text NOT NULL DEFAULT '',
    error_code text NOT NULL DEFAULT '',
    result jsonb,
    lease_token text NOT NULL DEFAULT '',
    lease_until bigint NOT NULL DEFAULT 0,
    next_check_at bigint NOT NULL DEFAULT 0,
    created_by text NOT NULL,
    created_at bigint NOT NULL,
    updated_at bigint NOT NULL,
    UNIQUE(account_hash,supplier_id)
);
CREATE INDEX cici_purchase_queue ON cici_purchases(next_check_at) WHERE supplier_id IS NOT NULL;
CREATE TABLE cici_purchase_events (
    id text PRIMARY KEY,
    purchase_id text NOT NULL REFERENCES cici_purchases(id),
    actor text NOT NULL,
    action text NOT NULL,
    state text NOT NULL,
    error_code text NOT NULL DEFAULT '',
    created_at bigint NOT NULL
);
