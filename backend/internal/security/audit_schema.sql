-- Database schema for Audit Logging

CREATE TABLE audit_logs (
    id VARCHAR(64) PRIMARY KEY,
    timestamp TIMESTAMP NOT NULL,
    actor VARCHAR(128) NOT NULL, -- User ID or Service Account
    role VARCHAR(32) NOT NULL,
    action VARCHAR(64) NOT NULL, -- GET, POST, DELETE, "login"
    resource VARCHAR(255) NOT NULL, -- URI or Object ID
    status VARCHAR(20) NOT NULL, -- allowed, forbidden, success, failure
    ip_address VARCHAR(45),
    metadata JSONB, -- PII masked details
    user_agent VARCHAR(255)
);

-- Indexes for compliance reporting
CREATE INDEX idx_audit_timestamp ON audit_logs(timestamp);
CREATE INDEX idx_audit_actor ON audit_logs(actor);
CREATE INDEX idx_audit_resource ON audit_logs(resource);

-- Table for key rotation tracking (Compliance)
CREATE TABLE key_rotation_log (
    id VARCHAR(64) PRIMARY KEY,
    key_id VARCHAR(64) NOT NULL,
    rotated_at TIMESTAMP NOT NULL,
    rotated_by VARCHAR(128) NOT NULL,
    previous_key_hash VARCHAR(64) NOT NULL
);
