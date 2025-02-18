CREATE TABLE IF NOT EXISTS deployments
(
    uuid                 TEXT PRIMARY KEY,
    github_url           TEXT,
    branch               TEXT,
    deployment_url       TEXT,
    deployment_proxy_url TEXT,
    status               TEXT,
    error                TEXT,
    created_at           DATETIME DEFAULT CURRENT_TIMESTAMP,
    updated_at           DATETIME DEFAULT CURRENT_TIMESTAMP,
    deleted_at           DATETIME
);

CREATE TRIGGER update_deployments_updated_at
    AFTER UPDATE
    ON deployments
    FOR EACH ROW
BEGIN
    UPDATE deployments
    SET updated_at = CURRENT_TIMESTAMP
    WHERE uuid = NEW.uuid;
END;