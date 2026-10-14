CREATE EXTENSION IF NOT EXISTS vector;

CREATE TABLE sources (
  id text PRIMARY KEY,
  kind text NOT NULL,
  url text NOT NULL,
  lang text NOT NULL,
  section text NOT NULL,
  weight real NOT NULL DEFAULT 1.0,
  enabled boolean NOT NULL DEFAULT true,
  fail_count int NOT NULL DEFAULT 0,
  etag text,
  last_modified text,
  last_fetched_at timestamptz
);

CREATE TABLE clusters (
  id bigserial PRIMARY KEY,
  first_seen timestamptz NOT NULL DEFAULT now(),
  last_seen timestamptz NOT NULL DEFAULT now(),
  centroid halfvec(768),
  n_sources int NOT NULL DEFAULT 1,
  max_engagement int NOT NULL DEFAULT 0,
  section text,
  topics text[] NOT NULL DEFAULT '{}',
  hot_score real NOT NULL DEFAULT 0,
  hot_reason jsonb,
  last_sent_n_sources int
);

CREATE TABLE items (
  id bigserial PRIMARY KEY,
  source_id text NOT NULL REFERENCES sources(id),
  url_canonical text NOT NULL UNIQUE,
  title text NOT NULL,
  snippet text,
  lang text,
  published_at timestamptz,
  fetched_at timestamptz NOT NULL DEFAULT now(),
  engagement jsonb,
  title_hash text NOT NULL,
  state text NOT NULL DEFAULT 'new'
    CHECK (state IN ('new','triaged','embedded','clustered','dropped')),
  attempts int NOT NULL DEFAULT 0,
  triage jsonb,
  embedding halfvec(768),
  cluster_id bigint REFERENCES clusters(id)
);
CREATE INDEX items_state_idx ON items(state);
CREATE INDEX items_published_at_idx ON items(published_at);
CREATE INDEX items_title_hash_idx ON items(title_hash);
CREATE INDEX clusters_centroid_hnsw ON clusters USING hnsw (centroid halfvec_cosine_ops);

CREATE TABLE digests (
  id bigserial PRIMARY KEY,
  digest_date date NOT NULL UNIQUE,
  header_sent boolean NOT NULL DEFAULT false,
  created_at timestamptz NOT NULL DEFAULT now(),
  finished_at timestamptz
);

CREATE TABLE digest_items (
  digest_id bigint NOT NULL REFERENCES digests(id),
  cluster_id bigint NOT NULL REFERENCES clusters(id),
  rank int NOT NULL,
  state text NOT NULL DEFAULT 'selected'
    CHECK (state IN ('selected','summarized','sent')),
  summary jsonb,
  fallback boolean NOT NULL DEFAULT false,
  tg_message_id bigint,
  PRIMARY KEY (digest_id, cluster_id)
);

CREATE TABLE feedback (
  id bigserial PRIMARY KEY,
  cluster_id bigint NOT NULL REFERENCES clusters(id),
  kind text NOT NULL CHECK (kind IN ('up','down','save','click')),
  at timestamptz NOT NULL DEFAULT now()
);

-- day = ngày theo giờ Pacific (RPD của Google reset nửa đêm Pacific)
CREATE TABLE llm_usage (
  day date NOT NULL,
  model text NOT NULL,
  requests int NOT NULL DEFAULT 0,
  PRIMARY KEY (day, model)
);

CREATE TABLE profile (
  key text PRIMARY KEY,
  value jsonb NOT NULL
);
