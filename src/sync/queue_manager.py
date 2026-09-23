"""
Sync Queue Manager
Quản lý trạng thái sync files với SQLite database
"""
import logging
import sqlite3
from pathlib import Path
from datetime import datetime
from typing import List, Optional, Dict
from enum import Enum

logger = logging.getLogger(__name__)


class SyncStatus(Enum):
    """File sync status"""
    PENDING = "pending"
    DOWNLOADING = "downloading"
    UPLOADING = "uploading"
    COMPLETED = "completed"
    FAILED = "failed"


class FileRecord:
    """Represents a file in sync queue"""
    def __init__(self, id: int, camera_path: str, local_path: str, 
                 status: SyncStatus, file_size: int, camera_model: str,
                 created_at: datetime, completed_at: Optional[datetime] = None):
        self.id = id
        self.camera_path = camera_path
        self.local_path = local_path
        self.status = status
        self.file_size = file_size
        self.camera_model = camera_model
        self.created_at = created_at
        self.completed_at = completed_at


class QueueManager:
    """Manage sync queue with SQLite"""
    
    def __init__(self, db_path: Path = None):
        if db_path is None:
            db_path = Path.home() / ".sony_camera_sync" / "sync_queue.db"
            
        self.db_path = db_path
        self.db_path.parent.mkdir(parents=True, exist_ok=True)
        
        self._init_database()
        
    def _init_database(self):
        """Initialize database schema"""
        try:
            conn = sqlite3.connect(self.db_path)
            cursor = conn.cursor()
            
            cursor.execute("""
                CREATE TABLE IF NOT EXISTS sync_queue (
                    id INTEGER PRIMARY KEY AUTOINCREMENT,
                    camera_path TEXT NOT NULL,
                    local_path TEXT NOT NULL,
                    status TEXT NOT NULL,
                    file_size INTEGER,
                    camera_model TEXT,
                    created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
                    completed_at TIMESTAMP,
                    error_message TEXT,
                    UNIQUE(camera_path, camera_model)
                )
            """)
            
            # Create index for faster queries
            cursor.execute("""
                CREATE INDEX IF NOT EXISTS idx_status 
                ON sync_queue(status)
            """)
            
            cursor.execute("""
                CREATE INDEX IF NOT EXISTS idx_camera_model 
                ON sync_queue(camera_model)
            """)
            
            conn.commit()
            conn.close()
            
            logger.info(f"Database initialized: {self.db_path}")
            
        except Exception as e:
            logger.error(f"Error initializing database: {e}")
            raise
            
    def add_file(self, camera_path: str, local_path: str, file_size: int, 
                 camera_model: str) -> Optional[int]:
        """
        Add a file to sync queue
        
        Returns:
            File ID or None if already exists
        """
        try:
            conn = sqlite3.connect(self.db_path)
            cursor = conn.cursor()
            
            cursor.execute("""
                INSERT OR IGNORE INTO sync_queue 
                (camera_path, local_path, status, file_size, camera_model)
                VALUES (?, ?, ?, ?, ?)
            """, (camera_path, local_path, SyncStatus.PENDING.value, file_size, camera_model))
            
            file_id = cursor.lastrowid
            conn.commit()
            conn.close()
            
            if file_id > 0:
                logger.info(f"Added to queue: {camera_path}")
                return file_id
            else:
                logger.debug(f"File already in queue: {camera_path}")
                return None
                
        except Exception as e:
            logger.error(f"Error adding file to queue: {e}")
            return None
            
    def update_status(self, file_id: int, status: SyncStatus, error_message: str = None):
        """Update file sync status"""
        try:
            conn = sqlite3.connect(self.db_path)
            cursor = conn.cursor()
            
            if status == SyncStatus.COMPLETED:
                cursor.execute("""
                    UPDATE sync_queue 
                    SET status = ?, completed_at = CURRENT_TIMESTAMP, error_message = ?
                    WHERE id = ?
                """, (status.value, error_message, file_id))
            else:
                cursor.execute("""
                    UPDATE sync_queue 
                    SET status = ?, error_message = ?
                    WHERE id = ?
                """, (status.value, error_message, file_id))
                
            conn.commit()
            conn.close()
            
        except Exception as e:
            logger.error(f"Error updating status: {e}")
            
    def get_pending_files(self) -> List[FileRecord]:
        """Get all pending files"""
        return self._get_files_by_status(SyncStatus.PENDING)
        
    def get_failed_files(self) -> List[FileRecord]:
        """Get all failed files"""
        return self._get_files_by_status(SyncStatus.FAILED)
        
    def _get_files_by_status(self, status: SyncStatus) -> List[FileRecord]:
        """Get files by status"""
        try:
            conn = sqlite3.connect(self.db_path)
            cursor = conn.cursor()
            
            cursor.execute("""
                SELECT id, camera_path, local_path, status, file_size, camera_model,
                       created_at, completed_at
                FROM sync_queue
                WHERE status = ?
                ORDER BY created_at ASC
            """, (status.value,))
            
            records = []
            for row in cursor.fetchall():
                records.append(FileRecord(
                    id=row[0],
                    camera_path=row[1],
                    local_path=row[2],
                    status=SyncStatus(row[3]),
                    file_size=row[4],
                    camera_model=row[5],
                    created_at=datetime.fromisoformat(row[6]),
                    completed_at=datetime.fromisoformat(row[7]) if row[7] else None
                ))
                
            conn.close()
            return records
            
        except Exception as e:
            logger.error(f"Error getting files by status: {e}")
            return []
            
    def is_file_synced(self, camera_path: str, camera_model: str) -> bool:
        """Check if file has been synced before"""
        try:
            conn = sqlite3.connect(self.db_path)
            cursor = conn.cursor()
            
            cursor.execute("""
                SELECT COUNT(*) FROM sync_queue
                WHERE camera_path = ? AND camera_model = ? AND status = ?
            """, (camera_path, camera_model, SyncStatus.COMPLETED.value))
            
            count = cursor.fetchone()[0]
            conn.close()
            
            return count > 0
            
        except Exception as e:
            logger.error(f"Error checking if file synced: {e}")
            return False
    
    def mark_completed(self, camera_path: str, camera_model: str):
        """Mark a file as completed (for files that already exist locally)"""
        try:
            conn = sqlite3.connect(self.db_path)
            cursor = conn.cursor()
            
            cursor.execute("""
                UPDATE sync_queue 
                SET status = ?, completed_at = CURRENT_TIMESTAMP
                WHERE camera_path = ? AND camera_model = ?
            """, (SyncStatus.COMPLETED.value, camera_path, camera_model))
            
            conn.commit()
            conn.close()
            
        except Exception as e:
            logger.error(f"Error marking file as completed: {e}")
            
    def get_stats(self, camera_model: str = None) -> Dict:
        """Get sync statistics"""
        try:
            conn = sqlite3.connect(self.db_path)
            cursor = conn.cursor()
            
            if camera_model:
                cursor.execute("""
                    SELECT status, COUNT(*), SUM(file_size)
                    FROM sync_queue
                    WHERE camera_model = ?
                    GROUP BY status
                """, (camera_model,))
            else:
                cursor.execute("""
                    SELECT status, COUNT(*), SUM(file_size)
                    FROM sync_queue
                    GROUP BY status
                """)
                
            stats = {
                "total": 0,
                "completed": 0,
                "pending": 0,
                "failed": 0,
                "total_size": 0
            }
            
            for row in cursor.fetchall():
                status = row[0]
                count = row[1]
                size = row[2] or 0
                
                stats["total"] += count
                stats["total_size"] += size
                
                if status == SyncStatus.COMPLETED.value:
                    stats["completed"] = count
                elif status == SyncStatus.PENDING.value:
                    stats["pending"] = count
                elif status == SyncStatus.FAILED.value:
                    stats["failed"] = count
                    
            conn.close()
            return stats
            
        except Exception as e:
            logger.error(f"Error getting stats: {e}")
            return {}


if __name__ == "__main__":
    # Test code
    logging.basicConfig(level=logging.INFO)
    
    qm = QueueManager()
    
    # Add test files
    qm.add_file("DCIM/100MSDCF/DSC00001.ARW", "D:/Photos/test.ARW", 25000000, "ZV-E10")
    qm.add_file("DCIM/100MSDCF/DSC00002.JPG", "D:/Photos/test2.JPG", 5000000, "ZV-E10")
    
    # Get pending
    pending = qm.get_pending_files()
    print(f"Pending files: {len(pending)}")
    
    # Get stats
    stats = qm.get_stats()
    print(f"Stats: {stats}")
