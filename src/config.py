"""
Configuration Manager
Load and manage app configuration from YAML
"""
import logging
import sys
import os
from pathlib import Path
from typing import Dict, Any
import yaml

logger = logging.getLogger(__name__)


def get_config_filename():
    """
    Generate config filename based on executable name.
    Example: CameraConnect.exe → CameraConnectConfig.yaml
    """
    # Default fallback name
    default_name = "CameraConnect"
    
    # Get the executable name
    if getattr(sys, 'frozen', False):
        # Running as compiled executable (PyInstaller)
        exe_path = Path(sys.executable)
        exe_name = exe_path.stem  # e.g., "CameraConnect" from "CameraConnect.exe"
    else:
        # Running as Python script
        # Try to get the main script name
        script_name = sys.argv[0] if sys.argv and sys.argv[0] else ""
        
        # Handle edge cases (python -c, interactive mode, etc.)
        if not script_name or script_name.startswith('-') or not Path(script_name).suffix:
            exe_name = default_name
        else:
            exe_path = Path(script_name)
            # Use the script name (e.g., "tray_app" from "tray_app.py")
            # But for app consistency, we want to use the app name
            # Check if it's one of our known entry points
            if exe_path.stem in ('tray_app', 'main'):
                exe_name = default_name  # Use app name for known entry points
            else:
                exe_name = exe_path.stem
    
    # Create config filename
    config_filename = f"{exe_name}Config.yaml"
    
    logger.debug(f"Config filename: {config_filename}")
    return config_filename


class Config:
    """Application configuration"""
    
    def __init__(self, config_path: Path = None):
        if config_path is None:
            # Generate config filename based on executable name
            config_filename = get_config_filename()
            
            # Look for config file in current directory or user home
            if Path(config_filename).exists():
                config_path = Path(config_filename)
            else:
                # Fallback: check in executable directory
                if getattr(sys, 'frozen', False):
                    exe_dir = Path(sys.executable).parent
                else:
                    exe_dir = Path(__file__).parent.parent  # Project root
                
                if (exe_dir / config_filename).exists():
                    config_path = exe_dir / config_filename
                else:
                    # Default: create in current directory
                    config_path = Path(config_filename)
                
        self.config_path = config_path
        self._data: Dict[str, Any] = {}
        
        self.load()
        
    def load(self):
        """Load configuration from YAML file"""
        try:
            if not self.config_path.exists():
                logger.warning(f"Config file not found: {self.config_path}")
                self._load_defaults()
                return
                
            with open(self.config_path, 'r', encoding='utf-8') as f:
                self._data = yaml.safe_load(f) or {}
                
            logger.info(f"Configuration loaded from {self.config_path}")
            
        except Exception as e:
            logger.error(f"Error loading config: {e}")
            self._load_defaults()
            
    def _load_defaults(self):
        """Load default configuration"""
        self._data = {
            "destination": {
                "base_path": str(Path.home() / "Pictures" / "SonySync"),
                "folder_template": "{camera}/{year}/{month}-{day}"
            },
            "file_types": {
                "photos": ["ARW", "JPG", "JPEG", "HEIF"],
                "videos": ["MP4", "MTS", "AVCHD"]
            },
            "notifications": {
                "on_camera_connect": True,
                "on_copy_complete": "batch",
                "play_sound": True
            },
            "general": {
                "start_with_windows": False,
                "minimize_to_tray": True,
                "delete_after_sync": False,
                "poll_interval": 3
            }
        }
        
    def save(self):
        """Save configuration to YAML file"""
        try:
            self.config_path.parent.mkdir(parents=True, exist_ok=True)
            
            with open(self.config_path, 'w', encoding='utf-8') as f:
                yaml.dump(self._data, f, default_flow_style=False, allow_unicode=True)
                
            logger.info(f"Configuration saved to {self.config_path}")
            
        except Exception as e:
            logger.error(f"Error saving config: {e}")
            
    def get(self, key_path: str, default=None):
        """
        Get config value by dot-separated path
        
        Example: config.get("destination.base_path")
        """
        keys = key_path.split(".")
        value = self._data
        
        for key in keys:
            if isinstance(value, dict) and key in value:
                value = value[key]
            else:
                return default
                
        return value
        
    def set(self, key_path: str, value):
        """
        Set config value by dot-separated path
        
        Example: config.set("destination.base_path", "D:/Photos")
        """
        keys = key_path.split(".")
        data = self._data
        
        for key in keys[:-1]:
            if key not in data:
                data[key] = {}
            data = data[key]
            
        data[keys[-1]] = value
        
    def get_destination_path(self, camera_model: str, file_date: str, file_type: str) -> Path:
        """
        Generate destination path based on template
        
        Args:
            camera_model: Camera model name
            file_date: File date in YYYY-MM-DD format
            file_type: "photo" or "video"
            
        Returns:
            Full destination path
            
        Supported placeholders:
            {camera}     - Camera model name
            {type}       - File type (Photo/Video)
            {yyyy}       - Year 4 digits (2025)
            {yy}         - Year 2 digits (25)
            {mm}         - Month 2 digits with leading zero (01-12)
            {m}          - Month 1-2 digits without leading zero (1-12)
            {dd}         - Day 2 digits with leading zero (01-31)
            {d}          - Day 1-2 digits without leading zero (1-31)
            {date}       - Full date YYYY-MM-DD (2025-12-25)
            
        Examples:
            "{camera}/{yyyy}/{mm}-{dd}"           -> "ZV-E10/2025/12-25"
            "{yyyy}-{mm}-{dd}"                    -> "2025-12-25" (single folder)
            "{camera}/{yyyy}/{m}/{d}"             -> "ZV-E10/2025/12/25"
            "Video/{camera}/{yyyy}/{mm}"          -> "Video/ZV-E10/2025/12"
        """
        from datetime import datetime
        
        base_path = self.get("destination.base_path")
        
        # Use separate templates for photos and videos
        if file_type == "video":
            template = self.get("destination.video_template", self.get("destination.photo_template", "{camera}/{yyyy}/{yyyy}-{mm}-{dd}"))
        else:
            template = self.get("destination.photo_template", self.get("destination.folder_template", "{camera}/{yyyy}/{yyyy}-{mm}-{dd}"))
        
        # Parse date
        date_obj = datetime.strptime(file_date, "%Y-%m-%d")
        
        # Build replacement dict with all variants
        replacements = {
            # Camera and type
            "camera": camera_model,
            "type": file_type.capitalize(),
            
            # Year variants
            "yyyy": str(date_obj.year),
            "yy": str(date_obj.year)[2:],  # Last 2 digits
            
            # Month variants
            "mm": f"{date_obj.month:02d}",  # 01-12
            "m": str(date_obj.month),       # 1-12
            
            # Day variants
            "dd": f"{date_obj.day:02d}",    # 01-31
            "d": str(date_obj.day),         # 1-31
            
            # Full date
            "date": file_date,
            
            # Legacy support (deprecated but still work)
            "year": str(date_obj.year),
            "month": f"{date_obj.month:02d}",
            "day": f"{date_obj.day:02d}",
        }
        
        # Replace all placeholders
        folder = template
        for key, value in replacements.items():
            folder = folder.replace(f"{{{key}}}", value)
        
        return Path(base_path) / folder
        
    def is_file_type_allowed(self, filename: str) -> bool:
        """Check if file type is in allowed list"""
        ext = Path(filename).suffix.upper().lstrip(".")
        
        photos = self.get("file_types.photos", [])
        videos = self.get("file_types.videos", [])
        
        return ext in [p.upper() for p in photos] or ext in [v.upper() for v in videos]
        
    def get_file_type(self, filename: str) -> str:
        """Get file type (photo/video) from filename"""
        ext = Path(filename).suffix.upper().lstrip(".")
        
        photos = self.get("file_types.photos", [])
        videos = self.get("file_types.videos", [])
        
        if ext in [p.upper() for p in photos]:
            return "photo"
        elif ext in [v.upper() for v in videos]:
            return "video"
        else:
            return "unknown"


if __name__ == "__main__":
    # Test code
    logging.basicConfig(level=logging.INFO)
    
    config = Config()
    
    print(f"Base path: {config.get('destination.base_path')}")
    print(f"Template: {config.get('destination.folder_template')}")
    
    # Test path generation
    from datetime import datetime
    today = datetime.now().strftime("%Y-%m-%d")
    path = config.get_destination_path("ZV-E10", today, "photo")
    print(f"Generated path: {path}")
    
    # Test file type check
    print(f"DSC00001.ARW allowed: {config.is_file_type_allowed('DSC00001.ARW')}")
    print(f"test.txt allowed: {config.is_file_type_allowed('test.txt')}")
