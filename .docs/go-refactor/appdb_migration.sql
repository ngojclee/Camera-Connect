-- ============================================================
-- Camera Connect — AppDB registration migration (owner-run)
-- Target: https://appdb.lengoc.me (Supabase project DB)
-- Purpose: register the `camera_connect` extension slug so the
--          shared extension_* schema accepts Camera Connect
--          devices/settings alongside LNC-Proxy.
--
-- NOTE: run this in the Supabase SQL editor or psql as a role
-- with write access to the extension tables. Verify table names
-- against the live schema first (they were originally created by
-- the LNC-Proxy migration 004_extension_tables.sql).
-- ============================================================

begin;

-- 1) Extension/app registration --------------------------------
-- Adjust table/column names if the live schema differs
-- (candidates: public.extensions, public.extension_apps,
--  columns: slug | extension_key, name | display_name).
insert into public.extensions (slug, name)
values ('camera_connect', 'Camera Connect')
on conflict (slug) do nothing;

-- Fallback variant if the registry uses extension_key naming:
-- insert into public.extensions (extension_key, name)
-- values ('camera_connect', 'Camera Connect')
-- on conflict (extension_key) do nothing;

-- 2) Optional: grant usage on extension_settings sequences -----
-- Only needed if the schema uses sequences owned by another role.
-- (Usually unnecessary with Supabase + RLS.)

commit;

-- Verification queries (run after):
-- select * from public.extensions where slug = 'camera_connect';
-- select * from public.extension_tenants;   -- confirm tenant exists
--
-- After this migration the app can:
--   * enroll installations  (extension_enroll_current_installation)
--   * upsert scoped settings (extension_upsert_setting)
--   * heartbeat             (extension_heartbeat_installation)
-- with p_extension_key = 'camera_connect'.
