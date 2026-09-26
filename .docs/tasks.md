# Sony Camera Auto-Sync - Tasks

## Phase 1: Core Infrastructure ✅ COMPLETED
- [x] Setup project structure và virtual environment
- [x] Cài đặt dependencies (không cần libgphoto2, dùng MTP)
- [x] Implement USB camera detector (`detector.py`)
- [x] Implement MTP handler (`mtp_handler.py`)
- [x] Test với camera thật ✅ (ZV-E10 tested successfully)

## Phase 2: Sync Engine ✅ COMPLETED
- [x] Setup SQLite database cho sync queue
- [x] Implement queue manager (`queue_manager.py`)
- [x] Implement config manager (`config.py`)
- [x] Implement main sync loop (`main.py`)
- [x] Test với real camera ✅ (7 files synced successfully)
- [x] Fix MTP folder navigation (Storage Media instead of DCIM)
- [x] Fix date extraction from folder path (Sony organizes by date)

## Phase 3: User Interface ✅ COMPLETED
- [x] Implement desktop GUI app (`tray_app.py`)
- [x] Three-tab interface (Dashboard, Manual Import, Settings)
- [x] Real-time activity log
- [x] Settings persistence
- [x] Fix COM threading issues (Connect-Work-Disconnect pattern)
- [x] Fix Settings tab layout (auto-resize canvas)

## Phase 4: Manual Sync Feature ✅ COMPLETED
- [x] Implement "Manual Import by Date"
  - [x] Scan `Storage Media` for date folders (YYYY-MM-DD pattern)
  - [x] UI with camera selection dropdown
  - [x] Checkbox list for date selection
  - [x] Import selected dates with progress feedback
  - [x] Thread-safe MTP access

## Phase 5: Copy/Move Modes ✅ COMPLETED
- [x] Implement Copy mode (default, safe)
- [x] Implement Move mode (copy + delete)
- [x] Add `delete_file()` to MTPHandler
- [x] Integrate delete logic in sync loop
- [x] Document Windows confirmation dialog limitation

## Phase 6: Mass Storage & Dual Slots ✅ COMPLETED
- [x] Implement Mass Storage Handler (faster than MTP)
- [x] Auto-detect drive letters for connected cameras
- [x] Dual Slot support (e.g. A7R III G: and H:)
- [x] Priority detection: Mass Storage > MTP

## Phase 7: Rebranding & Cloud Backup ✅ COMPLETED
- [x] **Rebranding**: Renamed application to "Camera Connect" (Brand Agnostic)
- [x] **Cloud Backup**: Integrated Rclone for one-way sync to Google Drive
- [x] **Portable Config**: Rclone configuration stored locally with executable
- [x] **Free Up Space**: Option to delete local files after successful upload (Move workflow)

## Phase 8: System Enhancements ✅ COMPLETED
- [x] **Enhanced Startup**:
    - "Start with Windows" option
    - "Start Minimized" to System Tray option
    - "Auto-Start Sync Service" option
- [x] **Dynamic Tray**: Tray menu with Start/Stop controls and status
- [x] **Packaging**: 
    - Updated `build.bat` and `spec` file
    - Bundled `rclone.exe` for portable deployment

## Phase 9: Future Roadmap
- [ ] Add retry logic for failed downloads
- [ ] Progress bars for large file transfers
- [ ] File verification (hash check) before delete
- [ ] Silent delete for Move mode (requires low-level WPD API)
- [ ] Add installer/uninstaller (Currently Portable ZIP)

## Known Limitations:
- **MTP Delete**: May show Windows confirmation dialog (user must check "Do this for all")
- **Folder Structure**: Assumes standard DCIM or Storage Media organization
- **No Progress UI**: Large transfers show no progress bar (only log messages)

## Recent Fixes (2025-12-26):
- Implemented robust Startup Logic (Winshell shortcut)
- Integrated Rclone for reliable Cloud Sync
- Refactored Settings UI for better organization
- Renamed project components to "Camera Connect"
- Fixed Rclone config path for portability


---

## Go Refactor (2026-09-26)
New refactor plan + tasks live in [.docs/go-refactor/](./go-refactor/) — see `plan.md` and `task.md` there. Audit findings in [.docs/refactor_plan.md](./refactor_plan.md).
