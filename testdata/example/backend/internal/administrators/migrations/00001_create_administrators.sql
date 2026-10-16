-- +goose Up
CREATE TABLE administrators (
    id           uuid PRIMARY KEY,
    tenant_id    uuid NOT NULL,
    company      text NOT NULL CHECK (company <> ''),
    email        text NOT NULL CHECK (email = lower(email)),
    phone        text NOT NULL DEFAULT '',
    keycloak_sub uuid,
    create_time  timestamptz NOT NULL,
    update_time  timestamptz NOT NULL
);

CREATE UNIQUE INDEX administrators_tenant_email_key ON administrators (tenant_id, email);
CREATE UNIQUE INDEX administrators_tenant_keycloak_sub_key ON administrators (tenant_id, keycloak_sub)
    WHERE keycloak_sub IS NOT NULL;
-- Keyset pagination: list by tenant ordered by (create_time, id).
CREATE INDEX administrators_tenant_order_idx ON administrators (tenant_id, create_time, id);

-- +goose Down
DROP TABLE administrators;
