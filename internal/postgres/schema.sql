-- Idempotent schema, applied at startup.

CREATE TABLE IF NOT EXISTS articles (
    id         BIGSERIAL PRIMARY KEY,
    title      TEXT NOT NULL,
    body       TEXT NOT NULL,
    category   TEXT NOT NULL,
    audience   TEXT NOT NULL CHECK (audience IN ('all', 'admin')),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    -- Title words weigh more than body words in ranking.
    search     TSVECTOR GENERATED ALWAYS AS (
        setweight(to_tsvector('english', title), 'A') || setweight(to_tsvector('english', body), 'B')
    ) STORED
);
CREATE INDEX IF NOT EXISTS articles_search_idx ON articles USING GIN (search);

CREATE TABLE IF NOT EXISTS tickets (
    id         BIGSERIAL PRIMARY KEY,
    subject    TEXT NOT NULL CHECK (length(subject) <= 200),
    body       TEXT NOT NULL,
    status     TEXT NOT NULL CHECK (status IN ('open', 'pending', 'escalated', 'closed')),
    priority   TEXT NOT NULL CHECK (priority IN ('low', 'normal', 'high', 'urgent')),
    requester  TEXT NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS tickets_status_idx ON tickets (status, id DESC);

CREATE TABLE IF NOT EXISTS comments (
    id         BIGSERIAL PRIMARY KEY,
    ticket_id  BIGINT NOT NULL REFERENCES tickets (id),
    author     TEXT NOT NULL,
    body       TEXT NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS comments_ticket_idx ON comments (ticket_id, id);

CREATE TABLE IF NOT EXISTS audit_log (
    id          BIGSERIAL PRIMARY KEY,
    at          TIMESTAMPTZ NOT NULL,
    principal   TEXT NOT NULL,
    role        TEXT NOT NULL,
    tool        TEXT NOT NULL,
    args        TEXT NOT NULL,
    outcome     TEXT NOT NULL,
    error       TEXT NOT NULL DEFAULT '',
    duration_ms BIGINT NOT NULL
);

CREATE TABLE IF NOT EXISTS api_keys (
    key_hash   TEXT PRIMARY KEY,
    name       TEXT NOT NULL,
    role       TEXT NOT NULL CHECK (role IN ('viewer', 'agent', 'admin')),
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
