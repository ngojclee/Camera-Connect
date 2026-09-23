# Sony Camera Auto-Sync

Ứng dụng Windows tự động đồng bộ ảnh/video từ máy ảnh Sony về PC.

## Tính năng

✅ **Auto-Detection**: Tự động nhận diện máy ảnh Sony khi cắm USB (NEX-5R, ZV-E10, A7R III, ...)  
✅ **Auto-Sync**: Tự động copy ảnh/video mới về PC  
✅ **Folder Templates**: Tùy chỉnh cấu trúc thư mục đích với placeholders `{camera}`, `{year}`, `{month}`, `{day}`  
✅ **Notifications**: Thông báo khi camera kết nối và khi sync xong  
✅ **Background Service**: Chạy ngầm, tự động wake up khi phát hiện camera  
✅ **Smart Queue**: SQLite database track files đã sync, tránh duplicate  

## Cài đặt

### 1. Cài đặt Python dependencies

```bash
pip install -r requirements.txt
```

### 2. Cấu hình

Chỉnh sửa file config (tên dựa theo executable, ví dụ: `CameraConnectConfig.yaml`):

```yaml
destination:
  base_path: "D:/Photos"  # Thư mục đích
  folder_template: "{camera}/{year}/{month}-{day}"  # Template

notifications:
  on_camera_connect: true
  on_copy_complete: "batch"  # "each" hoặc "batch"
```

### 3. Chạy ứng dụng

```bash
python src/main.py
```

## Hướng dẫn sử dụng

1. **Cắm máy ảnh Sony vào PC qua USB**
2. **Set camera sang chế độ MTP** (hoặc Mass Storage)
   - Trên camera: Menu → Setup → USB Connection → MTP
3. **App sẽ tự động:**
   - Nhận diện camera
   - Quét thư mục DCIM
   - Copy files mới về thư mục đích
   - Hiển thị notification khi xong

## Folder Template Placeholders

| Placeholder | Ví dụ | Mô tả |
|-------------|-------|-------|
| `{camera}` | ZV-E10 | Tên model camera |
| `{type}` | Photo / Video | Loại file |
| `{yyyy}` | 2025 | Năm (4 chữ số) |
| `{yy}` | 25 | Năm (2 chữ số) |
| `{mm}` | 01-12 | Tháng (2 chữ số, có leading zero) |
| `{m}` | 1-12 | Tháng (1-2 chữ số, không leading zero) |
| `{dd}` | 01-31 | Ngày (2 chữ số, có leading zero) |
| `{d}` | 1-31 | Ngày (1-2 chữ số, không leading zero) |
| `{date}` | 2025-12-25 | Ngày đầy đủ YYYY-MM-DD |

**Ví dụ:**
- `{camera}/{yyyy}/{mm}-{dd}` → `D:/Photos/ZV-E10/2025/12-25/DSC00001.ARW`
- `{yyyy}-{mm}-{dd}` → `D:/Photos/2025-12-25/DSC00001.ARW` (single folder)
- `{camera}/{yy}{mm}{dd}` → `D:/Photos/ZV-E10/251225/DSC00001.ARW`
- `{yyyy}/{m}/{d}` → `D:/Photos/2025/12/25/DSC00001.ARW` (no leading zeros)

> **Note:** Dấu `/` tạo subfolder. Không có `/` = tên folder đơn.

## Camera hỗ trợ

| Model | Năm | Trạng thái |
|-------|-----|------------|
| NEX-5R | 2012 | ✅ Tested |
| ZV-E10 | 2021 | ✅ Tested |
| A7R III | 2017 | ✅ Tested |
| A7 III | 2018 | ⚠️ Chưa test |
| A7 IV | 2021 | ⚠️ Chưa test |

> Hầu hết các máy Sony từ 2010 trở lại đây đều hỗ trợ MTP và sẽ hoạt động.

## Troubleshooting

### Camera không được nhận diện
1. Kiểm tra camera đã bật chưa
2. Kiểm tra USB mode trên camera (phải là MTP, không phải PC Remote)
3. Thử rút dây và cắm lại
4. Kiểm tra log file: `%USERPROFILE%\.sony_camera_sync\app.log`

### Files không được copy
1. Kiểm tra file type có trong config file (ví dụ: `CameraConnectConfig.yaml`) → `file_types` chưa
2. Kiểm tra quyền ghi vào thư mục đích
3. Kiểm tra database: `%USERPROFILE%\.sony_camera_sync\sync_queue.db`

### Notification không hiện
- Cài đặt: `pip install win10toast`
- Kiểm tra Windows notification settings

## Roadmap

- [ ] System tray UI
- [ ] Settings dialog (GUI)
- [ ] Google Drive sync
- [ ] NAS/SMB sync
- [ ] Auto-delete files on camera after sync
- [ ] PyInstaller build script (.exe)

## License

MIT License

## Credits

Developed for Sony camera users who want seamless photo/video backup workflow.
