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
- [ ] Hardware gate pending owner: real SD card insert → dated folders + skip + move mode (needs physical card)

## Phase 3 — MTP ✅ CODE COMPLETE (2026-09-26, hardware gate pending)
- [x] `internal/camera/mtp`: `go-ole` Shell.Application — all COM on one OS-locked worker goroutine per Source (apartment-safe); `NameSpace(17)` This PC enumeration, device match by PNP-device-id path or model name
- [x] `ListMedia` recursive walk collecting files under DCIM/PRIVATE paths (brand-agnostic, skips THMBNL/THUMBNAIL)
- [x] `CopyTo` → `Folder.CopyHere(item, 9748)` + `waitSettled` size-stable check (3×500ms, 10min cap) — truncated-upload race impossible; post-copy size verify
- [x] `SupportsDelete()=false` for MTP — move mode copy-only on MTP (safer than Python's unreliable delete), mass storage keeps delete
- [x] `syncengine.MediaSource` interface — massstorage + MTP unified; engine syncs either mode
- [x] Agent: MTP devices get auto-sync loops + manual scan-now support
- [x] `go build/test/vet` all clean; agent runs WMI portable-device scan without errors
- [ ] Hardware gate pending owner: ILCE-7RM3 + NEX-5R + ZV-E10 list/download over MTP (needs physical camera)

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
