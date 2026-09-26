"""
MTP Handler for Sony Cameras
Sử dụng Windows MTP API để truy cập thẻ nhớ camera
"""
import logging
import os
import time
from pathlib import Path
from typing import List, Optional, Dict
from datetime import datetime
import comtypes.client
from comtypes import CoInitialize, CoUninitialize

logger = logging.getLogger(__name__)


class MTPFile:
    """Represents a file on MTP device"""
    def __init__(self, name: str, size: int, date_modified: datetime, path: str):
        self.name = name
        self.size = size
        self.date_modified = date_modified
        self.path = path
        self.is_photo = name.upper().endswith(('.JPG', '.JPEG', '.ARW', '.HEIF'))
        self.is_video = name.upper().endswith(('.MP4', '.MTS', '.AVCHD'))


class MTPHandler:
    """Handle MTP communication with Sony cameras"""
    
    def __init__(self, device_id: str, model_name: str):
        self.device_id = device_id
        self.model_name = model_name
        self._shell = None
        self._device = None
        self._connected = False
        
    def connect(self) -> bool:
        """
        Connect to MTP device
        
        Returns:
            True if connected successfully
        """
        try:
            CoInitialize()
            
            # Create Shell.Application COM object
            self._shell = comtypes.client.CreateObject("Shell.Application")
            
            # Find the camera in "Computer" namespace
            computer = self._shell.NameSpace(17)  # 17 = My Computer
            
            # Sony ILCE model codes (official names in Windows)
            ILCE_CODES = {
                "A7R III": "ILCE-7RM3",
                "A7 III": "ILCE-7M3",
                "A7 IV": "ILCE-7M4",
                "ZV-E10": "ILCE-ZV-E10",
                "NEX-5R": "NEX-5R",
            }
            
            # Get ILCE code for this model
            ilce_code = ILCE_CODES.get(self.model_name, self.model_name)
            
            # Look for portable devices
            for item in computer.Items():
                item_name = item.Name
                logger.debug(f"Checking device: {item_name}")
                
                # Check if this is our camera
                # Match by: ILCE code, model name, or "sony"
                if (ilce_code.lower() in item_name.lower() or 
                    self.model_name.lower() in item_name.lower() or 
                    "sony" in item_name.lower() or
                    "ilce" in item_name.lower()):
                    
                    self._device = item
                    self._connected = True
                    # Update model_name to actual MTP device name
                    self.model_name = item_name
                    logger.info(f"Connected to MTP device: {item_name}")
                    return True
                    
            logger.debug(f"Could not find MTP device for {self.model_name} (ILCE: {ilce_code})")
            return False
            
        except Exception as e:
            logger.error(f"Error connecting to MTP device: {e}")
            return False
            
    def disconnect(self):
        """Disconnect from MTP device"""
        self._device = None
        self._shell = None
        self._connected = False
        try:
            CoUninitialize()
        except:
            pass
            
    def list_files(self, folder_path: str = "Storage Media") -> List[MTPFile]:
        """
        List files in a folder on the camera
        
        Args:
            folder_path: Path relative to device root (default: "Storage Media")
            
        Returns:
            List of MTPFile objects
        """
        if not self._connected or not self._device:
            logger.error("Not connected to MTP device")
            return []
            
        try:
            files = []
            current_folder = self._navigate_to_folder(folder_path)
            
            if not current_folder:
                logger.warning(f"Folder not found: {folder_path}")
                # Try root folder instead
                current_folder = self._device.GetFolder
                logger.info("Using root folder instead")
                
            # Recursively get all files
            self._scan_folder(current_folder, files, folder_path if folder_path else "root")
            
            return files
            
        except Exception as e:
            logger.error(f"Error listing files: {e}")
            return []
            
    def list_subfolders(self, folder_path: str = "Storage Media") -> List[str]:
        """List subfolders in a folder (non-recursive)"""
        if not self._connected or not self._device:
            return []
            
        try:
            folder = self._navigate_to_folder(folder_path)
            if not folder:
                # Try scanning root if specific folder fails
                folder = self._device.GetFolder
                
            subfolders = []
            for item in folder.Items():
                if item.IsFolder:
                    subfolders.append(item.Name)
            return subfolders
            
        except Exception as e:
            logger.error(f"Error listing subfolders: {e}")
            return []
            
    def _navigate_to_folder(self, path: str):
        """Navigate to a folder on the device"""
        try:
            # GetFolder is a property, not a method
            current = self._device.GetFolder
            
            if not path or path == ".":
                return current
                
            # Split path and navigate
            parts = path.split("/")
            for part in parts:
                if not part:
                    continue
                    
                found = False
                for item in current.Items():
                    if item.IsFolder and item.Name.upper() == part.upper():
                        current = item.GetFolder
                        found = True
                        break
                        
                if not found:
                    logger.warning(f"Folder not found: {part}")
                    return None
                    
            return current
            
        except Exception as e:
            logger.error(f"Error navigating to folder: {e}")
            return None
            
    def _scan_folder(self, folder, files: List[MTPFile], current_path: str):
        """Recursively scan folder for files"""
        try:
            for item in folder.Items():
                item_path = f"{current_path}/{item.Name}"
                
                if item.IsFolder:
                    # Recurse into subfolder (GetFolder is a property)
                    subfolder = item.GetFolder
                    self._scan_folder(subfolder, files, item_path)
                else:
                    # It's a file
                    try:
                        # Get file size - use item.Size (may be 0 for MTP, but we use date-based skip anyway)
                        size = item.Size if hasattr(item, 'Size') else 0
                        
                        # Extract date from folder path (Sony organizes by date: Storage Media/YYYY-MM-DD/)
                        # Example: "Storage Media/2025-12-25/DSC00001.JPG"
                        import re
                        date_match = re.search(r'(\d{4})-(\d{2})-(\d{2})', current_path)
                        
                        if date_match:
                            # Use date from folder path
                            from datetime import datetime
                            year, month, day = date_match.groups()
                            date_modified = datetime(int(year), int(month), int(day))
                        else:
                            # Fallback to ModifyDate if available
                            modify_date = item.ModifyDate
                            
                            if isinstance(modify_date, (int, float)) and modify_date > 0:
                                # OLE date: days since 1899-12-30
                                from datetime import datetime, timedelta
                                ole_epoch = datetime(1899, 12, 30)
                                date_modified = ole_epoch + timedelta(days=modify_date)
                            else:
                                # Use current date as fallback
                                from datetime import datetime
                                date_modified = datetime.now()
                        
                        mtp_file = MTPFile(
                            name=item.Name,
                            size=size,
                            date_modified=date_modified,
                            path=item_path
                        )
                        files.append(mtp_file)
                        
                    except Exception as e:
                        logger.warning(f"Error reading file info for {item.Name}: {e}")
                        
        except Exception as e:
            logger.error(f"Error scanning folder: {e}")
            
    def download_file(self, mtp_file: MTPFile, destination_path: Path, overwrite: bool = True) -> bool:
        """
        Download a file from camera to local disk
        
        Args:
            mtp_file: MTPFile object to download
            destination_path: Local path to save file
            overwrite: If True, overwrite existing files without prompt
            
        Returns:
            True if downloaded successfully
        """
        if not self._connected or not self._device:
            logger.error("Not connected to MTP device")
            return False
            
        try:
            # Ensure destination directory exists
            destination_path.parent.mkdir(parents=True, exist_ok=True)
            
            # Check if file exists and skip if overwrite is False
            if destination_path.exists() and not overwrite:
                logger.info(f"Skipped (already exists): {mtp_file.name}")
                return True  # Not an error, just skipped
            
            # Navigate to the file's folder (use forward slash for MTP)
            folder_path = "/".join(mtp_file.path.split("/")[:-1])
            folder = self._navigate_to_folder(folder_path)
            
            if not folder:
                logger.error(f"Could not navigate to folder: {folder_path}")
                return False
                
            # Find the file
            for item in folder.Items():
                if item.Name == mtp_file.name:
                    # Copy file using Shell.Application
                    dest_folder = self._shell.NameSpace(str(destination_path.parent))
                    
                    # Flags for silent operation:
                    # 4 = No progress dialog
                    # 16 = Yes to all (auto-confirm)
                    # 512 = Don't confirm directory creation
                    # 1024 = Don't display error UI
                    # 8192 = No confirmation dialogs
                    # Combined: 4 + 16 + 512 + 1024 + 8192 = 9748
                    copy_flags = 9748 if overwrite else 16
                    
                    dest_folder.CopyHere(item, copy_flags)

                    # CopyHere is asynchronous — wait until the destination
                    # file exists and its size has stopped growing before
                    # reporting success (prevents truncated uploads/deletes).
                    if not self._wait_settled(destination_path):
                        logger.error(f"Download incomplete (settle timeout): {mtp_file.name}")
                        return False

                    logger.info(f"Downloaded: {mtp_file.name} -> {destination_path}")
                    return True
                    
            logger.error(f"File not found: {mtp_file.name}")
            return False
            
        except Exception as e:
            logger.error(f"Error downloading file {mtp_file.name}: {e}")
            return False

    def _wait_settled(self, path: Path, timeout_s: int = 600,
                      interval: float = 0.5, stable_rounds: int = 3) -> bool:
        """
        Wait until `path` exists and its size stays unchanged for
        `stable_rounds` consecutive polls (shell copy finished).
        Returns False on timeout so callers can retry instead of
        trusting a partially-written file.
        """
        deadline = time.time() + timeout_s
        last_size, stable = -1, 0
        while time.time() < deadline:
            try:
                if path.exists():
                    size = path.stat().st_size
                    if size > 0 and size == last_size:
                        stable += 1
                        if stable >= stable_rounds:
                            return True
                    else:
                        stable, last_size = 0, size
            except OSError:
                pass
            time.sleep(interval)
        logger.warning(f"Settle timeout waiting for {path.name}")
        return False

    def delete_file(self, mtp_file: MTPFile) -> bool:
        """
        Delete a file from the camera
        
        Args:
            mtp_file: MTPFile object to delete
            
        Returns:
            True if deleted successfully (or delete command sent)
        """
        if not self._connected or not self._device:
            return False
            
        try:
            # Navigate to folder
            folder_path = "/".join(mtp_file.path.split("/")[:-1])
            folder = self._navigate_to_folder(folder_path)
            
            if not folder:
                return False
                
            # Find item
            for item in folder.Items():
                if item.Name == mtp_file.name:
                    # Execute delete
                    # Note: This might show a confirmation dialog on Windows
                    # We try InvokeVerb("delete")
                    item.InvokeVerb("delete")
                    logger.info(f"Deleted (command sent): {mtp_file.name}")
                    return True
                    
            return False
            
        except Exception as e:
            logger.error(f"Error deleting file {mtp_file.name}: {e}")
            return False


if __name__ == "__main__":
    # Test code
    logging.basicConfig(
        level=logging.INFO,
        format='%(asctime)s - %(name)s - %(levelname)s - %(message)s'
    )
    
    # This requires a Sony camera to be connected in MTP mode
    handler = MTPHandler("test_device", "Sony Camera")
    
    if handler.connect():
        print("✅ Connected to camera")
        
        print("\nListing files in DCIM...")
        files = handler.list_files("DCIM")
        
        print(f"\nFound {len(files)} files:")
        for f in files[:10]:  # Show first 10
            file_type = "📷 Photo" if f.is_photo else "🎥 Video" if f.is_video else "📄 File"
            print(f"{file_type} {f.name} ({f.size / 1024 / 1024:.2f} MB)")
            
        handler.disconnect()
    else:
        print("❌ Could not connect to camera")
