-- Schema version 1. C4 section 7 tables and lookup index, plus the
-- 1 minute rollup table and the action log "recent" index.

CREATE TABLE metric_sample (
    id            INTEGER PRIMARY KEY,
    profile       TEXT    NOT NULL,
    taken_at      INTEGER NOT NULL,   -- unix seconds
    scope         TEXT    NOT NULL,   -- 'vm' or a container id
    cpu_percent   REAL,
    memory_bytes  INTEGER,
    disk_bytes    INTEGER
);
CREATE INDEX metric_sample_lookup ON metric_sample (profile, scope, taken_at);

CREATE TABLE metric_sample_1m (
    profile       TEXT    NOT NULL,
    scope         TEXT    NOT NULL,
    bucket_start  INTEGER NOT NULL,   -- unix seconds, multiple of 60
    cpu_percent   REAL,
    memory_bytes  INTEGER,
    disk_bytes    INTEGER,
    samples       INTEGER NOT NULL,   -- raw samples folded into the bucket
    cpu_samples   INTEGER NOT NULL,   -- how many of them had a cpu value
    mem_samples   INTEGER NOT NULL,   -- ... a memory value
    disk_samples  INTEGER NOT NULL,   -- ... a disk value
    PRIMARY KEY (profile, scope, bucket_start)
) WITHOUT ROWID;

CREATE TABLE action_log (
    id          INTEGER PRIMARY KEY,
    profile     TEXT    NOT NULL,
    started_at  INTEGER NOT NULL,
    action      TEXT    NOT NULL,     -- 'start', 'stop', 'config_change', ...
    outcome     TEXT    NOT NULL,     -- 'success', 'failed', 'cancelled'
    detail      TEXT
);
CREATE INDEX action_log_recent ON action_log (profile, started_at DESC, id DESC);
