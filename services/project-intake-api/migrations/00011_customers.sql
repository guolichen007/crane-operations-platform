-- V1.1 R5 (B2): customers and contacts on the existing master data.
--
-- 00001-00010 are not modified. organizations stays the single customer
-- master (normalized_name stays globally unique); nothing is merged by name.
-- Everything descriptive is kept per team so that a globally unique name never
-- exposes another team's data:
--   * organization_team_profiles: one team's view of a customer (industry,
--     region, type, notes). A team sees a customer when it has a profile row
--     or a project it may read names the customer.
--   * contacts gain a maintaining team; a contact is visible to that team (and
--     directors/administrators) only. Legacy contacts without a team are
--     visible to directors/administrators only.
--   * customer_audit: immutable record of every customer/contact change.
CREATE TABLE organization_team_profiles (
 organization_id uuid NOT NULL REFERENCES organizations,
 team_id uuid NOT NULL REFERENCES teams,
 -- The team's own spelling of the customer name; another team's spelling of
 -- the shared master record is never shown.
 display_name text NOT NULL DEFAULT '' CHECK (length(display_name) <= 200),
 industry text NOT NULL DEFAULT '' CHECK (length(industry) <= 100),
 region text NOT NULL DEFAULT '' CHECK (length(region) <= 100),
 customer_type text NOT NULL DEFAULT '' CHECK (customer_type IN ('','END_USER','EQUIPMENT_MAKER','DESIGN_INSTITUTE','EPC','INTEGRATOR','AGENT','OTHER')),
 notes text NOT NULL DEFAULT '' CHECK (length(notes) <= 2000),
 version integer NOT NULL DEFAULT 1 CHECK (version > 0),
 created_by_subject text NOT NULL REFERENCES app_users,
 created_at timestamptz NOT NULL DEFAULT now(),
 updated_at timestamptz NOT NULL DEFAULT now(),
 PRIMARY KEY (organization_id, team_id)
);
CREATE INDEX organization_team_profiles_team ON organization_team_profiles(team_id);

ALTER TABLE contacts
 ADD COLUMN team_id uuid REFERENCES teams,
 ADD COLUMN title text NOT NULL DEFAULT '' CHECK (length(title) <= 100),
 ADD COLUMN department text NOT NULL DEFAULT '' CHECK (length(department) <= 100),
 ADD COLUMN phone text NOT NULL DEFAULT '' CHECK (length(phone) <= 32),
 ADD COLUMN notes text NOT NULL DEFAULT '' CHECK (length(notes) <= 1000),
 ADD COLUMN version integer NOT NULL DEFAULT 1 CHECK (version > 0),
 ADD COLUMN created_by_subject text REFERENCES app_users,
 ADD COLUMN created_at timestamptz NOT NULL DEFAULT now(),
 ADD COLUMN updated_at timestamptz NOT NULL DEFAULT now(),
 ADD COLUMN archived_at timestamptz;
CREATE INDEX contacts_org_team ON contacts(organization_id, team_id);
CREATE INDEX parties_org_idx ON project_parties(organization_id);

ALTER TABLE project_contacts
 ADD COLUMN linked_by_subject text REFERENCES app_users,
 ADD COLUMN linked_at timestamptz NOT NULL DEFAULT now();

CREATE TABLE customer_audit (
 id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
 organization_id uuid NOT NULL REFERENCES organizations,
 team_id uuid REFERENCES teams,
 contact_id uuid REFERENCES contacts,
 project_id uuid REFERENCES intake_projects,
 actor_subject text NOT NULL REFERENCES app_users,
 action text NOT NULL CHECK (action IN ('customer.create','customer.edit','customer.credit-code','contact.create','contact.edit','contact.archive','contact.restore','project-contact.link','project-contact.unlink')),
 details jsonb NOT NULL DEFAULT '{}',
 at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX customer_audit_org ON customer_audit(organization_id, at DESC);
CREATE TRIGGER customer_audit_immutable BEFORE UPDATE OR DELETE ON customer_audit
 FOR EACH ROW EXECUTE FUNCTION project_evidence_immutable();
