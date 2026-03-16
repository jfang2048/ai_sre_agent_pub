-- Database schema for FinOps / Cost Management

CREATE TABLE resource_usage_snapshots (
    id SERIAL PRIMARY KEY,
    instance_id VARCHAR(64) NOT NULL,
    snapshot_time TIMESTAMP NOT NULL,
    region VARCHAR(32) NOT NULL,
    instance_type VARCHAR(32) NOT NULL,
    avg_cpu DOUBLE PRECISION,
    max_cpu DOUBLE PRECISION,
    avg_mem DOUBLE PRECISION,
    max_mem DOUBLE PRECISION,
    network_io BIGINT, -- bytes
    cost_hourly DOUBLE PRECISION
);

CREATE INDEX idx_usage_instance_time ON resource_usage_snapshots(instance_id, snapshot_time);

CREATE TABLE recommendations (
    id SERIAL PRIMARY KEY,
    instance_id VARCHAR(64) NOT NULL,
    generated_at TIMESTAMP NOT NULL,
    action VARCHAR(32) NOT NULL, -- Terminate, Downsize
    suggested_type VARCHAR(32),
    current_cost_monthly DOUBLE PRECISION,
    projected_cost_monthly DOUBLE PRECISION,
    potential_savings_monthly DOUBLE PRECISION,
    confidence_score DOUBLE PRECISION,
    status VARCHAR(20) DEFAULT 'open', -- open, applied, dismissed
    reason TEXT
);

CREATE TABLE cost_budgets (
    id SERIAL PRIMARY KEY,
    team_id VARCHAR(64),
    service_id VARCHAR(64),
    monthly_limit DOUBLE PRECISION NOT NULL,
    alert_threshold_percent INTEGER DEFAULT 80, -- Alert at 80% usage
    created_at TIMESTAMP DEFAULT NOW()
);
