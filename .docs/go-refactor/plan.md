# Camera Connect — Go Refactor Plan (W1)

> **Status:** PROPOSAL — awaiting owner approval
> **Date:** 2026-09-26 · **Author:** Devin · **Session:** sess_1790425266776_tk0y5
> **Companion docs:** [`../refactor_plan.md`](../refactor_plan.md) (full audit findings), [`./task.md`](./task.md)
> **Request:** Refactor Camera Connect (Python/tkinter) to a fast/light/stable stack; fix upload-delete bug; add profiles; multi-machine config sync via AppDB (appdb.lengoc.me) incl. passphrase-sealed rclone.conf; self-update from GitHub releases; multi-brand camera support; open-source release under a non-commercial license.

---

## 1. Decisions Summary

| Question | Decision | Rationale |
|---|---|---|
| Language/UI stack | **Go 1.25 + Wails v2 + vanilla TS/Tailwind v4** (clone LightroomSync) | ~60–70% infra reuse proven on this machine; CGO-free agent; fast, light, single binary |
| Component style | **LN-UI-Engine theme CSS + tokens** adopted into vanilla TS shell; no React | Engine value is `src/theme/*.css` (pure CSS); its React wrappers force react-router for no gain |
| Database | **SQLite via `modernc.org/sqlite`** (pure Go, no CGO) | Upload retry queue + sync history; filesystem remains skip-truth |
| Cloud upload | **rclone binary** (bundled/portable, like today) | Proven, no SDK maintenance |
| Config sync | **AppDB (Supabase self-hosted, appdb.lengoc.me)** — reuse LNC-Proxy contract | Existing tables/RPCs/scopes fit exactly; no new backend needed beyond one migration |
| Secrets sync | **Passphrase-sealed vault** (PBKDF2-SHA256 150k + AES-GCM-256, port of `cred-vault.js`) | rclone.conf encrypted client-side; server stores only sealed blob |
| Auth | Email+password via GoTrue `/auth/v1/token?grant_type=password`; anon key embedded (public-by-design, RLS enforced) | Same as LNC-Proxy; safe for open-source |
| License | **PolyForm Noncommercial 1.0.0** (alt: BSL 1.1) | "Use allowed, commercial use prohibited" |
| Offline path | App fully functional without login; AppDB optional; file export/import fallback for config+secrets bundle | Open-source users without AppDB access |

---

## 2. Target Architecture

```
CameraConnect/                       (new Go module, repo stays camera-connect)
├── cmd/agent/                       # LightroomSyncAgent equivalent — CGO_ENABLED=0
│   └── main.go                      # daemon: detector, sync worker, backup pipeline,
│                                    #   IPC server, tray publisher, updater, AppDB syncer
├── main.go, wails_app.go,           # Wails UI bootstrap (repo root — Wails constraint)
│   wails_runtime*.go, platform_*.go
├── frontend/                        # Vite + vanilla TS + Tailwind v4
│   └── src/{main,App,template,bridge,styles}.ts + theme/engine.css (from LN-UI-Engine)
├── internal/
│   ├── ipc/        uiapi/           # named pipe + ActionEnvelope (copy from LRSync)
│   ├── config/                      # layered: shared.yaml + local.yaml + migration
│   ├── profiles/                    # profile defs, active_profile, per-machine path map
│   ├── detect/                      # PNP-class + VID/PID detection, drive correlation
│   ├── camera/
│   │   ├── massstorage/             # port of mass_storage_handler.go (pure io)
│   │   └── mtp/                     # go-ole Shell.Application port (risk item)
│   ├── syncengine/                  # per-camera loop, skip logic, move/copy, jobs
│   ├── backup/                      # staging dir, settle-check, rclone --files-from,
│   │                                #   verified-delete, retry queue (SQLite)
│   ├── store/                       # modernc.org/sqlite: files, jobs, stats
│   ├── appdb/                       # GoTrue auth + PostgREST client + enroll + settings
│   ├── vault/                       # PBKDF2+AES-GCM seal/open (port of cred-vault.js)
│   ├── update/                      # GitHub release check/download (copy from LRSync)
│   ├── tray/ logstream/             # PS tray host + ring buffer (copy from LRSync)
│   └── coordinator/ platform/       # single-flight worker, watchdog, mutex, registry
├── installer/CameraConnectSetup.iss # new AppId GUID
├── scripts/build_windows.ps1        # adapted from LRSync
├── .github/workflows/windows-release.yml
├── LICENSE                          # PolyForm Noncommercial 1.0.0
└── rclone.exe (release asset, not in git history as binary — via .gitignore + CI fetch
    or installer-bundled; decide per repo-size preference)
```

---

## 3. Config & Profiles Model

```
%LOCALAPPDATA%\CameraConnect\
├── config.yaml            # SHARED (synced via AppDB user-scope): profiles defs,
│                          #   file_types, scan_mode, notifications, active_profile
├── config.local.yaml      # MACHINE (never synced): hostname, per-profile path
│                          #   overrides, backup toggles, window state
├── secrets\rclone.conf    # decrypted output of vault pull; never synced raw
├── cache.db               # SQLite
└── logs\
```

**Profile** (shared): `id, name, base_path, photo_template, video_template, file_types[], backup{enabled, remote_path}`.
**Per-machine override** (local): `machines.<hostname>.profile_paths.<profile_id> = <path>` — wins over profile `base_path` on that machine.
**Resolution:** local override → profile → shared default → builtin default.
**UI:** profile dropdown in header; switching `active_profile` is instant; next sync cycle uses it.

**Legacy migration:** detect `{exe}Config.yaml` + `%USERPROFILE%\.sony_camera_sync\` → import → archive (reuse `MigrateFromLegacyPaths` pattern).

---

## 4. AppDB Sync Design (the login-anywhere requirement)

Reuse existing schema — no new backend except registering slug `camera_connect` (one SQL migration modeled on `LuxeClaw/Extension/.docs/appdb-extension-migration/004_extension_tables.sql`).

| AppDB concept | Camera Connect use |
|---|---|
| GoTrue `signInWithPassword` | Settings → Account: email + password login; session persisted locally |
| `extension_enroll_current_installation` | Each machine enrolled once: `p_extension_key='camera_connect'`, `p_installation_key=<uuid>`, `p_machine_label=<hostname>` |
| `extension_settings` scope=`user` | `profiles`, `shared_config` — all machines of this user |
| scope=`device` | `device_paths` — per-machine folder overrides |
| scope=`tenant` | (optional v2) team-shared preset packs |
| `extension_upsert_setting` w/ `p_expected_revision` | Optimistic concurrency → "changed on another device" conflict dialog |
| `extension_heartbeat_installation` | Agent heartbeat → "Devices" UI shows online machines |

**Go client** (`internal/appdb`): `net/http` against `https://appdb.lengoc.me` — `POST /auth/v1/token?grant_type=password`, `GET/POST /rest/v1/...`, `Authorization: Bearer <access_token>` + `apikey` header. Token refresh via `refresh_token` grant. ~200–300 LOC. **No secrets in repo** — anon key is publishable; reject `sb_secret_` like LNC-Proxy's `validatePublishableKey`.

### rclone.conf vault flow
```
Push (machine with working config):
  read secrets\rclone.conf → vault.seal(passphrase, bytes)
  → upsert user-scope key "rclone_bundle" {format,alg,kdf,iter,salt,iv,data}
Pull (fresh machine):
  login → fetch "rclone_bundle" → vault.open(passphrase) → write secrets\rclone.conf
  → rclone works immediately (refresh tokens are portable across machines)
```
Passphrase: never persisted by default; optional store in Windows Credential Manager (`zalando/go-keyring`). Wrong passphrase = AES-GCM auth failure → re-prompt (same semantics as `cred-vault.js` open()).
Caveat documented: two machines running rclone simultaneously can race Google token refresh — rare and self-healing (rclone retries auth); accepted, same as manual conf copying.

---

## 5. Upload Pipeline v2 (bugfix — replaces today's critical flaws)

Current bugs being fixed (see `../refactor_plan.md` §0): unscoped `rclone move` on whole `base_path`; MTP async-copy race; no mutex/timeout/retry.

```
syncengine writes → <base_path>/_staging/<profile>/   (same volume = instant finalize)
  file settles (size stable ≥2s, handle closed)       → queued in SQLite jobs table
backup worker (single-flight, serialized):
  rclone copy --files-from <job list> _staging → remote:remote_path
  per-file verify (rclone check / lsjson size+hash)
  success → move to final tree (copy mode) OR delete local (free-space mode)
  failure → stays staged, retry w/ backoff; retries persist across restarts/unplug
```

`delete_local_after_upload` now only ever deletes files the app itself staged & verified — never the user's library. MTP path adds post-`CopyHere` settle polling before marking downloaded.

---

## 6. Detection & Multi-Brand

1. WMI `Win32_PnPEntity` by **class** (`PNPClass='PortableDevice'` / service `WUDFWpdFs`) — brand-agnostic; VID/PID table (Sony/Canon/Nikon/Fuji/Panasonic/OM/GoPro/DJI) only for friendly names.
2. Mass Storage: `GetLogicalDrives`+`GetDriveTypeW`+`DCIM|PRIVATE` probe (already generic) — fix device↔drive correlation via PNP DeviceID; unrelated card readers listed as separate selectable sources.
3. MTP roots: enumerate storages → recursive `DCIM` search instead of hardcoded `Storage Media`; per-brand override in config.
4. Extended default file types: `ARW CR2 CR3 NEF NRW RAF ORF RW2 DNG GPR JPG JPEG HEIF HIF` + `MP4 MTS AVCHD MOV LRV MKV` (editable per profile).

**MTP port risk** (only real unknown): (a) `go-ole` → `Shell.Application` COM — same mechanism as Python, preferred; (b) WPD COM API — more correct, higher effort; (c) Python sidecar fallback. Decide in Phase 3 spike (≤1 day).

---

## 7. UI Screens (vanilla TS + Tailwind + engine theme)

Sidebar: **Dashboard** (service status, active profile switcher, cameras live) · **Import** (manual scan, date picker, copy/move) · **Profiles** (CRUD, per-machine paths) · **Backup** (remote status, staging queue, free-space toggle + safety copy) · **Sync** (AppDB login, devices list, vault push/pull) · **History** (SQLite-backed log) · **Logs** · **Settings** · **About/Update**.
Dark default (LRSync palette) + `data-theme="light"` using engine tokens. LN-UI-Engine `src/theme/*.css` imported wholesale; classes used on plain markup.

---

## 8. Release & Update

`internal/update` (LRSync port): GitHub releases → asset scoring → `.part` download → Inno installer detached → installs over same dir (`%LOCALAPPDATA%` config untouched) → relaunch `--minimized`. Version `x.y.z.k` everywhere. CI = adapted `windows-release.yml`. **License: PolyForm Noncommercial 1.0.0.** `.gitignore`: `rclone.exe`, `rclone.conf`, `secrets/`, `config.local.yaml`, `dist/`, `build/`.

---

## 9. Phases

| Phase | Deliverable | Gate |
|---|---|---|
| **0** | Python hotfix (copy-only, scoped files, backup mutex) — optional but recommended NOW | Stops active data-loss path |
| **1** | Repo skeleton: LICENSE, .gitignore, go.mod, agent+UI boot from LRSync patterns, layered config + legacy migration, SQLite schema | Builds + both exes launch |
| **2** | Detection v2 + Mass Storage sync engine + profiles resolution | Sync works on SD card |
| **3** | MTP via go-ole spike → implement or sidecar fallback | MTP file list+download |
| **4** | Upload pipeline v2 (staging, settle, verify, retry, verified-delete) | No-loss guarantees |
| **5** | AppDB client + enrollment + settings sync + vault (rclone.conf) + conflict UX | Config follows login |
| **6** | Full UI panels + theme | Usable daily |
| **7** | Installer + self-update + CI release | `x.y.z.k` published |
| **8** | Testing phase: e2e smoke suite (port LRSync ps1), brand matrix, upgrade/migration test, soak test | Ship checklist green |

**Risks:** MTP COM in Go (mitigated: sidecar fallback) · rclone token-refresh race across machines (accepted, documented) · drive↔device correlation edge cases (USB hubs) · AppDB migration needs owner-run SQL on prod (provide `_sql_editor.sql` file like LNC pattern) · Wails embed constraint (UI bootstrap must stay at repo root).

**Secrets policy:** anon key only in client; rclone.conf never leaves machine unsealed; no Supabase service keys anywhere in repo/app; `.env` never used at runtime (config.yaml only).

**Full request coverage map:** auto-sync on camera connect → Phase 2/3 syncengine; profiles one-click folder routing → Phase 5 config + Phase 6 UI; per-machine folders → device-scope settings + local override; login-anywhere Drive reuse → Phase 5 vault; preset sync for other logged-in users → user/tenant scope settings; bug where videos deleted before upload done → Phase 4 (root fix) + Phase 0 (interim); language fast/light/stable → Go single binary, agent CGO-free; UI simple-but-beautiful → §7 engine theme; self-update preserving config → §8 Inno same-dir + `%LOCALAPPDATA%` config; no leaked keys on public repo → §4 + §8 gitignore + anon-key-only policy + PolyForm license; multi-brand cameras → §6; icon/favicon/installer branding → Phase 7 (`build/windows/icon.ico`+`appicon.png` for UI, `cmd/agent` `.syso` for agent, Inno wizard images).

---

## 9a. Acceptance Criteria & Verification (per phase)

Global commands (run from repo root): `go build ./...`, `go test ./... -count=1`, `go vet ./...`, `wails build -tags wails -platform windows/amd64`, `frontend: npm ci && npm run build`, `scripts/e2e_*.ps1` smoke suite, `scripts/build_installer.ps1`. Phase gate = all listed checks pass on the dev machine.

- **P0:** with `delete_local_after_upload` legacy flag set, `git grep` shows forced `copy`; two overlapping triggers → one rclone at a time (mutex log line); MTP download returns only after size-stable poll.
- **P1:** `CameraConnect.exe --action get-status` prints JSON `ActionEnvelope`; UI launches showing agent-offline overlay; `CameraConnectConfig.yaml` migrated into layered config (backup of legacy file created); SQLite `cache.db` created with schema.
- **P2:** SD card insert → detected within 2s → new files land in `<active_profile.base_path>/<template>`; second run skips all; `sync_mode=move` deletes camera file only after verified local copy; unmatched card readers appear as selectable sources.
- **P3:** ILCE-7RM3/NEX-5R/ZV-E10 over MTP: list + download ≥95% files; no partial files in dest (settle-checked); disconnect mid-copy → clean error, no crash.
- **P4:** copy 4GB video → staging → upload → verify → finalize; `kill -9` mid-upload → restart → job retries automatically; free-space mode deletes ONLY staged+verified files; user's other library files untouched (`rclone` never receives base root).
- **P5:** fresh Windows profile/machine → install → login → passphrase → rclone uploads succeed w/o manual conf; profiles edited on machine A appear on machine B after sync; device-scope path override stays machine-local; tampered blob → AES-GCM failure → re-prompt (no crash); revision conflict → reload prompt.
- **P6:** all panels render; profile switch applies to next cycle; dark/light themes correct; tray menu actions work; single-instance enforced.
- **P7:** `tag v1.0.0.0` → CI builds installer → install over existing dir → settings/rclone.conf/profiles intact → in-app "update available" → one-click → app relaunches at new version.
- **P8:** e2e suite green; brand matrix ≥ Sony MTP+MS + one non-Sony SD; 24h continuous soak no leak/crash; memory report stored in Cortex.

---

## 10. Owner Decisions (2026-09-26 — all resolved)

1. ✅ App identity: keep `Camera Connect` / `CameraConnect.exe`, same dir `D:\Python\projects\Camera Connect`, same repo `github.com/ngojclee/camera-connect`, commit+push to `main` per checkpoint (W6 convention).
2. ✅ MTP "Move" mode: **kept** for MTP too — implement delete via WPD/`InvokeVerb` with post-copy settle verification before delete.
3. ✅ Staging dir: `<base_path>/_staging` (same volume, instant finalize).
4. ✅ AppDB slug `camera_connect`: approved — author SQL migration file for owner to run in Supabase SQL Editor (LNC-Proxy migration pattern).
5. ✅ Phase 0 Python hotfix: **approved & shipped** — copy-only backup, `--files-from` scoping, single-flight lock, MTP settle check.
