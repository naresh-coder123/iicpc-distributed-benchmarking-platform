CREATE EXTENSION IF NOT EXISTS timescaledb;

CREATE TABLE IF NOT EXISTS metric_records (
  time            TIMESTAMPTZ NOT NULL,
  test_run_id     TEXT NOT NULL,
  contestant_id   TEXT NOT NULL,
  client_id       TEXT NOT NULL,
  order_id        TEXT NOT NULL,
  sent_time_ns    BIGINT NOT NULL,
  recv_time_ns    BIGINT NOT NULL,
  engine_time_ns  BIGINT NOT NULL,
  is_correct      BOOLEAN NOT NULL,
  error_code      TEXT NOT NULL,
  latency_ns      BIGINT NOT NULL
);

SELECT create_hypertable('metric_records', by_range('time'), if_not_exists => TRUE);

CREATE INDEX IF NOT EXISTS metric_records_contestant_time_idx
  ON metric_records (contestant_id, time DESC);

