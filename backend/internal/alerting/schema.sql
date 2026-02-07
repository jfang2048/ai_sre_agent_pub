-- Database schema for Intelligent Alert Management System

CREATE TABLE alerts (
    id VARCHAR(64) PRIMARY KEY,
    fingerprint VARCHAR(64) NOT NULL, -- Deduplication key
    status VARCHAR(20) NOT NULL, -- firing, resolved, suppressed
    severity VARCHAR(20) NOT NULL,
    starts_at TIMESTAMP NOT NULL,
    ends_at TIMESTAMP,
    labels JSONB NOT NULL, -- Tags (host, service, region)
    annotations JSONB, -- Description, Runbook URL
    generator_url VARCHAR(255),
    incident_id VARCHAR(64) -- Link to parent incident
);

CREATE INDEX idx_alerts_fingerprint ON alerts(fingerprint);
CREATE INDEX idx_alerts_starts_at ON alerts(starts_at);
CREATE INDEX idx_alerts_status ON alerts(status);

CREATE TABLE incidents (
    id VARCHAR(64) PRIMARY KEY,
    title VARCHAR(255) NOT NULL,
    description TEXT,
    severity VARCHAR(20) NOT NULL,
    state VARCHAR(20) NOT NULL,
    created_at TIMESTAMP NOT NULL,
    updated_at TIMESTAMP NOT NULL,
    resolved_at TIMESTAMP,
    alert_count INT DEFAULT 0
);

CREATE TABLE notification_policies (
    id VARCHAR(64) PRIMARY KEY,
    route_key VARCHAR(64) NOT NULL, -- e.g. "team-sre"
    matchers JSONB NOT NULL, -- e.g. {"service": "db", "severity": "critical"}
    receiver_type VARCHAR(20) NOT NULL, -- slack, pagerduty
    receiver_config JSONB NOT NULL -- webhook_url, integration_key
);

CREATE TABLE silences (
    id VARCHAR(64) PRIMARY KEY,
    matchers JSONB NOT NULL,
    starts_at TIMESTAMP NOT NULL,
    ends_at TIMESTAMP NOT NULL,
    created_by VARCHAR(128) NOT NULL,
    comment TEXT
);
