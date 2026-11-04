-- 批准后由 Baobei 数据库初始化管理员在专用 baobei 数据库执行。
BEGIN;
DO $$
BEGIN
 IF NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname='baobei_staff_api') THEN
  CREATE ROLE baobei_staff_api LOGIN NOSUPERUSER NOCREATEDB NOCREATEROLE NOREPLICATION NOBYPASSRLS;
 END IF;
END $$;
GRANT CONNECT ON DATABASE baobei TO baobei_staff_api;
GRANT USAGE ON SCHEMA public TO baobei_staff_api;
COMMIT;
