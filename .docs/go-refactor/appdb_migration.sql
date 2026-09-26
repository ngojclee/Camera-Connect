-- ============================================================
-- Camera Connect — AppDB registration migration (owner-run)
-- Target: https://appdb.lengoc.me (Supabase project DB)
--
-- AppDB is a SHARED multi-app backend: all apps live in the
-- generic extension_* schema and are distinguished by
-- extension_key rows in public.extension_catalog (created by
-- 001_core_schema.sql of the LNC-Proxy/LuxeClaw migrations).
-- We do NOT create cameraconnect_* tables.
--
-- Run in the Supabase SQL editor as the owner role.
-- ============================================================

begin;

-- Register the app slug. extension_key must match
-- '^[a-z0-9]+(?:[._-][a-z0-9]+)*$' — 'camera_connect' is valid.
insert into public.extension_catalog (extension_key, display_name, status)
values ('camera_connect', 'Camera Connect', 'active')
on conflict (extension_key) do nothing;

commit;

-- Verification (run after):
--   select id, extension_key, display_name, status
--   from public.extension_catalog where extension_key = 'camera_connect';
--
-- After this row exists the app can:
--   * extension_enroll_current_installation(p_extension_key := 'camera_connect', ...)
--   * extension_upsert_setting(p_extension_key := 'camera_connect', ...)
--   * extension_heartbeat_installation(...)
-- Note: enroll requires a tenant (p_tenant_id). First-time users call
-- extension_create_tenant first — handled by internal/appdb enrollment ctx.
