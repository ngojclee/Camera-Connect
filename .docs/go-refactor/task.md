# Camera Connect — Go Refactor Tasks (W1)

> Paired with [./plan.md](./plan.md). Checkboxes per phase. Status: PROPOSAL.
> Legend: `[ ]` todo · `[x]` done · `[~]` in-progress/blocked

---

## Phase 0 — Python hotfix ✅ SHIPPED (2026-09-26)
- [x] In `src/backup_manager.py`: force `operation="copy"`; `delete_local_after_upload` disabled with warning until v2 pipeline exists
- [x] Scope backup to files downloaded this run: `_pending_upload` → `--files-from` (manual import path also collected + triggers backup)
- [x] Single-flight `threading.Lock` + `_pending_files` requeue-on-failure in `_backup_worker`
- [x] Post-`CopyHere` settle check `_wait_settled()` (size stable 3×500ms, 10min cap) before `download_file` returns True
- [x] Compile check OK (`py_compile` all touched files)
- [x] Commit + push to `main`

## Phase 1 — Repo skeleton & foundations
- [ ] Create `LICENSE` = PolyForm Noncommercial 1.0.0 (owner confirms) + repo hygiene `.gitignore` (rclone.exe/conf, secrets/, dist/, build/, config.local.yaml)
- [ ] `go.mod` (Go 1.25.x): wails v2.12, go-winio, yaml.v3, modernc.org/sqlite, go-ole, go-keyring
- [ ] Copy+adapt from LightroomSync: `internal/ipc`, `internal/uiapi`, `internal/coordinator`, `internal/logstream`, `internal/platform/windows`, `internal/tray`, `internal/update`, root `main.go`/`wails_*.go`/`platform_*.go`
- [ ] `internal/config`: layered loader (`config.yaml` shared + `config.local.yaml` machine) with dot-path get/set; `MigrateFromLegacyPaths` importing `{exe}Config.yaml` + `~/.sony_camera_sync`
- [ ] `internal/profiles`: schema (id/name/base_path/templates/file_types/backup), `active_profile`, `machines.<hostname>.profile_paths` override resolution
- [ ] `internal/store`: SQLite schema — `files(path,camera,profile,size,sha256?,synced_at,dest)`, `jobs(id,kind,payload,state,attempts,next_retry)`, `stats` — migrations via `embed`
- [ ] IPC contract: commands `ping, get_status, get_config, save_config, list_cameras, scan_now, list_profiles, set_profile, import_dates, backup_status, retry_backups, appdb_login, appdb_logout, vault_push, vault_pull, check_update, download_update, subscribe_logs, shutdown_agent`
- [ ] Gate: `go build ./...` clean; agent + UI exes launch; `--action get-status` CLI works

## Phase 2 — Detection v2 + Mass Storage engine
- [ ] `internal/detect`: WMI `Win32_PnPEntity` poll (2s) — class-based (`PortableDevice`/`WUDFWpdFs`) + VID/PID friendly-name table (Sony 054C, Canon 04A9, Nikon 04B0, Fuji 04CB, Panasonic 04DA, OM 07B4, GoPro 2672, DJI 2CA3)
- [ ] Drive discovery: `GetLogicalDrives` + `GetDriveTypeW(DRIVE_REMOVABLE)` + `DCIM|PRIVATE` probe; correlate drive→camera via PNP DeviceID; unmatched drives exposed as standalone sources
- [ ] `internal/camera/massstorage`: port `mass_storage_handler.py` — list, `copy2`-equiv w/ progress, delete (+ thumbnail/XML cleanup), `find_all_camera_drives`
- [ ] `internal/syncengine`: per-camera loop honoring `scan_mode`, `poll_interval`, `active_profile`; filesystem-existence skip; templates `{camera}/{yyyy}/{yyyy}-{mm}-{dd}` etc.
- [ ] Extended file-type defaults + per-profile overrides
- [ ] Gate: sync real SD card → correct dated folders, skip-existing works, move mode deletes verified copies only

## Phase 3 — MTP (risk phase)
- [ ] Spike: `go-ole` IDispatch → `Shell.Application`/`Namespace`/`CopyHere` PoC (≤1 day) — decide a/b/c per plan §6
- [ ] Implement MTP list/download with post-copy settle check (size-stable polling — fixes async race)
- [ ] Brand-agnostic roots: enumerate storages → locate `DCIM` recursively; per-brand override config
- [ ] Move-mode policy per owner decision (recommend mass-storage-only)
- [ ] Gate: ILCE-7RM3 + NEX-5R + ZV-E10 list/download over MTP; truncated-upload race impossible

## Phase 4 — Upload pipeline v2
- [ ] Staging dir `<base_path>/_staging/<profile>/` (per owner decision) + same-volume finalize move
- [ ] `internal/backup`: serialized worker over `jobs` table; `rclone copy --files-from` per batch; timeout + backoff; resume-after-restart
- [ ] Post-upload verify (`rclone check`/`lsjson` size+hash) → move-to-final or delete-local (free-space mode), `--delete-empty-src-dirs` only within staging
- [ ] UI safety copy for `delete_local_after_upload` (scope explanation + confirm)
- [ ] Gate: kill app mid-upload → restart → retry completes; large video (≥4GB) verified end-to-end

## Phase 5 — AppDB sync + vault
- [ ] Owner runs SQL migration on appdb.lengoc.me registering slug `camera_connect` (author `012_camera_connect.sql` modeled on LNC-Proxy `004_extension_tables.sql`)
- [ ] `internal/appdb`: GoTrue login/refresh + PostgREST calls (`extension_settings` get, `extension_upsert_setting`, `extension_enroll_current_installation`, `extension_heartbeat_installation`); anon-key validation (reject `sb_secret_`)
- [ ] `internal/vault`: PBKDF2-SHA256(150k)+AES-GCM-256 seal/open matching `cred-vault.js` format exactly
- [ ] Settings sync: `profiles`+`shared_config` @ user scope; `device_paths` @ device scope; revision-conflict handling → UI reload prompt
- [ ] rclone.conf push/pull: seal→upsert `rclone_bundle` (user scope) / fetch→unseal→write `secrets\rclone.conf`; optional passphrase persist via Windows Credential Manager
- [ ] Heartbeat loop + Devices list in UI
- [ ] Gate: machine B fresh install → login → passphrase → Drive upload works without manual file copy

## Phase 6 — UI
- [ ] `frontend/` Vite+TS+Tailwind v4; `bridge.ts` (Wails + mock fallback); App/template/styles skeleton from LRSync
- [ ] Import LN-UI-Engine `src/theme/*.css`; map tokens to Tailwind `@theme`; light+dark
- [ ] Panels: Dashboard, Import, Profiles, Backup, Sync(AppDB), History, Logs, Settings, About/Update
- [ ] Profile switcher in header; native dir pickers via Wails runtime; offline-overlay pattern
- [ ] Tray integration + single-instance + autostart registry

## Phase 7 — Installer, update, release
- [ ] `installer/CameraConnectSetup.iss` (new AppId GUID) + `build_windows.ps1`/`build_installer.ps1` adapted
- [ ] Self-update E2E: check→download `.part`→install over same dir→config preserved→relaunch minimized
- [ ] `.github/workflows/windows-release.yml` adapted; tag `v*.*.*.*` release flow
- [ ] README (EN+VI), screenshots, open-source prep checklist

## Phase 8 — Testing
- [ ] Port e2e ps1 smoke suite (agent IPC ping/status, sync-now via CLI action, tray presence)
- [ ] Brand matrix test: Sony (MTP+MS), Canon/Nikon SD card at minimum
- [ ] Migration test: Python config → layered config; DB preserved stats
- [ ] Failure tests: unplug mid-sync, rclone crash mid-move, offline AppDB, wrong passphrase, revision conflict
- [ ] Soak: continuous mode 24h incl. large video batches
- [ ] `cortex_quality_report` + final review; owner sign-off → public release

---

### Backlog / v2
- [ ] Tenant-scope preset sharing (team packs)
- [ ] `rclone config` wizard inside UI (replace console popout)
- [ ] Hash-based dedupe option (slower, catches renamed files)
- [ ] macOS/Linux port evaluation (Wails cross-platform; MTP → gphoto2/libmtp)
