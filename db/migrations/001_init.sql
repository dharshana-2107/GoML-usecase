-- schema for the flash sale.
-- postgres runs this file automatically the first time the container starts.

-- one row per product on sale. "sold" can never pass "total": the check
-- constraint is a last safety net that even buggy code can not get around.
CREATE TABLE IF NOT EXISTS inventory (
    sku        TEXT PRIMARY KEY,
    total      INTEGER NOT NULL CHECK (total >= 0),
    sold       INTEGER NOT NULL DEFAULT 0 CHECK (sold >= 0),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    CONSTRAINT sold_within_total CHECK (sold <= total)
);

-- one row per reservation the worker processed (confirmed or rejected).
CREATE TABLE IF NOT EXISTS orders (
    reservation_id  UUID PRIMARY KEY,           -- makes worker retries safe
    user_id         TEXT NOT NULL,
    sku             TEXT NOT NULL REFERENCES inventory (sku),
    idempotency_key UUID NOT NULL UNIQUE,       -- one order per client request
    status          TEXT NOT NULL CHECK (status IN ('confirmed', 'rejected')),
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- a user can hold at most one confirmed phone per sku
CREATE UNIQUE INDEX IF NOT EXISTS one_phone_per_user
    ON orders (user_id, sku) WHERE status = 'confirmed';

CREATE INDEX IF NOT EXISTS orders_sku_status ON orders (sku, status);

-- starting stock. the seed tool resets this before each sale.
INSERT INTO inventory (sku, total, sold) VALUES ('PHONE-1', 500, 0)
ON CONFLICT (sku) DO NOTHING;
