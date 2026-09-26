# Camera Connect

Tự động đồng bộ ảnh/video từ máy ảnh về PC — hỗ trợ cả **USB Mass Storage** (thẻ nhớ) và **MTP** (camera qua USB). Background agent + desktop UI, cloud backup qua rclone, đồng bộ cấu hình giữa nhiều máy.

*Auto-import photos & videos from your camera — works with both mass-storage card readers and MTP devices (Sony, Canon, Nikon, Fujifilm, Panasonic, OM System, GoPro, DJI…).*

## Features

- **Auto-detect** — plug in camera/SD card → sync starts (mass storage preferred, MTP fallback)
- **Profiles** — per-workflow destinations + folder templates (`{camera}/{yyyy}/{yyyy}-{mm}-{dd}`…), machine-specific path overrides
- **Crash-safe copies** — `.part` + fsync + rename; MTP waits for transfer to settle before reporting done
- **Idempotent** — existing files skipped (unique camera file names), no re-copying, no overwrite popups
- **Cloud backup** — staged upload via rclone (`--files-from` scoped to the batch), verified with `lsjson` before files leave staging
- **AppDB sync** *(optional)* — sign in once on any machine to pull profiles + your encrypted `rclone.conf` vault (AES-256-GCM, sealed client-side)
- **Self-update** — in-app update checks against GitHub releases

## Install

Download `CameraConnectSetup-vX.Y.Z.K-windows-amd64.exe` from [Releases](https://github.com/ngojclee/camera-connect/releases), or build from source:

```powershell
# prerequisites: Go 1.22+, Node 20+
./scripts/build_windows.ps1 -Version 3.0.0.1
# outputs: build/bin/CameraConnect.exe (UI) + CameraConnectAgent.exe
```

The installer adds the agent to Windows startup and registers single-instance behavior. Config lives in `%LOCALAPPDATA%\CameraConnect\` — `config.yaml` (shared, syncable) + `config.local.yaml` (machine paths, never synced).

## CLI (useful while debugging)

```powershell
CameraConnect.exe --action get-status
CameraConnect.exe --action list-cameras
CameraConnect.exe --action scan-now            # force a sync pass
CameraConnect.exe --action set-profile --payload '{"profile_id":"studio"}'
CameraConnect.exe --action subscribe-logs
```

## Configuration

```yaml
# config.yaml — shared between machines
active_profile: default
profiles:
  - id: default
    name: Default
    photo_template: "{camera}/{yyyy}/{yyyy}-{mm}-{dd}"
    backup:
      enabled: true
      remote_name: gdrive
      remote_path: Backup/Photos
general:
  scan_mode: once          # once | continuous
  sync_mode: copy          # copy | move (move = mass storage only)
  auto_sync: true
```

```yaml
# config.local.yaml — this machine only
profile_paths:
  default: D:/Photos/Incoming
```

## Security notes

- `rclone.conf` and the AppDB session live under `%LOCALAPPDATA%\CameraConnect\secrets` (0600).
- Vault format is byte-compatible with the LNC Proxy `cred-vault.js` (PBKDF2-SHA256 ×150k + AES-256-GCM) — the server only stores ciphertext.
- The bundled AppDB anon key is public-by-design (Supabase RLS); `sb_secret_*` keys are rejected.

## License

[PolyForm Noncommercial 1.0.0](LICENSE) — free for personal/non-commercial use. Commercial use requires a separate license.
