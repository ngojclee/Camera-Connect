import logging
import threading
import time
import re
from typing import Dict, List, Union
from datetime import datetime
import pythoncom
from pathlib import Path

from config import Config
from camera.detector import CameraDetector
from camera.mtp_handler import MTPHandler
from camera.mass_storage_handler import MassStorageHandler, find_camera_drive
from sync.queue_manager import QueueManager, SyncStatus
from backup_manager import CloudBackupManager

logger = logging.getLogger(__name__)

class CameraSyncCore:
    """Main application class"""
    
    def __init__(self, config=None):
        self.config = config or Config()
        self.queue = QueueManager()
        self.detector = CameraDetector()
        self.backup_mgr = CloudBackupManager(self.config)
        
        # We no longer cache handlers globally to avoid COM threading issues.
        # Handlers are created on-demand in their respective threads.
        
        # Keep track of sync threads
        self.sync_threads: Dict[str, threading.Thread] = {}
        self.running = False
        self.notification_callback = None

        # Files downloaded this run — scoped input for cloud backup (hotfix P0)
        self._pending_upload = []
        
    def start(self):
        """Start the application"""
        logger.info("=" * 60)
        logger.info("Camera Connect Service Started")
        logger.info("=" * 60)
        
        self.running = True
        
        # Setup camera detector callbacks
        self.detector.set_callbacks(
            on_connect=self._on_camera_connected,
            on_disconnect=self._on_camera_disconnected
        )
        
        # Start detector
        self.detector.start()
        
        logger.info("Waiting for cameras...")
        logger.info(f"Destination: {self.config.get('destination.base_path')}")
        # Log new granular templates
        logger.info(f"📸 Photo Template: {self.config.get('destination.photo_template')}")
        logger.info(f"🎥 Video Template: {self.config.get('destination.video_template')}")
        
    def stop(self):
        """Stop the application"""
        logger.info("Stopping application...")
        self.running = False
        
        # Stop detector
        self.detector.stop()
        
        logger.info("Application stopped")
        
    def _on_camera_connected(self, device_id: str, model_name: str):
        """Called when camera is connected (runs in Detector thread)"""
        
        # Get actual MTP device name by doing a quick connect
        actual_name = model_name  # Default to PID-based name
        try:
            import pythoncom
            pythoncom.CoInitialize()
            
            # Quick MTP connect to get real name
            temp_handler = MTPHandler(device_id, model_name)
            if temp_handler.connect():
                actual_name = temp_handler.model_name  # Get ILCE name
                temp_handler.disconnect()
                logger.info(f"✅ Detected camera: {actual_name}")
            else:
                logger.debug(f"Could not get MTP name, using: {model_name}")
        except Exception as e:
            logger.debug(f"Error getting MTP name: {e}")
        
        logger.info(f"📷 Camera connected: {actual_name}")
        
        # Show notification
        if self.config.get("notifications.on_camera_connect"):
            self._show_notification(
                f"Camera Connected",
                f"{actual_name} is ready to sync"
            )
        
        # Start sync loop ONLY if Auto-Sync Service is globally running
        if self.running:
            if device_id not in self.sync_threads or not self.sync_threads[device_id].is_alive():
                sync_thread = threading.Thread(
                    target=self._sync_loop,
                    args=(device_id, actual_name),  # Use actual name
                    daemon=True,
                    name=f"Sync-{actual_name}"
                )
                sync_thread.start()
                self.sync_threads[device_id] = sync_thread
        else:
            logger.info("Service is OFF: Skipping auto-sync start for detected camera.")
            
    def _on_camera_disconnected(self, device_id: str, model_name: str):
        """Called when camera is disconnected"""
        logger.info(f"📷 Camera disconnected: {model_name}")
        
        # Thread will exit when it fails to find device or sees is_alive checks
        if device_id in self.sync_threads:
            del self.sync_threads[device_id]
            
        # Show summary notification
        stats = self.queue.get_stats(model_name)
        if stats.get("completed", 0) > 0:
            self._show_notification(
                f"{model_name} Disconnected",
                f"Synced {stats['completed']} files"
            )
            
    def _sync_loop(self, device_id: str, model_name: str):
        """
        Main sync loop for a camera.
        Auto-detects MTP vs Mass Storage and uses appropriate handler.
        """
        import traceback
        
        try:
            pythoncom.CoInitialize()
        except:
            pass
            
        logger.info(f"Starting sync loop for {model_name}")
        files_synced_batch = 0
        
        # Per-camera running flag (independent of global self.running)
        camera_running = True
        
        while self.running and camera_running:
            try:
                # Get latest poll interval
                poll_interval = self.config.get("general.poll_interval", 3)
                
                # Check if device still connected
                connected_cams = self.detector.get_connected_cameras()
                if device_id not in connected_cams:
                    logger.info(f"Device {model_name} no longer detected. Stopping loop.")
                    break
                
                # ---------------- REFACTORED LOOP (Dual Slot Support) ----------------
                handlers = []
                from camera.mass_storage_handler import find_all_camera_drives
                
                # A. Try Mass Storage
                try:
                    drives = find_all_camera_drives()
                    if drives:
                        for d in drives:
                            handlers.append(MassStorageHandler(device_id, model_name, d))
                            logger.info(f"� Found drive target: {d}")
                except Exception as e:
                    logger.error(f"Error scanning drives: {e}")

                # B. Try MTP (Fallback if no drives found)
                if not handlers:
                     handlers.append(MTPHandler(device_id, model_name))

                any_connected = False
                
                # Check connection and Sync
                for handler in handlers:
                    if not self.running or not camera_running: break
                    
                    try:
                        if handler.connect():
                            any_connected = True
                            mode = "Mass Storage" if "MassStorage" in str(type(handler)) else "MTP"
                            
                            if mode == "Mass Storage":
                                logger.info(f"📁 Using Mass Storage: {handler.drive_letter}")
                            else:
                                logger.info("📱 Using MTP mode")

                            # --- SYNC ---
                            count = self._perform_sync_on_handler(handler, model_name, mode)
                            files_synced_batch += count
                            
                            handler.disconnect()
                    except Exception as e:
                         logger.error(f"Handler error: {e}")
                
                if not any_connected:
                    logger.warning(f"Could not connect to {model_name} (Retrying...)")
                    time.sleep(poll_interval)
                    continue

                # Notification batch
                if files_synced_batch > 0:
                    if self.config.get("notifications.on_copy_complete") == "batch":
                        self._show_notification(f"Sync Update", f"Downloaded {files_synced_batch} new files from {model_name}")
                    
                    # Trigger Cloud Backup
                    self._trigger_backup_if_enabled()
                    
                    files_synced_batch = 0
                
                # Check scan mode
                scan_mode = self.config.get("general.scan_mode", "continuous")
                if scan_mode == "once":
                    logger.info(f"🛑 Scan Once mode - Stopping after sync")
                    camera_running = False
                    break
                    
                # Sleep before next poll
                logger.debug(f"Sleeping {poll_interval}s before next scan...")
                time.sleep(poll_interval)
                
            except Exception as e:
                logger.error(f"CRITICAL ERROR in sync loop: {e}")
                logger.error(f"Full traceback: {traceback.format_exc()}")
                time.sleep(poll_interval)  # Sleep before retry
            
        logger.info(f"Sync loop ended for {model_name}")

    def _get_dest_path(self, mtp_file, model_name):
        file_date = mtp_file.date_modified.strftime("%Y-%m-%d")
        file_type = self.config.get_file_type(mtp_file.name)
        dest_folder = self.config.get_destination_path(model_name, file_date, file_type)
        return dest_folder / mtp_file.name

    def _download_file(self, handler, mtp_file, model_name, connection_mode="MTP"):
        dest_path = self._get_dest_path(mtp_file, model_name)
        overwrite = self.config.get("general.overwrite_existing", True)
        logger.info(f"Downloading {mtp_file.name}...")
        
        if handler.download_file(mtp_file, dest_path, overwrite):
            logger.info(f"✅ Downloaded: {mtp_file.name}")

            # Mark as completed in database
            self.queue.mark_completed(mtp_file.path, model_name)
            self._pending_upload.append(dest_path)
            
            # Check Move Mode
            sync_mode = self.config.get("general.sync_mode")
            if sync_mode == "move":
                # Warn if using Move with MTP
                if connection_mode == "MTP":
                    logger.warning(f"⚠️ Move mode with MTP may require confirmation dialog")
                
                if handler.delete_file(mtp_file):
                    logger.info(f"🗑️ Deleted from camera: {mtp_file.name}")
                else:
                    logger.warning(f"⚠️ Could not delete {mtp_file.name}")

            if self.config.get("notifications.on_copy_complete") == "each":
                self._show_notification("File Synced", mtp_file.name)
        else:
            logger.error(f"❌ Failed: {mtp_file.name}")
            return False
        return True

    def _perform_sync_on_handler(self, handler, model_name, connection_mode) -> int:
        """Helper to sync files from a connected handler"""
        import traceback 
        synced_count = 0
        folder_path = "DCIM" if connection_mode == "Mass Storage" else "Storage Media"
        drive_info = getattr(handler, 'drive_letter', 'MTP')
        
        try:
            logger.info(f"🔍 Scanning {model_name} ({drive_info})...")
            
            # 1. List files
            camera_files = handler.list_files(folder_path)
            logger.debug(f"Found {len(camera_files)} files")
            
            # 2. Filter
            valid_files = [f for f in camera_files if self.config.is_file_type_allowed(f.name)]
            
            if not valid_files:
                logger.info(f"📂 No new files found on {drive_info}")
                return 0

            # 3. Process
            files_to_download = []
            skipped_by_date = {}

            for mtp_file in valid_files:
                if not self.running: break
                
                dest_path = self._get_dest_path(mtp_file, model_name)
                
                # Check Local Existence
                if dest_path.exists():
                     # Already exists on disk
                     # Update DB if missing
                     if not self.queue.is_file_synced(mtp_file.path, model_name):
                         self.queue.add_file(mtp_file.path, str(dest_path), mtp_file.size, model_name)
                         self.queue.mark_completed(mtp_file.path, model_name)
                     
                     fdate = mtp_file.date_modified.strftime("%Y-%m-%d")
                     skipped_by_date[fdate] = skipped_by_date.get(fdate, 0) + 1
                else:
                     # Does not exist - Download
                     # But check DB just in case (stale record)
                     if self.queue.is_file_synced(mtp_file.path, model_name):
                         logger.warning(f"⚠️ DB says synced but file missing: {mtp_file.name}")
                     files_to_download.append(mtp_file)

            # Summary
            skipped_sum = sum(skipped_by_date.values())
            if skipped_sum > 0:
                summary = ", ".join([f"{d}: {c}" for d,c in sorted(skipped_by_date.items())])
                logger.info(f"📂 Skipped {skipped_sum} existing files")

            # Download
            if files_to_download:
                logger.info(f"📥 Downloading {len(files_to_download)} new files from {drive_info}")
                for f in files_to_download:
                    if not self.running: break
                    if self._download_file(handler, f, model_name, connection_mode):
                        synced_count += 1
            else:
                logger.info(f"✅ All files up to date on {drive_info}")
                
        except Exception as e:
            logger.error(f"Sync error on {drive_info}: {e}")
            logger.debug(traceback.format_exc())
            
        return synced_count


    # --- HELPER METHODS FOR UI (MANUAL IMPORT) ---

    def is_syncing(self, device_id: str) -> bool:
        """Check if a specific device is currently running auto-sync"""
        if device_id in self.sync_threads:
            thread = self.sync_threads[device_id]
            if thread.is_alive():
                return True
        return False

    def get_connected_cameras(self):
        """Get connected cameras from Detector"""
        # Return format expected by UI: [{'id':..., 'model':...}]
        
        # If detector is not running (e.g. Service not started), force a one-time scan
        if not self.detector._running:
            logger.debug("Force scanning cameras for UI request...")
            self.detector._scan_existing_cameras()
            
        cams = self.detector.get_connected_cameras() # {id: model}
        return [{"id": k, "model": v} for k, v in cams.items()]

    # --- HELPER METHODS FOR UI (MANUAL IMPORT) ---

    def is_syncing(self, device_id: str) -> bool:
        """Check if a specific device is currently running auto-sync"""
        # Handle composite ID from UI (e.g., "0BFF|G:")
        real_id = device_id.split('|')[0] if '|' in device_id else device_id
        
        if real_id in self.sync_threads:
            thread = self.sync_threads[real_id]
            if thread.is_alive():
                return True
        return False

    def get_connected_cameras(self):
        """Get connected cameras, split by Drive Letter for Mass Storage"""
        if not self.detector._running:
            self.detector._scan_existing_cameras()
            
        cams = self.detector.get_connected_cameras() # {pid: model}
        result = []
        
        from camera.mass_storage_handler import find_all_camera_drives
        drives = find_all_camera_drives() 
        
        for pid, model in cams.items():
            if drives:
                for d in drives:
                     result.append({
                        "id": f"{pid}|{d}",
                        "model": f"{model} ({d})"
                     })
            else:
                result.append({"id": pid, "model": model})
        return result

    def is_camera_mass_storage(self, model_name: str) -> bool:
        """Check if camera is available as Mass Storage drive"""
        return bool(find_camera_drive(model_name))

    def scan_camera_dates(self, device_id: str):
        """
        Scan dates - Thread Safe. Supports Composite ID (PID|Drive)
        """
        # Mass Storage specific drive
        if '|' in device_id:
            pid, drive = device_id.split('|')
            try:
                # Use pid for logging mainly, drive for connect
                model_name = self.detector.get_connected_cameras().get(pid, "Sony Camera")
                
                handler = MassStorageHandler(pid, model_name, drive)
                if handler.connect():
                    logger.info(f"Manual Scan: Using Mass Storage {drive}")
                    dates = set()
                    files = handler.list_files("DCIM")
                    for f in files:
                        if hasattr(f, 'date_modified'):
                            dates.add(f.date_modified.strftime("%Y-%m-%d"))
                    handler.disconnect()
                    return sorted(list(dates), reverse=True)
            except Exception as e:
                logger.error(f"Scan failed on {drive}: {e}")
            return []

        # Fallback MTP Logic
        cams = self.detector.get_connected_cameras()
        model_name = cams.get(device_id, "Sony Camera")
        
        for i in range(3):
            handler = MTPHandler(device_id, model_name)
            if handler.connect():
                try:
                    dates = set()
                    subfolders = handler.list_subfolders("Storage Media")
                    for folder in subfolders:
                        if re.match(r"\d{4}-\d{2}-\d{2}", folder):
                            dates.add(folder)
                    return sorted(list(dates), reverse=True)
                finally:
                    handler.disconnect()
            time.sleep(1)
        return []

    def queue_manual_sync(self, device_id: str, dates: list, mode: str = 'copy'):
        """Queue manual sync - Thread Safe. Supports Composite ID (PID|Drive)"""
        count = 0
        sync_mode = mode
        delete_after = (sync_mode == "move")
        
        # Mass Storage Single Drive Logic
        if '|' in device_id:
            pid, drive = device_id.split('|')
            model_name = self.detector.get_connected_cameras().get(pid, "Sony Camera")
            
            try:
                handler = MassStorageHandler(pid, model_name, drive)
                if handler.connect():
                     # 1. List files
                     all_files = handler.list_files("DCIM")
                     target_dates = set(dates)
                     
                     # 2. Process
                     for f in all_files:
                         f_date = f.date_modified.strftime("%Y-%m-%d")
                         if f_date in target_dates and self.config.is_file_type_allowed(f.name):
                             # Process Download
                             dest_path = self._get_dest_path(f, model_name)
                             
                             # Add Queue
                             self.queue.add_file(f.path, str(dest_path), f.size, model_name)
                             
                             # Download (Overwrite = True for Manual)
                             success = handler.download_file(f, dest_path, overwrite=True)
                             if success:
                                 count += 1
                                 self.queue.mark_completed(f.path, model_name)
                                 self._pending_upload.append(dest_path)
                                 if delete_after: handler.delete_file(f)
                handler.disconnect()
            except Exception as e:
                logger.error(f"Manual Sync failed on {drive}: {e}")
            self._trigger_backup_if_enabled()
            return count

        # Fallback MTP Logic
        cams = self.detector.get_connected_cameras()
        model_name = cams.get(device_id, "Sony Camera")
        
        handler = MTPHandler(device_id, model_name)
        if handler.connect():
            try:
                for date_folder in dates:
                    full_path = f"Storage Media/{date_folder}"
                    files = handler.list_files(full_path)
                    valid_files = [f for f in files if self.config.is_file_type_allowed(f.name)]
                    for mtp_file in valid_files:
                        dest_path = self._get_dest_path(mtp_file, model_name)
                        
                        self.queue.add_file(mtp_file.path, str(dest_path), mtp_file.size, model_name)
                        
                        success = handler.download_file(mtp_file, dest_path)
                        if success:
                            count += 1
                            self.queue.mark_completed(mtp_file.path, model_name)
                            self._pending_upload.append(dest_path)
                            # MTP Delete logic (risky, maybe disable for now or simple delete)
                            # MTP delete usually just deletes the file.
            finally:
                handler.disconnect()
        self._trigger_backup_if_enabled()
        return count

    def _show_notification(self, title, message):
        if self.notification_callback:
            self.notification_callback(title, message)

    def _trigger_backup_if_enabled(self):
        """Trigger one-way cloud backup, scoped to files downloaded this run"""
        dest_base = self.config.get("destination.base_path")
        remote_name = self.config.get("backup.remote_name", "drive")
        remote_path = self.config.get("backup.remote_path", "CameraBackup")

        files = None
        if self._pending_upload:
            base = Path(dest_base)
            rels = []
            for p in self._pending_upload:
                try:
                    rels.append(Path(p).relative_to(base).as_posix())
                except ValueError:
                    pass  # outside base_path — not backup-scoped
            files = rels
            self._pending_upload = []

        self.backup_mgr.run_backup(dest_base, remote_name, remote_path, files=files)
