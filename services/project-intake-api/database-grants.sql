-- Executed by the production migrator in the same transaction as schema updates.
GRANT SELECT ON baobei_migrations TO baobei_runtime, baobei_maintenance;

GRANT SELECT ON app_users, reference_values, organization_aliases TO baobei_runtime;
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
