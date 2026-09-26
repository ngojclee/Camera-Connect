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

## Phase 1 — Repo skeleton & foundations ✅ SHIPPED (2026-09-26)
- [x] Create `LICENSE` = PolyForm Noncommercial 1.0.0 + repo hygiene `.gitignore`; untracked `rclone.exe`, installer binary, `CameraConnectConfig.yaml`→`.example.yaml`
- [x] `go.mod` (module `github.com/ngojclee/camera-connect`): wails v2.16, go-winio, yaml.v3, modernc.org/sqlite (go-ole/go-keyring deferred to Phase 3/5)
- [x] Copy+adapt from LightroomSync: `internal/ipc`, `internal/uiapi`, `internal/coordinator`, `internal/logstream`, `internal/platform/windows`, `internal/tray`, `internal/update`, root `main.go`/`wails_*.go`/`platform_*.go` — all Lightroom identifiers removed/renamed (heartbeat+lock monitor dropped; Phase 5 replaces with AppDB heartbeat)
- [x] `internal/config`: layered loader (`config.yaml` shared + `config.local.yaml` machine); `MigrateFromLegacyPaths` imports `{exe}Config.yaml`→ `profiles[default]` + shared settings + backup copy (free_space forced OFF)
- [x] Profiles folded into `internal/config` (`Profile` schema + `active_profile` + `local.profile_paths` per-machine override + `ResolvedBasePath`)
- [x] `internal/store`: SQLite (modernc) — `files(camera_path,camera,profile,size,sha256?,dest,state,synced_at)`, `jobs(id,kind,payload,state,attempts,next_retry,last_error)`, `kv`
- [x] IPC contract: `ping, get_status, get_config, save_config, list_cameras, scan_now, list_profiles, set_profile, import_dates, backup_status, retry_backups, appdb_status, appdb_login, appdb_logout, vault_push, vault_pull, check_update, download_update, subscribe_logs, pause_sync, resume_sync, shutdown_agent`
- [x] `cmd/agent`: mutex, event bus, single-flight sync worker, watchdog, tray host + status publisher, resume detector, path health monitor, update check/download, inline IPC dispatch
- [x] Frontend skeleton: Vite+TS+Tailwind v4 + LN-UI-Engine theme CSS imported; dashboard renders status/cameras/profiles/logs via `window.go` bridge
- [x] Gate: `go build ./...` ✅ · `go test ./...` ✅ · `go vet ./...` ✅ · `npm run build` ✅ · agent launch + `--action ping/get-status/get-config/save-config/scan-now/shutdown-agent` all verified over named pipe; `%LOCALAPPDATA%\CameraConnect\{config.yaml,config.local.yaml,cache.db,tray_status.json}` created

## Phase 2 — Detection v2 + Mass Storage engine ✅ (2026-09-26)
- [x] `internal/detect`: WMI `Win32_PnPEntity` poll — `PNPClass=PortableDevice`/`Service=WUDFWpdFs` + brand VID table (Sony 054C, Canon 04A9, Nikon 04B0, Fuji 04CB, Panasonic 04DA, OM 07B4, GoPro 2672, DJI 2CA3, Creative, Blackmagic)
- [x] Drive discovery: `GetLogicalDrives` + `GetDriveTypeW(DRIVE_REMOVABLE)` + `DCIM|PRIVATE` probe + volume label; `Win32_DiskDrive→Partition→LogicalDisk` correlation for model names (StackExchange/wmi)
- [x] `internal/camera/massstorage`: recursive scan (skips THMBNL/THUMBNAIL), `PRIVATE/M4ROOT/CLIP` video scan, date-from-folder or mtime; `CopyTo` via `.part`+fsync+size-verify+rename (crash-safe); `Delete` removes THMBNL thumbs + XML sidecars
- [x] `internal/syncengine`: template renderer `{camera}/{type}/{yyyy}/{yy}/{mm}/{m}/{dd}/{d}/{date}` + legacy `{year}/{month}/{day}`; `SyncDevice` per-camera pass honoring `scan_mode`, `poll_interval`, `active_profile`, `sync_mode`; filesystem-existence skip (filesystem=truth, DB=history); `PendingUpload` → jobs queue
- [x] Agent wiring: `detect.Watcher`→state/events; `deviceLoopManager` (auto_sync on connect; continuous re-scan at poll_interval; stops on disconnect); `scan_now` runs real sync on mass-storage devices
- [x] Tests: template render, fake-card copy/skip/move incl. sidecar cleanup — `go test ./...` all pass
- [x] **Hardware gate PASSED 2026-09-26** (commit `08810bc`): real Sony SD card (`G:\`) — filtered 2 WPD/USBSTOR ghost entries; `sync-now` copied **47 files** (JPG + MP4 ~2.5GB incl. 800MB videos) → `D:\Temp\CameraImport\Camera Card\2026\2026-02-01\` via `.part`+rename; second sync skipped all 47; 0 failures, 0 orphan .part files
- [x] `save-config` accepts `profiles`/`profile_paths`/`file_type_*` patch fields (was missing — profiles could never be saved)

## Phase 3 — MTP ✅ DONE (2026-09-26, hardware-validated on ZV-E10)
- [x] `internal/camera/mtp`: `go-ole` Shell.Application — all COM on one OS-locked worker goroutine per Source (apartment-safe); `NameSpace(17)` This PC enumeration, device match by PNP-device-id path or model name
- [x] `ListMedia` recursive walk collecting files under DCIM/PRIVATE paths (brand-agnostic, skips THMBNL/THUMBNAIL)
- [x] `CopyTo` → `Folder.CopyHere(item, 9748)` + `waitSettled` size-stable check (3×500ms, 10min cap) — truncated-upload race impossible; post-copy size verify
- [x] `SupportsDelete()=false` for MTP — move mode copy-only on MTP (safer than Python's unreliable delete), mass storage keeps delete
- [x] `syncengine.MediaSource` interface — massstorage + MTP unified; engine syncs either mode
- [x] Agent: MTP devices get auto-sync loops + manual scan-now support
- [x] `go build/test/vet` all clean; agent runs WMI portable-device scan without errors
- [x] Hardware gate PASSED on ZV-E10 (2026-09-26): enumerate 47 files, copy 2.6GB incl. 800MB MP4, idempotent skip pass 47/47, no dialogs

## Phase 4 — Upload pipeline v2 ✅ (2026-09-26)
- [x] Staging route: backup-enabled profiles write `<base>/_staging/<id>/<destRel>` (same volume); syncengine skips camera re-copy when staged copy exists
- [x] `internal/backup.Worker`: serialized loop over `jobs` table (store.DueJobs → MarkJobRunning/Done/Failed w/ backoff); `rclone copy --files-from` scoped to batch only — never base root; per-job 2h timeout; injectable runner for tests
- [x] Post-upload verify: `rclone lsjson` size check per file → then `os.Rename` staged→final (or delete when `free_space`); crash-between-verify handled (missing staged + existing final = done); `pruneEmptyDirs` cleans staging
- [x] `uploadBatcher`: accumulates staged files per profile → one upload job per sync pass (flush on EvtSyncCompleted/Failed); `ReconcileStaging` at startup re-enqueues orphans after crash
- [x] `retry_backups` IPC → `RequeueJobs` + worker.Wake; `backup_status` lists queue
- [x] Tests: fake rclone runner — success finalize, free-space delete, failure→retry_wait survival — all pass; `go build/test/vet` clean
- [ ] Hardware gate pending owner: real rclone.conf + Drive remote; 4GB video kill-mid-upload resume check

## Phase 5 — AppDB sync + vault ✅ CODE COMPLETE (2026-09-26, live gates pending)
- [x] Migration SQL authored: `.docs/go-refactor/appdb_migration.sql` registering slug `camera_connect` (owner runs on appdb.lengoc.me; adjust table name if schema differs)
- [x] `internal/appdb`: GoTrue `/auth/v1/token` password+refresh grant; PostgREST RPCs (`enroll`, `heartbeat`, `upsert_setting`) + `extension_settings` GET with scope-column map (tenant/user/device/installation/shop); anon key embedded (public-by-design, rejects `sb_secret_*`); `SessionStore` persists session+enroll ctx in secrets dir (0600)
- [x] `internal/vault`: PBKDF2-SHA256(150k)+AES-256-GCM, byte-compatible wire format with `cred-vault.js` (16B salt/12B IV/tag check); ErrAuth vs ErrMalformed separated; tests incl. tamper/wrong-pass
- [x] `Service.PushRcloneConf/PullRcloneConf`: seal→upsert `rclone_bundle` @ user scope w/ revision check; pull→unseal→`secrets\rclone.conf` (0600); session auto-refresh on expiry
- [x] IPC: `appdb_status/login/logout/vault_push/vault_pull` all wired — verified live: bad login → clean `invalid_credentials` from appdb.lengoc.me; missing rclone.conf → clear error; logged-out → `appdb: not authenticated`
- [ ] Tenant picker for multi-tenant users (first active tenant auto-picked for now)
- [ ] Settings sync loop (profiles @ user scope, device_paths @ device scope) + heartbeat loop — lands with Phase 6 sync UI
- [ ] Live gate pending owner: run SQL migration → real login → push/pull rclone.conf → machine B fresh-install works without manual copy

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
