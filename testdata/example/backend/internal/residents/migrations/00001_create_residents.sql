-- +goose Up
CREATE TABLE residents (
    id           uuid PRIMARY KEY,
    tenant_id    uuid NOT NULL,
    unit_id      uuid NOT NULL,
    full_name    text NOT NULL CHECK (full_name <> ''),
    email        text NOT NULL CHECK (email = lower(email)),
    phone        text NOT NULL DEFAULT '',
    keycloak_sub uuid,
    create_time  timestamptz NOT NULL,
    update_time  timestamptz NOT NULL
);

CREATE UNIQUE INDEX residents_tenant_email_key ON residents (tenant_id, email);
CREATE UNIQUE INDEX residents_tenant_keycloak_sub_key ON residents (tenant_id, keycloak_sub)
    WHERE keycloak_sub IS NOT NULL;
-- Keyset pagination: list by tenant, optionally by unit, ordered by (create_time, id).
CREATE INDEX residents_tenant_order_idx ON residents (tenant_id, create_time, id);
CREATE INDEX residents_tenant_unit_order_idx ON residents (tenant_id, unit_id, create_time, id);

-- +goose Down
DROP TABLE residents;
