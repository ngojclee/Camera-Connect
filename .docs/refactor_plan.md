# Camera Connect — Go Refactor Plan & Options Analysis

> Status: PROPOSAL (awaiting owner decision)
> Date: 2026-09-26
> Scope: full rewrite (Go + Wails), upload-pipeline bugfix, profiles, multi-machine config, self-update, multi-brand support, open-source release prep.

---

## 0. Why refactor at all (findings from Python audit)

| Finding | Severity | Evidence |
|---|---|---|
| `rclone move` runs on the ENTIRE `destination.base_path` (the studio library root), not a staging area | **Critical — data loss** | `src/main.py:512-518`, `src/backup_manager.py:88-113` |
| MTP `CopyHere` is async/fire-and-forget; file marked done + backup triggered while still writing → truncated upload then local delete under `move` | **Critical — matches reported video loss** | `src/camera/mtp_handler.py:289`, `src/main.py:209-210` |
| Backup threads: no single-flight, no timeout, no retry; failed runs only retry when NEW files arrive | High | `src/backup_manager.py:82-131` |
| Notification callback never wired — all notifications are no-ops | Medium | `src/main.py:34`, `509` |
| `mark_completed` updates 0 rows (no `add_file` first) — DB/stats broken | Medium | `src/main.py:247`, `queue_manager.py:212-228` |
| Duplicate `is_syncing`/`get_connected_cameras` defs; dead config keys (`google_drive`, `nas`, `cameras`, `delete_after_sync`, `play_sound`); dead deps in requirements.txt | Low | `src/main.py:340-393`, `CameraConnectConfig.yaml` |
| `CameraConnect.spec` bundles rclone AFTER `Analysis()` — never actually bundled | Low | `CameraConnect.spec:46,66` |
| Any removable drive with DCIM/PRIVATE is synced under whichever camera is connected (no device↔drive correlation) | Medium | `src/main.py:157-173`, `mass_storage_handler.py` |
| Detector hardcodes Sony VID_054C PIDs only — no multi-brand | Gap | `src/detector.py:19-106` |

**Interim safety patch (optional, ~30 lines, recommended before refactor ships):** force `operation = "copy"` (ignore `delete_local_after_upload`), scope rclone to `--files-from` of files the app itself downloaded this session, add a module-level `threading.Lock` around `_run_rclone_copy`. Eliminates both critical paths without touching architecture.

---

## 1. Stack options — decision required

### Option A — Go + Wails (clone LightroomSync architecture) ✅ RECOMMENDED

**Agent/UI split, proven on this exact machine:**

- `cmd/agent` — background daemon: USB/drive detection, sync worker (single-flight), rclone pipeline, named-pipe IPC server, PowerShell-hosted tray, self-update. CGO_ENABLED=0.
- Root Wails app (`main.go`, `wails_app.go`, `wails_runtime*.go` at root — forced by Wails embed constraint) — thin UI client of IPC.
- `frontend/` — vanilla TypeScript + Tailwind v4 + Vite. **Not React.**
- Reuse near-verbatim from `D:\Python\projects\LightroomSync`: `internal/ipc`, `internal/uiapi`, `internal/config` (incl. `MigrateFromLegacyPaths` — perfect for importing `CameraConnectConfig.yaml`), `internal/coordinator` (single-flight worker + watchdog + event bus + AppState), `internal/logstream`, `internal/update`, `internal/platform/windows` (mutex/registry/process), `internal/tray` (PS tray host), `bridge.ts`, build scripts, `.iss` installer, GH release workflow.
- Estimated reuse: ~60–70% of infrastructure code is rename-and-adapt.

**Pros:** fastest/lightest (Go binary, ~10-20MB, low RAM, instant startup); proven in-house; installer+updater+tray solved; CGO-free agent.
**Cons:** MTP access via `Shell.Application` COM needs `go-ole` IDispatch glue (tedious but doable) OR a different strategy (see §5).

### Option B — Go + Wails + React + `@luxeclaw/ui-engine`

- Same backend as A; frontend React, engine installed via `file:D:/Python/projects/LN-UI-Engine`.
- Engine reality check: v0.6.4, ~2k LOC, 46 thin className wrappers + theme CSS. No sidebar/titlebar/toast-manager/modal-focus-trap/icons — those get hand-built anyway. `react-router-dom` becomes a forced dep (top-level import in `primitives.tsx`) even for a single-window app.
- **Verdict:** adds React + router + package plumbing for marginal gain. The engine's real value is `src/theme/*.css` tokens — **which are pure CSS and work in vanilla TS too** (Option A can import them directly or port to Tailwind `@theme`).

### Option C — Rust + Tauri / C# .NET

- Rust+Tauri: smallest binary, but zero in-house precedent, WPD/MTP COM interop equally painful, frontend still needed.
- C#/.NET (WPF/WinUI): best-in-class MTP via WPD COM, single language; but heavier runtime, new toolchain, throws away the LightroomSync reuse.
- **Verdict:** only revisit if Option A's MTP path proves impossible.

### UI direction (decided independent of A/B)

"Simple but beautiful, learn from LN-UI-Engine" → adopt its theme (`--bg-*`, `--accent`, `--radius`, calm ops-console light+dark) as the design language, applied on the LightroomSync-style shell (sidebar + panels). Dark default like LightroomSync; add `data-theme="light"` support since engine ships both.

---

## 2. Database — options

| Option | What | Verdict |
|---|---|---|
| **A. No DB** | Filesystem existence = truth (already the real skip mechanism); stats/history in-memory + JSONL log file | Viable, simplest; loses history across restarts |
| **B. SQLite via `modernc.org/sqlite`** ✅ | Pure Go (CGO-free — required for agent), single file in config dir; tables: `files` (dedupe/history), `jobs` (backup queue+retry state), `stats` | Recommended — enables Queue/History UI tab, persistent retry queue for failed uploads (fixes "unplug = pending forever"), and survives restarts |
| **C. bbolt (KV)** | Simplest embedded KV | Less expressive than SQLite for history queries; fine but B costs the same |

**Recommendation: B.** The current app already wants a DB (`queue_manager.py`) but uses it as a broken write-only cache. In the Go version the DB earns its place: upload retry-queue and sync history are real features, dedupe stays filesystem-first.

---

## 3. Config & profiles design

### Layered config (multi-machine)

```
%LOCALAPPDATA%\CameraConnect\
├── config.yaml          # SHARED — synced across machines (non-secret)
├── config.local.yaml    # MACHINE — never synced; keyed by hostname
├── secrets\             # rclone.conf etc. — never synced, never in git
├── cache.db             # SQLite
└── logs\
```

**config.yaml (shared):** file_types, scan_mode, poll_interval, notifications, **profiles[]** definitions, ui.theme, remote_name (name only, no tokens).
**config.local.yaml:** `machine_name` (auto `os.Hostname()`), per-machine `base_path` overrides per profile, `backup.enabled/remote_path`, rclone.conf path, window state.

Resolution order: profile field → `config.local.yaml` machine override → `config.yaml` default → builtin default.

### Profiles (owner requirement: pick profile → copy goes to its folder)

```yaml
profiles:
  - id: luxeclaw
    name: LuxeClaw Products
    base_path: D:/Photos/CameraConnect      # machine-overridable
    photo_template: '{camera}/{yyyy}/{yyyy}-{mm}-{dd}'
    video_template: Video
    file_types: [ARW, JPG, JPEG, HEIF, MP4, MTS]
    backup: {enabled: true, remote_path: Backup/LuxeClaw}
  - id: personal
    name: Personal
    base_path: D:/Photos
    ...
active_profile: luxeclaw
```

UI: profile dropdown in header + per-profile "set folder" (native dir picker via Wails runtime). Switching is instant — sync loop reads `active_profile` each cycle. Per-machine path override: `config.local.yaml → machines.<hostname>.profile_paths.<id>`.

### Migration

`MigrateFromLegacyPaths` (reused): detect `{exe}Config.yaml` / `%USERPROFILE%\.sony_camera_sync\` → import into layered config → archive legacy file.

---

## 4. Secrets & open-source safety

- **Never in repo:** `rclone.conf`, `secrets/`, `config.local.yaml` → `.gitignore` + `LICENSE` (see §8).
- **rclone.conf is machine-local by design** (OAuth tokens bound to the rclone remote the user creates). The shared-config sync therefore excludes it — bootstrap = per-machine `rclone config` wizard (app launches it, like today) or encrypted export/import (`rclone config` obscure / age-encrypted bundle) for advanced users.
- **Shared-config sync transport:** once backup remote exists, app can push/pull `config.yaml` to `<remote>:CameraConnect/config.yaml` — uses user's own cloud, zero API keys in the app. Git-based sync also possible later.
- No backend DB/Supabase → nothing to leak. App is fully local-first.

---

## 5. Multi-brand camera support

Current: Sony VID `054C` + PID table only. Plan:

1. **Mass Storage path is already brand-agnostic** — any removable drive containing `DCIM/` (DCF standard). Keep + fix device↔drive correlation (match drive's PNP device ID to the detected camera where possible; else list drives as independent sources in UI).
2. **Detection by class, not VID:** WMI `Win32_PnPEntity` where `PNPClass='PortableDevice'`/`WPD` OR service `WUDFWpdFs` → catches Canon/Nikon/Fuji/Panasonic/OM/GoPro/DJI without PID tables. Keep VID table as fast-path + friendly-name map.
3. **MTP root discovery:** Sony uses `Storage Media`; Canon/Nikon expose storage(s) then `DCIM` — implement "enumerate storages → recursively locate DCIM" instead of hardcoded root. Per-brand root overrides in config as escape hatch.
4. **File types:** ship extended defaults — photos `ARW CR2 CR3 NEF RAF ORF DNG GPR JPG JPEG HEIF`, videos `MP4 MTS AVCHD MOV LRV MKV`; editable per-profile.

### MTP strategy in Go (the one real port risk)

Ordered options:
- **a) `go-ole` driving `Shell.Application` COM** — same mechanism as Python, moderate tedium, synchronous-wait solvable (poll dest size-stability post-CopyHere). Preferred: identical behavior, no new deps beyond `go-ole`.
- **b) WPD API via COM (`PortableDevice` objects)** — more correct (real byte-stream transfer with progress), higher effort, better long-term.
- **c) Python sidecar** — keep `mtp_handler.py` as a subprocess shim for MTP only. Ugly but zero-risk fallback; decides whether refactor ships blocked on MTP.
- Mass Storage needs none of this (pure `os`/`filepath`) and is already the recommended mode.

---

## 6. Upload pipeline v2 (the actual bugfix)

```
[download] → staging/<profile>/ → [size-stable 2s + optional hash]
          → rclone copy --files-from <batch list> staging → remote
          → on exit 0 + rclone check/lsjson verify:
              move staging → final dest  (copy mode)
              OR delete local            (move/free-space mode)
          → on failure: files stay in staging, job row = retryable, retry on next trigger/timer
```

Kills all three current holes: scoped file list (never touches user's library), settle-check before upload (no truncation), verified delete (delete only post-verified-upload), serialized worker + retry persisted in SQLite, timeout + exponential backoff.

`delete_local_after_upload` gets a safety UI: explain scope, require the staging flow, never run `rclone move` on a user root again.

---

## 7. Self-update & release

Reuse `internal/update` wholesale: GitHub releases check → asset scoring → `dest.part` streaming download → launch Inno installer detached → installer kills app, installs over same dir (config lives in `%LOCALAPPDATA%` → preserved by design), `--minimized` restart. Versioning `x.y.z.k` per house convention. CI: copy `windows-release.yml`, regenerate Inno `AppId` GUID.

**License (non-commercial):** recommend **PolyForm Noncommercial 1.0.0** (purpose-built for "use but don't sell") or **BSL 1.1** (auto-converts to Apache after N years). CC BY-NC works but isn't software-designed. Decide before first public push.

---

## 8. Phasing

| Phase | Deliverable | Notes |
|---|---|---|
| 0 | Python hotfix (optional): copy-only + scoped files + backup mutex | 30 lines, protects current users now |
| 1 | Repo skeleton: license, .gitignore, go.mod, agent+Wails boot, config layer + migration, SQLite schema, IPC contract | Reuse LRSync files |
| 2 | Detection v2 (class-based + VID table) + Mass Storage sync + profiles applied | Biggest user-visible win |
| 3 | MTP handler via go-ole w/ settle-check download | Risk item; fallback = sidecar |
| 4 | Upload pipeline v2 (staging + verified delete + retry queue) | THE bugfix |
| 5 | UI: dashboard/cameras/profiles/history/settings/logs/update panels, LN-Engine theme | Vanilla TS + Tailwind |
| 6 | Installer + self-update + GH Actions + README/LICENSE publish | Public release |

Open questions for owner:
1. Staging folder location: `<base_path>/_staging` (simplest, same volume = instant moves) vs config-dir (cross-volume copy cost)? → recommend in-base-path, hidden folder.
2. Keep `sync_mode: move` (delete-from-camera) for MTP? It's dialog-prone today; mass-storage only?
3. App name stays "Camera Connect"?
