-- A job is one action execution: what ran, who ran it, and what it said.
CREATE TABLE jobs (
  id          TEXT PRIMARY KEY,
  action_id   TEXT NOT NULL,
  label       TEXT NOT NULL,
  kind        TEXT NOT NULL DEFAULT '',
  target      TEXT NOT NULL DEFAULT '',
  actor       TEXT NOT NULL DEFAULT '',
  status      TEXT NOT NULL,            -- running | succeeded | failed
  output      TEXT NOT NULL DEFAULT '',
  error       TEXT NOT NULL DEFAULT '',
  started_at  TEXT NOT NULL DEFAULT (datetime('now')),
  finished_at TEXT
);
CREATE INDEX idx_jobs_started ON jobs(started_at DESC);
