-- +goose Up
CREATE TABLE tenants (id text PRIMARY KEY, name text NOT NULL);
CREATE TABLE projects (id text PRIMARY KEY, tenant_id text NOT NULL REFERENCES tenants(id), name text NOT NULL);
CREATE TABLE devices (id text PRIMARY KEY, project_id text NOT NULL REFERENCES projects(id), created_at timestamptz NOT NULL DEFAULT now());
CREATE TABLE device_shadow (device_id text PRIMARY KEY REFERENCES devices(id), payload_json jsonb NOT NULL, updated_at timestamptz NOT NULL, version bigint NOT NULL DEFAULT 1);
CREATE TABLE device_heartbeats (device_id text PRIMARY KEY REFERENCES devices(id), observed_at timestamptz NOT NULL);
CREATE TABLE telemetry_samples (id bigserial PRIMARY KEY, device_id text NOT NULL REFERENCES devices(id), sampled_at timestamptz NOT NULL, payload_json jsonb NOT NULL);
CREATE INDEX telemetry_device_time ON telemetry_samples(device_id,sampled_at DESC);
CREATE TABLE alarms (message_id text PRIMARY KEY, device_id text NOT NULL REFERENCES devices(id), observed_at timestamptz NOT NULL, payload_json jsonb NOT NULL);
CREATE TABLE events (message_id text PRIMARY KEY, device_id text NOT NULL REFERENCES devices(id), observed_at timestamptz NOT NULL, payload_json jsonb NOT NULL);
CREATE TABLE users (id text PRIMARY KEY, subject text UNIQUE NOT NULL);
CREATE TABLE roles (name text PRIMARY KEY);
CREATE TABLE user_roles (user_id text REFERENCES users(id), role_name text REFERENCES roles(name), PRIMARY KEY(user_id,role_name));
CREATE TABLE live_sessions (session_id text PRIMARY KEY, device_id text NOT NULL REFERENCES devices(id), user_id text NOT NULL REFERENCES users(id), expires_at timestamptz NOT NULL);
CREATE TABLE remote_sessions (session_id text PRIMARY KEY, device_id text NOT NULL REFERENCES devices(id), user_id text NOT NULL REFERENCES users(id), expires_at timestamptz NOT NULL, idle_seconds integer NOT NULL, source_ip inet, audit_id text NOT NULL);
CREATE TABLE audit_logs (id bigserial PRIMARY KEY, user_id text NOT NULL, device_id text, action text NOT NULL, result text NOT NULL, observed_at timestamptz NOT NULL DEFAULT now());
INSERT INTO tenants VALUES ('demo','Anonymous Demo');
INSERT INTO projects VALUES ('Project-A','demo','Project-A');
INSERT INTO users VALUES ('dev-admin','development-only');
INSERT INTO roles VALUES ('platform_admin'),('operator'),('maintainer'),('viewer');
INSERT INTO user_roles VALUES ('dev-admin','platform_admin');
-- +goose Down
DROP TABLE user_roles, roles, audit_logs, remote_sessions, live_sessions, users, events, alarms, telemetry_samples, device_heartbeats, device_shadow, devices, projects, tenants;
