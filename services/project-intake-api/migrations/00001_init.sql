CREATE EXTENSION IF NOT EXISTS pg_trgm WITH SCHEMA public;
CREATE TABLE teams (id uuid PRIMARY KEY DEFAULT gen_random_uuid(), name text NOT NULL);
CREATE TABLE app_users (subject text PRIMARY KEY, display_name text NOT NULL, role text NOT NULL CHECK(role IN ('baobei_sales','baobei_manager','baobei_director','baobei_admin')), team_id uuid REFERENCES teams, active boolean NOT NULL DEFAULT true);
CREATE TABLE app_sessions (token_hash text PRIMARY KEY, subject text NOT NULL REFERENCES app_users, csrf text NOT NULL, expires_at timestamptz NOT NULL);
CREATE TABLE organizations (id uuid PRIMARY KEY DEFAULT gen_random_uuid(), name text NOT NULL, normalized_name text NOT NULL UNIQUE, credit_code text UNIQUE);
CREATE TABLE organization_aliases (normalized_alias text PRIMARY KEY, organization_id uuid NOT NULL REFERENCES organizations);
CREATE TABLE contacts (id uuid PRIMARY KEY DEFAULT gen_random_uuid(), organization_id uuid REFERENCES organizations, display_name text NOT NULL);
CREATE TABLE registration_counters (year integer PRIMARY KEY, value integer NOT NULL CHECK (value BETWEEN 1 AND 999999));
CREATE FUNCTION next_registration_no() RETURNS text LANGUAGE plpgsql AS $$
DECLARE y integer := extract(year FROM now() AT TIME ZONE 'Asia/Shanghai'); n integer;
BEGIN
 INSERT INTO registration_counters(year,value) VALUES(y,1) ON CONFLICT(year) DO UPDATE SET value=registration_counters.value+1 RETURNING value INTO n;
 RETURN 'BB-' || y::text || '-' || lpad(n::text,6,'0');
END $$;
CREATE TABLE intake_projects (
 id uuid PRIMARY KEY DEFAULT gen_random_uuid(), registration_no text NOT NULL UNIQUE DEFAULT next_registration_no(),
 project_name text NOT NULL, normalized_project_name text NOT NULL,
 owner_subject text NOT NULL REFERENCES app_users, created_by_subject text NOT NULL REFERENCES app_users, team_id uuid REFERENCES teams,
 intake_status text NOT NULL DEFAULT 'DRAFT' CHECK (intake_status IN ('DRAFT','SUBMITTED','UNDER_REVIEW','APPROVED','RETURNED','REJECTED','EXPIRED','CANCELLED','CONVERTED')),
 sales_stage text NOT NULL DEFAULT 'L0_LEAD', protection_status text NOT NULL DEFAULT 'ACTIVE',
 information_source text NOT NULL DEFAULT '', confidence_level text NOT NULL DEFAULT '', project_type text NOT NULL DEFAULT '',
 estimated_amount numeric(18,2), expected_purchase_date date, last_activity_at timestamptz, next_followup_at timestamptz,
 version integer NOT NULL DEFAULT 1 CHECK(version>0), created_at timestamptz NOT NULL DEFAULT now(), updated_at timestamptz NOT NULL DEFAULT now(), document jsonb NOT NULL
);
CREATE INDEX intake_owner_idx ON intake_projects(owner_subject, updated_at DESC);
CREATE INDEX intake_team_idx ON intake_projects(team_id);
CREATE INDEX project_trgm_idx ON intake_projects USING gin(normalized_project_name gin_trgm_ops);
CREATE TABLE project_locations (project_id uuid PRIMARY KEY REFERENCES intake_projects, province text NOT NULL, city text NOT NULL, district text NOT NULL, industrial_park text NOT NULL, factory_name text NOT NULL, workshop text NOT NULL, address_detail text NOT NULL);
CREATE TABLE project_parties (id uuid PRIMARY KEY DEFAULT gen_random_uuid(), project_id uuid NOT NULL REFERENCES intake_projects, role text NOT NULL, organization_id uuid NOT NULL REFERENCES organizations);
CREATE INDEX parties_project_idx ON project_parties(project_id);
CREATE TABLE project_contacts (project_id uuid REFERENCES intake_projects, contact_id uuid REFERENCES contacts, PRIMARY KEY(project_id,contact_id));
CREATE TABLE planned_equipment (id uuid PRIMARY KEY DEFAULT gen_random_uuid(), project_id uuid NOT NULL REFERENCES intake_projects, data jsonb NOT NULL);
CREATE TABLE project_requirements (project_id uuid REFERENCES intake_projects, category text NOT NULL, description text NOT NULL DEFAULT '', PRIMARY KEY(project_id,category));
CREATE TABLE project_activities (id uuid PRIMARY KEY DEFAULT gen_random_uuid(), project_id uuid NOT NULL REFERENCES intake_projects, subject text NOT NULL REFERENCES app_users, type text NOT NULL, occurred_at timestamptz NOT NULL, summary text NOT NULL, result text NOT NULL, next_action text NOT NULL, next_followup_at timestamptz, created_at timestamptz NOT NULL DEFAULT now());
CREATE TABLE duplicate_cases (id uuid PRIMARY KEY DEFAULT gen_random_uuid(), project_id uuid NOT NULL REFERENCES intake_projects, candidate_id uuid NOT NULL REFERENCES intake_projects, score integer NOT NULL, risk text NOT NULL, created_at timestamptz NOT NULL DEFAULT now());
CREATE TABLE approvals (id uuid PRIMARY KEY DEFAULT gen_random_uuid(), project_id uuid REFERENCES intake_projects, subject text NOT NULL REFERENCES app_users, decision text NOT NULL, reason text NOT NULL, created_at timestamptz NOT NULL DEFAULT now());
CREATE TABLE ownership_history (id uuid PRIMARY KEY DEFAULT gen_random_uuid(), project_id uuid REFERENCES intake_projects, from_subject text, to_subject text NOT NULL, reason text NOT NULL, created_at timestamptz NOT NULL DEFAULT now());
CREATE TABLE conversion_jobs (id uuid PRIMARY KEY DEFAULT gen_random_uuid(), project_id uuid NOT NULL REFERENCES intake_projects, source_revision integer NOT NULL, payload_hash text NOT NULL, status text NOT NULL DEFAULT 'PREVIEW', UNIQUE(project_id,source_revision));
CREATE TABLE conversion_links (job_id uuid PRIMARY KEY REFERENCES conversion_jobs, target_system text NOT NULL, target_project_id text NOT NULL, UNIQUE(target_system,target_project_id));
CREATE TABLE audit_events (id uuid PRIMARY KEY DEFAULT gen_random_uuid(), project_id uuid REFERENCES intake_projects, subject text NOT NULL, action text NOT NULL, revision integer NOT NULL, created_at timestamptz NOT NULL DEFAULT now());
CREATE TABLE reference_values (key text PRIMARY KEY, value jsonb NOT NULL);
INSERT INTO reference_values VALUES ('duplicate_thresholds','{"warn":70,"high":90}');
