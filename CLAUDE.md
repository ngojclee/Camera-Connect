# Camera Connect - Technical Documentation
 
 ## 🎯 Project Overview
 Auto-sync application for digital cameras (formerly Sony Camera Auto-Sync) supporting both MTP (Media Transfer Protocol) and Mass Storage modes. Features automatic backup to cloud (Google Drive via Rclone), flexible file organization, and background service operation.

---

## 🏗️ Architecture

### Core Components
1. **Camera Detection** (`src/camera/detector.py`)
   - WMI-based USB device monitoring
   - PID-based camera identification
   - Automatic MTP name resolution (ILCE codes)

2. **MTP Handler** (`src/camera/mtp_handler.py`)
   - Shell.Application COM interface
   - File listing and download via MTP
   - ILCE model name mapping

3. **Mass Storage Handler** (`src/camera/mass_storage_handler.py`)
   - Direct filesystem access
   - Faster than MTP
   - Supports file deletion (Move mode)

4. **Sync Manager** (`src/main.py`)
   - Auto-detection: Mass Storage → MTP fallback
   - Smart skip logic (file existence-based)
   - Per-camera independent sync loops

5. **Queue Manager** (`src/sync/queue_manager.py`)
    - SQLite database for sync state
    - Database is CACHE only, filesystem is source of truth

6. **Cloud Backup Manager** (`src/backup_manager.py`)
    - Wrapper for `rclone` binary
    - Handles portable configuration (`rclone.conf`)
    - One-way sync (Copy/Move to Cloud)

---

## 🔑 Key Design Decisions

### 1. **Skip Logic: File Existence Only**
```python
if dest_path.exists():
    skip  # File name is unique, no need for date/size comparison
```

**Why:**
- File names are unique (camera counter + extension)
- Folder structure includes date (`{camera}/{yyyy}/{yyyy}-{mm}-{dd}`)
- File mtime changes every download (unreliable)
- Size may be 0 for MTP (Windows API limitation)

**Result:** Simple, fast, reliable!

---

### 2. **Auto-Detection: Mass Storage → MTP**
```python
# Try Mass Storage first (faster)
drive = find_camera_drive(model_name)
if drive:
    use MassStorageHandler
else:
    use MTPHandler  # Fallback
```

**Why:**
- Mass Storage is 10x faster
- Supports file deletion (Move mode)
- MTP as fallback for compatibility

---

### 3. **Per-Camera Running State**
```python
camera_running = True  # Per-camera flag
while self.running and camera_running:
    ...
    if scan_mode == "once":
        camera_running = False  # Only stop THIS camera
```

**Why:**
- Multiple cameras can sync independently
- Scan Once mode works per-camera
- Plug/unplug any camera anytime

---

### 4. **Universal Camera Support**
```python
# Detector: Generic PID detection
# MTP: Get actual name from Windows
actual_name = mtp_handler.model_name  # e.g., "ILCE-7RM3"
```

**Why:**
- No need to add new cameras to config
- Works with ANY Sony camera (and extensible to others)
- Uses official ILCE names

---

### 5. **Portable Rclone Integration**
```python
# backup_manager.py
# Use --config flag to force local config file
cmd = [rclone, "copy", ..., "--config", "rclone.conf"]
```

**Why:**
- Zero-installation needed for end user
- Configuration travels with the app (portable)
- Robust one-way sync logic provided by Rclone executable

---

## 📋 Configuration (Dynamic Naming)

Config file is named based on executable: `{exe_name}Config.yaml`
- `CameraConnect.exe` → `CameraConnectConfig.yaml`
- `MyApp.exe` → `MyAppConfig.yaml`

### Key Settings

```yaml
general:
  start_with_windows: true   # Create shortcut in startup
  startup_minimized: true    # Start to tray
  startup_auto_sync: false   # Auto-start service?
  scan_mode: once            # "once" or "continuous"
  poll_interval: 3           # Seconds between scans (continuous mode)
  sync_mode: copy            # "copy" or "move"
  overwrite_existing: true   # Auto-overwrite without popup

destination:
  base_path: F:/2.Studio/Products_LuxeClaw
  folder_template: '{camera}/{yyyy}/{yyyy}-{mm}-{dd}'

backup:
  enabled: true
  remote_name: gdrive
  remote_path: Backup/Photos
  delete_local_after_upload: false # Free up space logic

file_types:
  photos: [ARW, JPG, JPEG, HEIF]
  videos: [MP4, MTS, AVCHD]
```

---

## 🔄 Sync Modes

### Scan Once
- Scan 1 time per camera connection
- Auto-stop after sync complete
- Perfect for: Quick import when plugging in camera

### Continuous
- Loop every `poll_interval` seconds
- Detect new files automatically
- Perfect for: Tethered shooting

---

## 🐛 Known Issues & Solutions

### Issue 1: MTP File Size = 0
**Problem:** `item.Size` returns 0 for MTP files

**Solution:** Don't use size for skip logic, use file existence only

---

### Issue 2: MTP Overwrite Popup
**Problem:** Windows shows "Replace file?" dialog

**Solution:** Use CopyHere flags `9748`:
```python
# 4 = No progress dialog
# 16 = Yes to all
# 512 = Don't confirm mkdir
# 1024 = Don't show error UI
# 8192 = No confirmation dialogs
copy_flags = 9748
```

---

### Issue 3: Date Mismatch After Download
**Problem:** Local file mtime ≠ camera file date (always mismatch)

**Solution:** Don't compare dates! File name is unique enough.

---

## 🧪 Testing Checklist

### MTP Mode
- [x] Detect camera (ILCE name)
- [x] List files
- [x] Download new files
- [x] Skip existing files (no popup)
- [x] Scan Once mode
- [x] Continuous mode
- [x] Multiple cameras

### Mass Storage Mode
- [ ] Detect SD card drive
- [ ] List files
- [ ] Copy files (faster than MTP)
- [ ] Move files (delete after copy)
- [ ] Skip existing files

---

## 📁 Project Structure

```
Sony Camera/
├── src/
│   ├── main.py              # Main sync logic
│   ├── config.py            # Config loader
│   ├── tray_app.py          # GUI (tkinter)
│   ├── camera/
│   │   ├── detector.py      # USB device detection
│   │   ├── mtp_handler.py   # MTP operations
│   │   └── mass_storage_handler.py  # SD card operations
│   └── sync/
│       └── queue_manager.py # SQLite sync state
├── CameraConnectConfig.yaml # User configuration (named after exe)
├── run.bat                  # Launcher
└── requirements.txt         # Dependencies
```

---

## 🚀 Next Steps: Mass Storage Testing

### Test Plan
1. **Setup:**
   - Change camera to Mass Storage mode (USB settings)
   - Plug in camera
   - Verify drive letter detected

2. **Copy Mode:**
   - Files copied to destination
   - Original files remain on camera
   - Skip existing files

3. **Move Mode:**
   - Files copied to destination
   - Original files deleted from camera
   - Verify deletion successful

4. **Edge Cases:**
   - SD card full
   - Write-protected files
   - Interrupted transfer

---

## 💡 Tips & Tricks

### Enable Debug Logs
```python
logger.setLevel(logging.DEBUG)
```

### Reset Database
```bash
del C:\Users\<user>\.sony_camera_sync\sync_queue.db
```

### Check MTP Device Name
```python
# In mtp_test.py
for item in computer.Items():
    print(item.Name)  # Shows actual MTP name
```

---

## 📊 Performance Metrics

| Mode | Speed | Reliability | Delete Support |
|------|-------|-------------|----------------|
| Mass Storage | ⚡⚡⚡ Fast | ✅ Excellent | ✅ Yes |
| MTP | 🐌 Slow | ⚠️ Good | ❌ Limited |

**Recommendation:** Use Mass Storage when possible!

---

## 🎉 Success Criteria

✅ **Achieved:**
- Universal camera support (any Sony camera)
- Smart skip logic (no re-downloads)
- No overwrite popups
- Per-camera independent sync
- Scan Once & Continuous modes
- Auto-detect MTP vs Mass Storage

🔜 **Next:**
- Mass Storage mode testing
- Move mode with file deletion
- Performance optimization

---

*Last Updated: 2025-12-26*
*Status: MTP Mode Complete ✅ | Mass Storage Mode Complete ✅ | Dual Slot Support ✅*

---

## 🧠 Technical Memory / Context

### 1. Configuration Access
- **Rule:** `config.py` uses direct dictionary access.
- **Critical:** Always match keys exactly with config file (e.g., `CameraConnectConfig.yaml`).
  - ❌ `config.get("paths.photo_template")` (Old/Wrong)
  - ✅ `config.get("destination.photo_template")` (Correct)

### 2. Windows UI Font Rendering
- **Issue:** Standard Tkinter Text widgets (using `Consolas` etc.) render Unicode Emojis as monochrome/black & white on Windows.
- **Fix:** Use `font=('Segoe UI Emoji', 10)` or `('Segoe UI', 10)` in `scrolledtext.ScrolledText` widgets to enforce Windows Color Emoji rendering.

### 3. Mass Storage & Dual Slots
- **Detection:** Sony cameras mount each card as a separate Removable Drive.
- **Constraint:** `GetDriveTypeW` + checking for `DCIM` or `PRIVATE` folders is the reliable way to identify them.
- **Handling:**
  - **Auto-Sync:** Must loop through `find_all_camera_drives()` results.
  - **Manual Import:** Best practice is to list drives **individually** (`PID|DriveLetter`) in the UI dropdown to give users explicit control and simplify backend logic.

### 4. Service Conflict Management
- **Issue:** Running Manual Scan while Auto-Service is active causes race conditions (files locked, double copy).
- **Solution:** Enforce a "Stop & Wait" workflow.
  - 1. Prompt User -> 2. `toggle_service()` -> 3. Poll `self.app.running` (Wait Loop) -> 4. Execute Scan.

