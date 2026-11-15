-- 0019_source_channel.sql
--
-- A source is the content owner, a channel is one web entry of that source.
-- Both are configuration that the operator can edit and pause. The tables do
-- not replace the plan: a plan still names (source_code, channel), which is the
-- pair the running worker already uses, so existing plans keep pointing at a
-- real channel through the rows seeded below.
--
-- Copying the plan columns into a channel would create a second source of
-- truth. The channel owns what belongs to the entry - its adapter, its URL and
-- host allowlist, the request rate and the timeout - while the plan keeps its
-- pages, budgets and schedule. A plan may override the rate; an empty override
-- means the channel value applies and the run snapshot records what was used.
--
-- Nothing secret is stored here: secret_ref only names a server-side private
-- configuration, and the console shows whether it exists, never its value.

CREATE TABLE IF NOT EXISTS source (
  id BIGINT NOT NULL AUTO_INCREMENT,
  code VARCHAR(32) NOT NULL,
  name VARCHAR(80) NOT NULL,
  -- enabled or paused. Pausing stops new collection only; stored candidates and
  -- published work are untouched.
  status VARCHAR(16) NOT NULL DEFAULT 'enabled',
  version INT NOT NULL DEFAULT 1,
  created_at DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
  updated_at DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6) ON UPDATE CURRENT_TIMESTAMP(6),
  PRIMARY KEY (id),
  UNIQUE KEY uk_source_code (code)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

CREATE TABLE IF NOT EXISTS source_channel (
  id BIGINT NOT NULL AUTO_INCREMENT,
  source_id BIGINT NOT NULL,
  code VARCHAR(32) NOT NULL,
  name VARCHAR(80) NOT NULL,
  -- adapter_key names a metadata adapter the server registered; entry_kind and
  -- entry_code must be declared by that adapter.
  adapter_key VARCHAR(32) NOT NULL,
  entry_kind VARCHAR(32) NOT NULL DEFAULT 'rank',
  entry_code VARCHAR(32) NOT NULL DEFAULT 'rank',
  -- base_url is an HTTPS origin without path, credentials or query.
  base_url VARCHAR(255) NOT NULL,
  -- allowed_hosts is a comma separated list of bare hosts; a redirect or origin
  -- outside it is refused.
  allowed_hosts VARCHAR(512) NOT NULL,
  rate_per_minute INT NOT NULL DEFAULT 30,
  timeout_seconds INT NOT NULL DEFAULT 20,
  -- secret_ref names a private server configuration; it is never a secret.
  secret_ref VARCHAR(128) NOT NULL DEFAULT '',
  status VARCHAR(16) NOT NULL DEFAULT 'enabled',
  version INT NOT NULL DEFAULT 1,
  created_at DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
  updated_at DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6) ON UPDATE CURRENT_TIMESTAMP(6),
  PRIMARY KEY (id),
  UNIQUE KEY uk_channel (source_id, code),
  KEY idx_channel_status (status)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

-- The one entry the deployed adapter really supports (the slug it builds is
-- "hot-drama"). Seeded so plans can name a channel that really exists.
INSERT INTO source (code, name, status, version)
SELECT 'hongguo', '红果短剧', 'enabled', 1
WHERE NOT EXISTS (SELECT 1 FROM source WHERE code = 'hongguo');

INSERT INTO source_channel
  (source_id, code, name, adapter_key, entry_kind, entry_code, base_url, allowed_hosts,
   rate_per_minute, timeout_seconds, status, version)
SELECT s.id, 'hot-drama', '热播榜', 'hongguo_web', 'rank', 'hot-drama',
       'https://hongguoduanju.com', 'hongguoduanju.com,www.hongguoduanju.com', 30, 20, 'enabled', 1
FROM source s
WHERE s.code = 'hongguo'
  AND NOT EXISTS (
    SELECT 1 FROM source_channel c WHERE c.source_id = s.id AND c.code = 'hot-drama'
  );
