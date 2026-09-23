import logging
import subprocess
import threading
import sys
import shutil
from pathlib import Path

logger = logging.getLogger(__name__)

class CloudBackupManager:
    """
    Manages cloud backup operations using Rclone.
    Enables one-way sync (copy) from local destination to cloud.
    """
    
    def __init__(self, config):
        self.config = config
        self.rclone_path = self._find_rclone()
        
        # Determine portable config path
        if getattr(sys, 'frozen', False):
            base_path = Path(sys.executable).parent
        else:
            base_path = Path(__file__).parent.parent
            
        self.rclone_conf = base_path / "rclone.conf"
        
    def _find_rclone(self):
        """Find rclone executable."""
        # 1. Check bundled (PyInstaller)
        if hasattr(sys, '_MEIPASS'):
            bundled_path = Path(sys._MEIPASS) / "rclone.exe"
            if bundled_path.exists():
                return str(bundled_path)
                
        # 2. Check current directory
        local_path = Path("rclone.exe").resolve()
        if local_path.exists():
            return str(local_path)
            
        # 3. Check system PATH
        path_exe = shutil.which("rclone")
        if path_exe:
            return path_exe
            
        # 4. Check typical installation paths (optional)
        
        return None

    def is_installed(self):
        return self.rclone_path is not None

    def configure_remote(self):
        """Run rclone config in a terminal."""
        if not self.rclone_path:
            raise FileNotFoundError("Rclone not found. Please place rclone.exe in the application folder.")
            
        try:
            # Launch in new console window
            # Use local config file
            cmd = [self.rclone_path, "config", "--config", str(self.rclone_conf)]
            subprocess.Popen(cmd, creationflags=subprocess.CREATE_NEW_CONSOLE)
        except Exception as e:
            logger.error(f"Failed to launch config: {e}")
            raise

    def run_backup(self, source_dir: str, remote_name: str, remote_dir: str = ""):
        """
        Run rclone copy in background.
        source_dir: Local folder path
        remote_name: Name of remote (e.g., 'gdrive')
        remote_dir: Path on remote (e.g., 'Backup/Photos')
        """
        if not self.rclone_path:
            logger.error("Rclone not found.")
            return

        if not self.config.get("backup.enabled", False):
            logger.info("Cloud backup disabled in settings.")
            return

        thread_name = f"Backup-Thread"
        # Avoid starting duplicate threads if one is already running for this exact sync? 
        # Rclone is safe to run multiple times, but let's avoid spamming.
        
        threading.Thread(target=self._run_rclone_copy, args=(source_dir, remote_name, remote_dir), daemon=True, name=thread_name).start()

    def _run_rclone_copy(self, source, remote, remote_dir):
        try:
            full_remote = f"{remote}:{remote_dir}"
            
            # Check if user wants to DELETE local files after upload
            delete_after = self.config.get("backup.delete_local_after_upload", False)
            operation = "move" if delete_after else "copy"
            
            action_icon = "🚚" if delete_after else "☁️"
            logger.info(f"{action_icon} Starting Cloud Backup ({operation}): {source} -> {full_remote}")
            
            # Build command
            cmd = [
                self.rclone_path, 
                operation, 
                source, 
                full_remote,
                "--config", str(self.rclone_conf),
                "--transfers", "4",
                "--log-level", "INFO"
            ]
            
            if delete_after:
                 # Safety: delete empty source dirs after move
                 cmd.append("--delete-empty-src-dirs")
            
            # We can capture output or log it
            process = subprocess.Popen(
                cmd, 
                stdout=subprocess.PIPE, 
                stderr=subprocess.PIPE,
                text=True,
                creationflags=subprocess.CREATE_NO_WINDOW
            )
            
            stdout, stderr = process.communicate()
            
            if process.returncode == 0:
                logger.info(f"✅ Cloud Backup Complete ({operation})")
            else:
                logger.error(f"❌ Cloud Backup Failed: {stderr}")
                
        except Exception as e:
            logger.error(f"Backup Error: {e}")

