-- Executed by the production migrator in the same transaction as schema updates.
GRANT SELECT ON baobei_migrations TO baobei_runtime, baobei_maintenance;

REVOKE SELECT ON app_users FROM baobei_runtime;
GRANT SELECT (subject,display_name,role,team_id,active,staff_permissions) ON app_users TO baobei_runtime;
GRANT UPDATE (last_login_at) ON app_users TO baobei_runtime;
GRANT SELECT ON reference_values, organization_aliases TO baobei_runtime;
GRANT SELECT, INSERT, UPDATE ON intake_projects, organizations, project_locations,
    registration_counters TO baobei_runtime;
GRANT SELECT, INSERT, UPDATE, DELETE ON duplicate_cases TO baobei_runtime;
GRANT SELECT, INSERT, DELETE ON app_sessions, oidc_pending_logins TO baobei_runtime;
GRANT INSERT, DELETE ON project_parties, planned_equipment, project_requirements TO baobei_runtime;
GRANT SELECT ON project_parties TO baobei_runtime;
GRANT SELECT (project_id) ON planned_equipment, project_requirements TO baobei_runtime;
GRANT SELECT, INSERT ON project_activities, approvals, ownership_history, audit_events TO baobei_runtime;

REVOKE EXECUTE ON FUNCTION next_registration_no() FROM PUBLIC;
GRANT EXECUTE ON FUNCTION next_registration_no() TO baobei_runtime;

GRANT SELECT, INSERT, UPDATE ON teams, app_users TO baobei_maintenance;
GRANT SELECT (subject), DELETE ON app_sessions TO baobei_maintenance;
GRANT INSERT ON audit_events TO baobei_maintenance;

-- The staff connection serves verified OIDC logins and staff operations.
-- It has no DDL or project-content writes.
GRANT SELECT ON baobei_migrations, app_users, teams, staff_audit, staff_outbox, staff_handoffs TO baobei_staff_api;
GRANT SELECT (token_hash,subject,expires_at), INSERT, DELETE ON app_sessions TO baobei_staff_api;
GRANT SELECT, INSERT ON staff_sessions TO baobei_staff_api;
GRANT INSERT (subject,display_name,role,team_id,active,staff_id,work_email,staff_status,staff_note,staff_permissions,staff_managed)
 ON app_users TO baobei_staff_api;
GRANT UPDATE (display_name,role,team_id,active,work_email,staff_status,staff_version,staff_note,staff_permissions,staff_managed,last_login_at)
 ON app_users TO baobei_staff_api;
GRANT INSERT, UPDATE (name,staff_version) ON teams TO baobei_staff_api;
GRANT INSERT ON staff_audit, staff_handoffs TO baobei_staff_api;
GRANT INSERT, UPDATE (status,processed_at) ON staff_outbox TO baobei_staff_api;
GRANT SELECT (id,owner_subject,team_id,registration_no,project_name,intake_status,sales_stage,version,updated_at),
 UPDATE (owner_subject,team_id,version,updated_at) ON intake_projects TO baobei_staff_api;
GRANT SELECT (project_id,from_subject,to_subject), INSERT ON ownership_history TO baobei_staff_api;
GRANT INSERT ON audit_events TO baobei_staff_api;

GRANT SELECT, INSERT ON staff_audit TO baobei_maintenance;
GRANT SELECT, UPDATE (status,processed_at,receipt_hash) ON staff_outbox TO baobei_maintenance;
