"""
Mass Storage Handler for Sony Cameras
Handles cameras connected as USB Mass Storage (drive letter)
"""
import logging
import os
import shutil
from pathlib import Path
from typing import List, Optional
from datetime import datetime
import re

logger = logging.getLogger(__name__)

class MTPFile:
    """Represents a file (reuse from mtp_handler for compatibility)"""
    def __init__(self, name: str, size: int, date_modified: datetime, path: str):
        self.name = name
        self.size = size
        self.date_modified = date_modified
        self.path = path
        self.is_photo = name.upper().endswith(('.JPG', '.JPEG', '.ARW', '.HEIF'))
        self.is_video = name.upper().endswith(('.MP4', '.MTS', '.AVCHD'))


class MassStorageHandler:
    """Handle Mass Storage communication with Sony cameras"""
    
    def __init__(self, device_id: str, model_name: str, drive_letter: str):
        self.device_id = device_id
        self.model_name = model_name
        self.drive_letter = drive_letter  # E.g., "E:"
        self._connected = False
        
    def connect(self) -> bool:
        """
        Verify drive is accessible
        
        Returns:
            True if drive is accessible
        """
        try:
            # Check if drive exists and is accessible
            if not os.path.exists(self.drive_letter):
                logger.warning(f"Drive {self.drive_letter} not accessible")
                return False
                
            # Try to list root directory
            try:
                os.listdir(self.drive_letter)
            except PermissionError:
                logger.error(f"No permission to access {self.drive_letter}")
                return False
                
            self._connected = True
            logger.info(f"Connected to Mass Storage: {self.drive_letter} ({self.model_name})")
            return True
            
        except Exception as e:
            logger.error(f"Error connecting to Mass Storage: {e}")
            return False
            
    def disconnect(self):
        """Disconnect (no-op for mass storage)"""
        self._connected = False
            
    def list_files(self, folder_path: str = "DCIM") -> List[MTPFile]:
        """
        List files in a folder
        For Sony cameras, if folder_path is DCIM, also scans PRIVATE/M4ROOT/CLIP
        
        Args:
            folder_path: Path relative to drive root (default: "DCIM")
            
        Returns:
            List of MTPFile objects
        """
        if not self._connected:
            logger.error("Not connected to drive")
            return []
            
        try:
            files = []
            
            # 1. Scan requested folder (usually DCIM)
            full_path = Path(self.drive_letter) / folder_path
            
            if full_path.exists():
                # Recursively scan
                self._scan_folder(full_path, files, folder_path)
            else:
                logger.warning(f"Folder not found: {full_path}")
                
            # 2. Automatically scan Sony Video folder if scanning DCIM
            if folder_path == "DCIM":
                video_path_rel = "PRIVATE/M4ROOT/CLIP"
                video_path_full = Path(self.drive_letter) / "PRIVATE" / "M4ROOT" / "CLIP"
                
                if video_path_full.exists():
                    logger.debug(f"Scanning Sony Video folder: {video_path_full}")
                    self._scan_folder(video_path_full, files, video_path_rel)
            
            return files
            
        except Exception as e:
            logger.error(f"Error listing files: {e}")
            return []
            
    def list_subfolders(self, folder_path: str = "DCIM") -> List[str]:
        """List subfolders (non-recursive)"""
        if not self._connected:
            return []
            
        try:
            full_path = Path(self.drive_letter) / folder_path
            
            if not full_path.exists():
                return []
                
            subfolders = []
            for item in full_path.iterdir():
                if item.is_dir():
                    subfolders.append(item.name)
            return subfolders
            
        except Exception as e:
            logger.error(f"Error listing subfolders: {e}")
            return []
            
    def _scan_folder(self, folder: Path, files: List[MTPFile], current_path: str):
        """Recursively scan folder for files"""
        try:
            for item in folder.iterdir():
                item_path = f"{current_path}/{item.name}"
                
                # Skip thumbnail folder (Sony cameras)
                if "THMBNL" in item_path.upper() or "THUMBNAIL" in item_path.upper():
                    logger.debug(f"Skipping thumbnail folder: {item_path}")
                    continue
                
                if item.is_dir():
                    # Recurse into subfolder
                    self._scan_folder(item, files, item_path)
                else:
                    # It's a file
                    try:
                        stat = item.stat()
                        size = stat.st_size
                        
                        # Extract date from folder path (Sony: DCIM/YYYY-MM-DD/)
                        date_match = re.search(r'(\d{4})-(\d{2})-(\d{2})', current_path)
                        
                        if date_match:
                            year, month, day = date_match.groups()
                            date_modified = datetime(int(year), int(month), int(day))
                        else:
                            # Use file modification time
                            date_modified = datetime.fromtimestamp(stat.st_mtime)
                        
                        mtp_file = MTPFile(
                            name=item.name,
                            size=size,
                            date_modified=date_modified,
                            path=item_path
                        )
                        files.append(mtp_file)
                        
                    except Exception as e:
                        logger.warning(f"Error reading file info for {item.name}: {e}")
                        
        except Exception as e:
            logger.error(f"Error scanning folder: {e}")
            
    def download_file(self, mtp_file: MTPFile, destination_path: Path, overwrite: bool = True) -> bool:
        """
        Copy file from drive to destination
        
        Args:
            mtp_file: MTPFile object
            destination_path: Local path to save file
            overwrite: If True, overwrite existing files
            
        Returns:
            True if copied successfully
        """
        if not self._connected:
            logger.error("Not connected to drive")
            return False
            
        try:
            # Ensure destination directory exists
            destination_path.parent.mkdir(parents=True, exist_ok=True)
            
            # Check if file exists
            if destination_path.exists() and not overwrite:
                logger.info(f"Skipped (already exists): {mtp_file.name}")
                return True
                
            # Build source path
            source_path = Path(self.drive_letter) / mtp_file.path
            
            if not source_path.exists():
                logger.error(f"Source file not found: {source_path}")
                return False
                
            # Copy file
            shutil.copy2(source_path, destination_path)
            
            logger.info(f"Downloaded: {mtp_file.name} -> {destination_path}")
            return True
            
        except Exception as e:
            logger.error(f"Error downloading file {mtp_file.name}: {e}")
            return False

    def delete_file(self, mtp_file: MTPFile) -> bool:
        """
        Delete file from drive (for Move mode)
        Also deletes associated thumbnail files for videos
        
        Args:
            mtp_file: MTPFile object to delete
            
        Returns:
            True if deleted successfully
        """
        if not self._connected:
            return False
            
        try:
            source_path = Path(self.drive_letter) / mtp_file.path
            
            if not source_path.exists():
                logger.warning(f"File not found for deletion: {source_path}")
                return False
            
            # Delete associated thumbnail/XML for videos
            if mtp_file.is_video:
                # 1. Thumbnails in PRIVATE/M4ROOT/THMBNL/
                # Logic: Find ALL files in THMBNL that start with the video filename
                # Covers: C3035.JPG, C3035T01.JPG, C3035.THM, etc.
                try:
                    thm_dir = Path(self.drive_letter) / "PRIVATE" / "M4ROOT" / "THMBNL"
                    if thm_dir.exists():
                        # Pattern matches C3035*.JPG and C3035*.THM
                        # We iterate common extensions to be safe
                        for ext in ['*.JPG', '*.THM']:
                            pattern = f"{source_path.stem}{ext}"
                            for f in thm_dir.glob(pattern):
                                try:
                                    os.remove(f)
                                    logger.debug(f"Deleted associated thumbnail: {f.name}")
                                except Exception as e:
                                    logger.warning(f"Could not delete thumbnail {f.name}: {e}")
                except Exception as e:
                    logger.warning(f"Error scanning for thumbnails: {e}")

                # 2. Find and delete ALL associated XML files (e.g. C3035.XML, C3035M01.XML)
                # Pattern: FILENAME*.XML in the same directory
                try:
                    directory = source_path.parent
                    xml_pattern = f"{source_path.stem}*.XML"
                    
                    for xml_file in directory.glob(xml_pattern):
                        try:
                            os.remove(xml_file)
                            logger.debug(f"Deleted associated XML: {xml_file.name}")
                        except Exception as e:
                            logger.warning(f"Could not delete XML {xml_file.name}: {e}")
                except Exception as e:
                    logger.warning(f"Error scanning for XML files: {e}")
            
            # Delete main file
            os.remove(source_path)
            logger.info(f"Deleted from drive: {mtp_file.name}")
            return True
            
        except Exception as e:
            logger.error(f"Error deleting file {mtp_file.name}: {e}")
            return False


def find_all_camera_drives() -> List[str]:
    """
    Find ALL connected camera drives (for dual slot support)
    Checks for DCIM or PRIVATE folders on Removable drives
    """
    import string
    import ctypes
    
    found_drives = []
    
    # DRIVE_REMOVABLE = 2
    DRIVE_REMOVABLE = 2
    
    for letter in string.ascii_uppercase[2:]:  # Start from C:
        drive = f"{letter}:\\"
        try:
            drive_type = ctypes.windll.kernel32.GetDriveTypeW(drive)
            if drive_type != DRIVE_REMOVABLE:
                continue
            
            # Check for common camera structures
            has_dcim = (Path(drive) / "DCIM").exists()
            has_private = (Path(drive) / "PRIVATE").exists()
            
            if has_dcim or has_private:
                logger.debug(f"Found candidate drive: {letter}:")
                found_drives.append(f"{letter}:")
                
        except Exception:
            continue
            
    return found_drives

def find_camera_drive(model_name: str) -> Optional[str]:
    # Legacy wrapper for backward compatibility
    # Just returns the first one found
    drives = find_all_camera_drives()
    if drives:
        logger.info(f"✅ Found camera drive: {drives[0]} (Mass Storage)")
        return drives[0]
    return None
