import logging
import os
import subprocess
import tempfile
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

        # Single-flight + pending queue (hotfix P0)
        self._backup_lock = threading.Lock()
        self._pending_files = set()
        self._warned_delete_disabled = False
        
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

    def run_backup(self, source_dir: str, remote_name: str, remote_dir: str = "", files=None):
        """
        Queue a cloud backup in background (single-flight).
        source_dir: Local folder path (rclone source root)
        remote_name: Name of remote (e.g., 'gdrive')
        remote_dir: Path on remote (e.g., 'Backup/Photos')
        files: optional list of paths RELATIVE to source_dir — when given,
               backup is scoped to just those files via --files-from.
               When empty, the whole source_dir is copied (legacy behavior).
        """
        if not self.rclone_path:
            logger.error("Rclone not found.")
            return

        if not self.config.get("backup.enabled", False):
            logger.info("Cloud backup disabled in settings.")
            return

        if files:
            self._pending_files.update(files)

        if self._backup_lock.locked():
            logger.info("☁️ Backup already running — files queued for its next pass.")
            return

        threading.Thread(target=self._backup_worker, args=(source_dir, remote_name, remote_dir), daemon=True, name="Backup-Thread").start()

    def _backup_worker(self, source, remote, remote_dir):
        """Serialized worker: drains pending file batches, else whole-dir copy."""
        with self._backup_lock:
            if self._pending_files:
                batch = sorted(self._pending_files)
                self._pending_files.difference_update(batch)
                if not self._run_rclone_copy(source, remote, remote_dir, files=batch):
                    # Requeue on failure so the next trigger retries them
                    self._pending_files.update(batch)
                    return
                # Pick up files queued while this batch ran
                while self._pending_files:
                    batch = sorted(self._pending_files)
                    self._pending_files.difference_update(batch)
                    if not self._run_rclone_copy(source, remote, remote_dir, files=batch):
                        self._pending_files.update(batch)
                        return
            else:
                self._run_rclone_copy(source, remote, remote_dir)

    def _run_rclone_copy(self, source, remote, remote_dir, files=None) -> bool:
        """
        Run one rclone pass. Returns True on exit code 0.
        Always 'copy' — 'move'/delete-after-upload is disabled until the
        verified staging pipeline ships (see .docs/go-refactor/plan.md §5).
        """
        files_from_path = None
        try:
            full_remote = f"{remote}:{remote_dir}"

            delete_after = self.config.get("backup.delete_local_after_upload", False)
            if delete_after and not self._warned_delete_disabled:
                logger.warning(
                    "⚠️ backup.delete_local_after_upload is temporarily DISABLED "
                    "(rclone move on the library root could delete unrelated files). "
                    "Files are copy-only until the verified-delete pipeline ships."
                )
                self._warned_delete_disabled = True
            operation = "copy"

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

            scope = "full directory"
            if files:
                fd, files_from_path = tempfile.mkstemp(prefix="cc_files_", suffix=".txt")
                with open(fd, "w", encoding="utf-8") as f:
                    f.write("\n".join(files) + "\n")
                cmd += ["--files-from", files_from_path]
                scope = f"{len(files)} file(s)"

            logger.info(f"☁️ Starting Cloud Backup ({operation}, {scope}): {source} -> {full_remote}")

            process = subprocess.Popen(
                cmd,
                stdout=subprocess.PIPE,
                stderr=subprocess.PIPE,
                text=True,
                creationflags=subprocess.CREATE_NO_WINDOW
            )

            stdout, stderr = process.communicate()

            if process.returncode == 0:
                logger.info(f"✅ Cloud Backup Complete ({scope})")
                return True
            else:
                logger.error(f"❌ Cloud Backup Failed: {stderr}")
                return False

        except Exception as e:
            logger.error(f"Backup Error: {e}")
            return False
        finally:
            if files_from_path:
                try:
                    os.remove(files_from_path)
                except OSError:
                    pass

