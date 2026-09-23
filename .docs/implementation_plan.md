# Sony Camera Auto-Sync App - Implementation Plan

## Mục tiêu
Xây dựng ứng dụng Windows chạy nền (system tray) để:
1. **Tự động nhận diện** máy ảnh Sony khi cắm USB (cả đời cũ lẫn mới)
2. **Tự động copy** ảnh/video về PC theo cấu hình folder đã định sẵn
3. **Thông báo** khi copy xong từng file hoặc batch
4. Đồng bộ lên Google Drive và/hoặc NAS (optional)

## Camera hỗ trợ

| Model | Chế độ USB | Cách nhận diện | Cách lấy file |
|-------|------------|----------------|---------------|
| NEX-5R | MTP | USB VID/PID + MTP device class | Poll thẻ nhớ, so sánh danh sách file mới |
| ZV-E10 | PC Remote / MTP | USB VID/PID | PTP events hoặc poll |
| A7R III | PC Remote / MTP | USB VID/PID | PTP events hoặc poll |

> [!NOTE]
> **Về auto-detection:** App sẽ tự nhận diện model camera qua USB Vendor ID (Sony = 0x054C) và Product ID. Không cần chọn trước chế độ camera cũ/mới - app sẽ tự detect và chọn protocol phù hợp.

## Tính năng chính

### 1. Background Service (Chạy ngầm)
- Khởi động cùng Windows (optional)
- System tray icon với status indicator
- Tự động wake up khi phát hiện Sony camera cắm vào

### 2. Auto-Detection & Copy
- Nhận diện camera ngay khi cắm USB
- Poll thư mục DCIM trên camera mỗi 2-5 giây
- Phát hiện file mới (so sánh với database đã copy)
- Copy file mới về destination folder

### 3. Folder Naming Templates
Hỗ trợ các placeholder cho tên thư mục đích:
```
{year}     → 2025
{month}    → 12
{day}      → 25
{date}     → 2025-12-25
{camera}   → NEX-5R, ZV-E10, A7R3
{type}     → Photo, Video
```
**Ví dụ cấu hình:**
- Base path: `D:\Photos`
- Template: `{camera}/{year}/{month}-{day}`
- Kết quả: `D:\Photos\ZV-E10\2025\12-25\DSC00001.ARW`

### 4. Notifications
- Toast notification khi camera connected
- Toast notification khi copy xong (có option: mỗi file hoặc mỗi batch)
- Sound notification (optional)
- Summary notification khi rút camera: "Đã copy 15 ảnh, 2 video"

---

## Proposed Changes

### Phase 1: Core Infrastructure

#### [NEW] [detector.py](file:///d:/Python/projects/Sony%20Camera/src/camera/detector.py)
- Sử dụng `wmi` để detect USB device khi cắm/rút máy ảnh
- Nhận diện Sony camera qua VID/PID
- Emit events: `camera_connected`, `camera_disconnected`

#### [NEW] [gphoto_handler.py](file:///d:/Python/projects/Sony%20Camera/src/camera/gphoto_handler.py)
- Wrapper cho `python-gphoto2`
- Methods: `connect()`, `capture_image()`, `download_file()`, `list_files()`
- Xử lý PTP events để biết khi nào có file mới

#### [NEW] [mtp_handler.py](file:///d:/Python/projects/Sony%20Camera/src/camera/mtp_handler.py)
- Fallback cho camera cũ (NEX-5R) không hỗ trợ gphoto2 đầy đủ
- Dùng `comtypes` để truy cập Windows MTP API
- Chỉ hỗ trợ download, không hỗ trợ trigger capture

---

### Phase 2: Sync Engine

#### [NEW] [queue_manager.py](file:///d:/Python/projects/Sony%20Camera/src/sync/queue_manager.py)
- SQLite database để track trạng thái sync
- Bảng: `files(id, camera_path, local_path, status, created_at, uploaded_at)`
- Status: `pending`, `downloading`, `uploading`, `completed`, `failed`

#### [NEW] [drive_uploader.py](file:///d:/Python/projects/Sony%20Camera/src/sync/drive_uploader.py)
- Sử dụng `PyDrive2` cho Google Drive API
- OAuth2 authentication flow
- Upload với progress callback
- Tạo folder structure theo ngày: `SonySync/2025-12-25/`

#### [NEW] [nas_uploader.py](file:///d:/Python/projects/Sony%20Camera/src/sync/nas_uploader.py)
- Sử dụng `smbprotocol` cho SMB/CIFS
- Config: server, share, username, password
- Upload với retry logic

---

### Phase 3: User Interface

#### [NEW] [tray_app.py](file:///d:/Python/projects/Sony%20Camera/src/ui/tray_app.py)
- System tray icon với `pystray`
- Menu: Status, Settings, Manual Sync, Exit
- Notifications khi có file mới được sync

#### [NEW] [settings_dialog.py](file:///d:/Python/projects/Sony%20Camera/src/ui/settings_dialog.py)
- Tkinter dialog cho cấu hình
- Tabs: Camera, Google Drive, NAS, General

#### [NEW] [config.yaml](file:///d:/Python/projects/Sony%20Camera/config.yaml)
```yaml
# Folder structure settings
destination:
  base_path: "D:/Photos"
  folder_template: "{camera}/{year}/{month}-{day}"
  # Available: {year}, {month}, {day}, {date}, {camera}, {type}

# File type filters
file_types:
  photos: [ARW, JPG, JPEG, HEIF]
  videos: [MP4, MTS, AVCHD]

# Notification settings
notifications:
  on_camera_connect: true
  on_copy_complete: "batch"  # "each" = mỗi file, "batch" = khi xong hết
  play_sound: true
  
# Camera detection (auto-populated khi nhận được camera mới)
cameras:
  - name: "NEX-5R"
    usb_pid: "0x0946"
    protocol: "mtp"
  - name: "ZV-E10"
    usb_pid: "0x0D4A"
    protocol: "ptp"
  - name: "A7R III"
    usb_pid: "0x0C3B"  
    protocol: "ptp"

# Cloud sync (optional)
google_drive:
  enabled: false
  folder_id: ""
  
nas:
  enabled: false
  server: ""
  share: ""
  
# General
general:
  start_with_windows: true
  minimize_to_tray: true
  delete_after_sync: false  # Xóa file trên camera sau khi copy
```

---

### Phase 4: Entry Point & Packaging

#### [NEW] [main.py](file:///d:/Python/projects/Sony%20Camera/src/main.py)
- Application entry point
- Initialize all components
- Handle graceful shutdown

#### [NEW] [requirements.txt](file:///d:/Python/projects/Sony%20Camera/requirements.txt)
```
python-gphoto2>=2.3.4
PyDrive2>=1.15.0
smbprotocol>=1.10.0
pystray>=0.19.0
Pillow>=10.0.0
PyYAML>=6.0
wmi>=1.5.1
```

#### [NEW] [build.py](file:///d:/Python/projects/Sony%20Camera/build.py)
- PyInstaller script để đóng gói thành .exe
- Include gphoto2 DLLs

---

## Verification Plan

### Automated Tests

> [!IMPORTANT]
> Do tính chất phần cứng của project (cần camera thật), automated tests sẽ tập trung vào unit tests cho logic không phụ thuộc camera.

#### Unit Tests
```bash
# Chạy toàn bộ tests
pytest tests/ -v

# Test từng module
pytest tests/test_queue_manager.py -v
pytest tests/test_drive_uploader.py -v  # Mock Google API
pytest tests/test_nas_uploader.py -v    # Mock SMB
```

### Manual Verification (Yêu cầu camera thật)

#### Test 1: Camera Detection
1. Cắm máy ảnh Sony vào PC qua USB
2. Set camera sang mode "PC Remote" 
3. Chạy `python src/main.py`
4. **Expected:** Notification "Camera connected: Sony [Model]"

#### Test 2: Photo Sync
1. Đảm bảo camera đã kết nối
2. Bấm chụp ảnh trên máy ảnh
3. **Expected:** File ảnh xuất hiện trong `~/SonySync/[date]/` trong vòng 5 giây

#### Test 3: Video Sync  
1. Quay video ~10 giây rồi stop
2. **Expected:** File video được download sau khi stop (có thể mất 10-30 giây tùy dung lượng)

#### Test 4: Google Drive Upload
1. Cấu hình Google Drive trong Settings
2. Chụp 1 ảnh
3. **Expected:** File xuất hiện trên Google Drive trong folder `SonySync/[date]/`

#### Test 5: NEX-5R Fallback Mode
1. Cắm NEX-5R (set USB mode: MTP)
2. **Expected:** App detect camera, cho phép browse và sync files thủ công từ thẻ nhớ

---

## Rủi ro & Giải pháp

| Rủi ro | Giải pháp |
|--------|-----------|
| libgphoto2 khó cài trên Windows | Cung cấp pre-built binary hoặc hướng dẫn MSYS2 |
| NEX-5R không hỗ trợ PC Remote | Dùng MTP mode, chấp nhận chỉ download manual |
| Video file quá lớn (4K) | Queue system với resume support |
| OAuth2 phức tạp | Desktop app flow + token refresh |

---

## Timeline dự kiến

- **Week 1:** Phase 1 - Camera detection & gphoto2 handler
- **Week 2:** Phase 2 - Sync engine & uploaders  
- **Week 3:** Phase 3 - UI & configuration
- **Week 4:** Phase 4 - Packaging, testing, documentation
