-- A series is one measurable thing: host CPU, a container's memory, a disk's
-- used bytes. Samples reference it by integer id to keep the rows narrow.
CREATE TABLE series (
  id      INTEGER PRIMARY KEY AUTOINCREMENT,
  kind    TEXT NOT NULL,             -- 'host' | 'container' | 'disk'
  subject TEXT NOT NULL DEFAULT '',  -- '' for host, otherwise container or mount
  metric  TEXT NOT NULL,             -- 'cpu' | 'mem' | 'net_rx' | ...
  UNIQUE (kind, subject, metric)
);

-- Three resolutions, coarsening with age. Timestamps are unix seconds aligned
-- to the tier's bucket, so a rollup is idempotent and can safely re-run.
CREATE TABLE samples_raw (
  series_id INTEGER NOT NULL REFERENCES series(id) ON DELETE CASCADE,
  ts        INTEGER NOT NULL,
  value     REAL    NOT NULL,
  PRIMARY KEY (series_id, ts)
) WITHOUT ROWID;

CREATE TABLE samples_5m (
  series_id INTEGER NOT NULL REFERENCES series(id) ON DELETE CASCADE,
  ts        INTEGER NOT NULL,
  value     REAL    NOT NULL,
  min_value REAL    NOT NULL,
  max_value REAL    NOT NULL,
  PRIMARY KEY (series_id, ts)
) WITHOUT ROWID;

CREATE TABLE samples_1h (
  series_id INTEGER NOT NULL REFERENCES series(id) ON DELETE CASCADE,
  ts        INTEGER NOT NULL,
  value     REAL    NOT NULL,
  min_value REAL    NOT NULL,
  max_value REAL    NOT NULL,
  PRIMARY KEY (series_id, ts)
) WITHOUT ROWID;

-- Records how far each rollup has run, so a restart does not rescan history.
CREATE TABLE rollup_state (
  tier       TEXT PRIMARY KEY,
  through_ts INTEGER NOT NULL DEFAULT 0
);
