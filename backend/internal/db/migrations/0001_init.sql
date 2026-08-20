CREATE EXTENSION IF NOT EXISTS pgcrypto;

CREATE TABLE users (
    id            UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    email         TEXT UNIQUE NOT NULL,
    password_hash TEXT NOT NULL,
    created_at    TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE auth_tokens (
    id           UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id      UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    token_hash   TEXT NOT NULL UNIQUE,
    device_name  TEXT NOT NULL DEFAULT '',
    created_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    last_used_at TIMESTAMPTZ
);

CREATE TABLE apps (
    id         UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id    UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    local_id   TEXT NOT NULL,
    name       TEXT NOT NULL,
    command    TEXT NOT NULL,
    cwd        TEXT NOT NULL DEFAULT '',
    autostart  BOOLEAN NOT NULL DEFAULT false,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (user_id, local_id)
);

CREATE TABLE projects (
    id                UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id           UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    local_id          TEXT NOT NULL,
    name              TEXT NOT NULL,
    port              INTEGER NOT NULL,
    subdomain         TEXT NOT NULL DEFAULT '',
    base_domain       TEXT NOT NULL DEFAULT '',
    protocol          TEXT NOT NULL DEFAULT 'http',
    tunnel_id         TEXT NOT NULL DEFAULT '',
    tunnel_name       TEXT NOT NULL DEFAULT '',
    account_tag       TEXT NOT NULL DEFAULT '',
    tunnel_secret_enc BYTEA,
    autostart         BOOLEAN NOT NULL DEFAULT false,
    updated_at        TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (user_id, local_id)
);

CREATE TABLE domain_certs (
    id           UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id      UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    domain       TEXT NOT NULL,
    cert_pem_enc BYTEA NOT NULL,
    updated_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (user_id, domain)
);
