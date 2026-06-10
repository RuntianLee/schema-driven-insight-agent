CREATE TABLE IF NOT EXISTS trajectories (
    trajectory_id    TEXT    PRIMARY KEY,
    created_at       INTEGER NOT NULL,
    agent_version    TEXT    NOT NULL,
    input_question   TEXT    NOT NULL,
    final_output     TEXT,
    outcome          TEXT,
    total_tokens     INTEGER,
    total_cost_usd   REAL,
    total_latency_ms INTEGER,
    step_count       INTEGER,
    error_summary    TEXT,
    metadata         TEXT,
    task_class       TEXT                        -- production / benchmark（旧行 NULL=unknown）
);
CREATE INDEX IF NOT EXISTS idx_traj_created ON trajectories(created_at);
CREATE INDEX IF NOT EXISTS idx_traj_outcome ON trajectories(outcome);
CREATE INDEX IF NOT EXISTS idx_traj_version ON trajectories(agent_version);

CREATE TABLE IF NOT EXISTS trajectory_steps (
    step_id          TEXT    PRIMARY KEY,
    trajectory_id    TEXT    NOT NULL,
    step_index       INTEGER NOT NULL,
    step_type        TEXT    NOT NULL,
    started_at       INTEGER NOT NULL,
    ended_at         INTEGER NOT NULL,
    latency_ms       INTEGER,
    input            TEXT,
    output           TEXT,
    tokens_input     INTEGER,
    tokens_output    INTEGER,
    cost_usd         REAL,
    model_name       TEXT,
    tool_name        TEXT,
    error            TEXT,
    metadata         TEXT,
    FOREIGN KEY (trajectory_id) REFERENCES trajectories(trajectory_id)
);
CREATE INDEX IF NOT EXISTS idx_steps_traj  ON trajectory_steps(trajectory_id, step_index);
CREATE INDEX IF NOT EXISTS idx_steps_type  ON trajectory_steps(step_type);
CREATE INDEX IF NOT EXISTS idx_steps_tool  ON trajectory_steps(tool_name);
CREATE INDEX IF NOT EXISTS idx_steps_error ON trajectory_steps(trajectory_id) WHERE error IS NOT NULL;

CREATE TABLE IF NOT EXISTS eval_results (
    result_id      TEXT    PRIMARY KEY,
    trajectory_id  TEXT    NOT NULL,
    task_id        TEXT    NOT NULL,
    evaluator_name TEXT    NOT NULL,
    value          REAL,
    pass           INTEGER NOT NULL,
    display        TEXT,
    created_at     INTEGER NOT NULL,
    FOREIGN KEY (trajectory_id) REFERENCES trajectories(trajectory_id)
);
CREATE INDEX IF NOT EXISTS idx_eval_traj ON eval_results(trajectory_id);
CREATE INDEX IF NOT EXISTS idx_eval_pass ON eval_results(evaluator_name, pass);

CREATE TABLE IF NOT EXISTS _meta (
    key   TEXT PRIMARY KEY,
    value TEXT NOT NULL
);
