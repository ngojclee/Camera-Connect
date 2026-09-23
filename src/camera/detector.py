"""
USB Camera Detector
Phát hiện khi Sony camera được cắm/rút qua USB
"""
import logging
import threading
import time
from typing import Callable, Optional, Dict
import wmi

logger = logging.getLogger(__name__)

# Sony USB Vendor ID
SONY_VENDOR_ID = "054C"

# Known Sony Camera Product IDs
# Sony Camera USB Product IDs (PIDs)
# Source: Linux kernel, libgphoto2, user reports
SONY_CAMERA_PIDS = {
    # Alpha Series (Full Frame)
    "0C00": "A7R III",     # MTP mode
    "0C3B": "A7R III",     # Mass Storage
    "0BFF": "A7R III",     # Variant (User reported)
    "0BCC": "A7 III",      # MTP mode
    "0BCD": "A7 III",      # Mass Storage
    "0D49": "A7 IV",       # MTP mode
    "0D4A": "A7 IV",       # Mass Storage
    "0C5E": "A7R IV",      # MTP mode
    "0C5F": "A7R IV",      # Mass Storage
    "0E5C": "A7R V",       # MTP mode
    "0E5D": "A7R V",       # Mass Storage
    "0D8E": "A7S III",     # MTP mode
    "0D8F": "A7S III",     # Mass Storage
    "0B43": "A7R II",      # MTP mode
    "0B44": "A7R II",      # Mass Storage
    "0AD3": "A7R",         # MTP mode
    "0AD4": "A7R",         # Mass Storage
    "0A37": "A7",          # MTP mode
    "0A38": "A7",          # Mass Storage
    "0BCE": "A9",          # MTP mode
    "0BCF": "A9",          # Mass Storage
    "0D8A": "A9 II",       # MTP mode
    "0D8B": "A9 II",       # Mass Storage
    
    # Alpha Series (APS-C)
    "0BC7": "A6000",       # MTP mode
    "0BC8": "A6000",       # Mass Storage
    "0C18": "A6300",       # MTP mode
    "0C19": "A6300",       # Mass Storage
    "0C5A": "A6400",       # MTP mode
    "0C5B": "A6400",       # Mass Storage
    "0C1E": "A6500",       # MTP mode
    "0C1F": "A6500",       # Mass Storage
    "0D8C": "A6600",       # MTP mode
    "0D8D": "A6600",       # Mass Storage
    
    # ZV Series (Vlog)
    "0D96": "ZV-E10",      # MTP mode
    "0D95": "ZV-E10",      # Mass Storage mode
    "0E54": "ZV-E10 II",   # MTP mode
    "0E55": "ZV-E10 II",   # Mass Storage
    "0D43": "ZV-1",        # MTP mode
    "0D44": "ZV-1",        # Mass Storage
    "0E50": "ZV-1 II",     # MTP mode
    "0E51": "ZV-1 II",     # Mass Storage
    "0E3C": "ZV-E1",       # MTP mode
    "0E3D": "ZV-E1",       # Mass Storage
    
    # NEX Series (Legacy - Complete List)
    "08B4": "NEX-5",       # MTP mode
    "08B5": "NEX-5",       # Mass Storage
    "08B6": "NEX-3",       # MTP mode
    "08B7": "NEX-3",       # Mass Storage
    "08E2": "NEX-C3",      # MTP mode
    "08E3": "NEX-C3",      # Mass Storage
    "0946": "NEX-5N",      # MTP mode
    "0947": "NEX-5N",      # Mass Storage
    "09C3": "NEX-7",       # MTP mode
    "09C4": "NEX-7",       # Mass Storage
    "0993": "NEX-F3",      # MTP mode
    "0994": "NEX-6",       # MTP mode
    "0995": "NEX-6",       # Mass Storage
    "0A5A": "NEX-5R",      # MTP mode (variant 1)
    "0A5B": "NEX-5R",      # Mass Storage (variant 1)
    "0946": "NEX-5R",      # MTP mode (variant 2 - same as 5N)
    "0947": "NEX-5R",      # Mass Storage (variant 2)
    "066F": "NEX-5R",      # Alt PID - verified on actual device
    "0A5C": "NEX-5T",      # MTP mode
    "0A5D": "NEX-5T",      # Mass Storage
    "0A37": "NEX-3N",      # MTP mode
    "0A38": "NEX-3N",      # Mass Storage
    
    # RX Series (Compact)
    "0A5B": "RX100 III",   # MTP mode
    "0A5C": "RX100 III",   # Mass Storage
    "0BCA": "RX100 IV",    # MTP mode
    "0BCB": "RX100 IV",    # Mass Storage
    "0C1A": "RX100 V",     # MTP mode
    "0C1B": "RX100 V",     # Mass Storage
    "0D8": "RX100 VI",     # MTP mode
    "0D9": "RX100 VI",     # Mass Storage
    "0C3E": "RX100 VII",   # MTP mode
    "0C3F": "RX100 VII",   # Mass Storage
    
    # Add more as needed
}


class CameraDetector:
    """Detect Sony cameras via USB hotplug events"""
    
    def __init__(self):
        self._running = False
        self._thread: Optional[threading.Thread] = None
        self._on_connect: Optional[Callable] = None
        self._on_disconnect: Optional[Callable] = None
        self._connected_cameras: Dict[str, str] = {}  # {device_id: model_name}
        
    def set_callbacks(self, on_connect: Callable = None, on_disconnect: Callable = None):
        """
        Set callback functions for camera events
        
        Args:
            on_connect: Called with (device_id, model_name) when camera connected
            on_disconnect: Called with (device_id, model_name) when camera disconnected
        """
        self._on_connect = on_connect
        self._on_disconnect = on_disconnect
        
    def start(self):
        """Start monitoring for USB devices"""
        if self._running:
            logger.warning("Detector already running")
            return
            
        self._running = True
        self._thread = threading.Thread(target=self._monitor_loop, daemon=True)
        self._thread.start()
        logger.info("Camera detector started")
        
        # Initial scan
        self._scan_existing_cameras()
        
    def stop(self):
        """Stop monitoring"""
        self._running = False
        if self._thread:
            self._thread.join(timeout=5)
        # Clear state to allow re-detection on next start
        self._connected_cameras.clear()
        logger.info("Camera detector stopped")
        
    def _scan_existing_cameras(self):
        """Scan and update connected cameras list"""
        try:
            # Init WMI connection
            w = wmi.WMI()
            current_scan = {}
            
            # Get all PnP devices
            for device in w.Win32_PnPEntity():
                try:
                    # Check if device has DeviceID attribute
                    if not hasattr(device, 'DeviceID'):
                        continue
                    
                    device_id = device.DeviceID
                    if not device_id:
                        continue
                    
                    # Check if it's a Sony USB device
                    if f"VID_{SONY_VENDOR_ID}" not in str(device_id):
                        continue
                    
                    # Log all Sony devices found
                    # logger.info(f"Found Sony USB device: {device_id}") # Too noisy for periodic scan
                        
                    model_base = self._identify_camera(device_id)
                    
                    # Accept ALL Sony devices (VID_054C), even unknown PIDs
                    if model_base is None:
                        # Unknown PID - still accept as generic Sony Camera
                        model_base = "Sony Camera"
                    
                    # Try to get friendly name
                    friendly = getattr(device, 'Caption', '')
                    final_name = model_base
                    
                    if friendly and friendly != model_base:
                         # Allow generic names like "USB Mass Storage Device" as requested by user
                         # This helps identify connection mode
                         
                         should_append = True
                         
                         # Only skip if friendly name is EXACTLY the model name or just "Sony"
                         if model_base.lower() in friendly.lower():
                             # friendly already contains model name, use friendly as full name
                             final_name = friendly
                             should_append = False
                         
                         if friendly.strip() == "Sony":
                             should_append = False
                         
                         if should_append:
                              final_name = f"{model_base} ({friendly})"
                    
                    current_scan[device_id] = final_name

                    # New connection
                    if device_id not in self._connected_cameras:
                        self._connected_cameras[device_id] = final_name
                        # Extract PID for logging
                        pid_match = device_id.upper().split("PID_")[1].split("\\")[0] if "PID_" in device_id.upper() else "Unknown"
                        is_known = SONY_CAMERA_PIDS.get(pid_match) is not None
                        status_icon = "✅" if is_known else "⚠️"
                        logger.info(f"{status_icon} Sony camera detected (PID: {pid_match}): {final_name}")
                        
                        if self._on_connect:
                            self._on_connect(device_id, final_name)
                            
                except AttributeError:
                    continue
                except Exception as e:
                    logger.debug(f"Error checking device: {e}")
                    continue
            
            # Check for disconnections (Device in cache but not in current scan)
            disconnected_ids = [did for did in self._connected_cameras if did not in current_scan]
            
            for did in disconnected_ids:
                model = self._connected_cameras.pop(did)
                logger.info(f"❌ Camera disconnected: {model} ({did})")
                if self._on_disconnect:
                    self._on_disconnect(did, model)
                        
        except Exception as e:
            logger.error(f"Error scanning existing cameras: {e}")
            
    def _monitor_loop(self):
        """Main monitoring loop using polling (WMI events can be unreliable)"""
        import pythoncom
        
        # Initialize COM for this thread
        pythoncom.CoInitialize()
        
        logger.info("Starting camera monitor loop")
        
        try:
            while self._running:
                try:
                    # Init WMI connection for this iteration
                    w = wmi.WMI()
                    
                    current_cameras = {}
                    
                    # Scan all PnP devices
                    for device in w.Win32_PnPEntity():
                        try:
                            # Check if device has DeviceID attribute
                            if not hasattr(device, 'DeviceID'):
                                continue
                            
                            device_id = device.DeviceID
                            if not device_id:
                                continue
                            
                            # Check if it's a Sony USB device
                            if f"VID_{SONY_VENDOR_ID}" not in str(device_id):
                                continue
                            
                            # Log Sony device found (only once per scan to avoid spam)
                            logger.debug(f"Scanning Sony device: {device_id}")
                                
                            model_base = self._identify_camera(device_id)
                            
                            # Accept ALL Sony devices (VID_054C), even unknown PIDs
                            if model_base is None:
                                # Unknown PID - still accept as generic Sony Camera
                                model_base = "Sony Camera"
                            
                            # Try to get friendly name
                            friendly = getattr(device, 'Caption', '')
                            final_name = model_base
                            
                            if friendly and friendly != model_base:
                                 # Allow generic names as requested
                                 should_append = True
                                 
                                 if model_base.lower() in friendly.lower():
                                     final_name = friendly
                                     should_append = False
                                 
                                 if friendly.strip() == "Sony":
                                     should_append = False
                                 
                                 if should_append:
                                      final_name = f"{model_base} ({friendly})"

                            current_cameras[device_id] = final_name
                                
                        except AttributeError:
                            # Skip devices without DeviceID
                            continue
                        except Exception as e:
                            logger.debug(f"Error checking device: {e}")
                            continue
                    
                    # Check for new connections
                    for device_id, model in current_cameras.items():
                        if device_id not in self._connected_cameras:
                            # Extract PID for logging
                            pid_match = device_id.upper().split("PID_")[1].split("\\")[0] if "PID_" in device_id.upper() else "Unknown"
                            is_known = SONY_CAMERA_PIDS.get(pid_match) is not None
                            status_icon = "✅" if is_known else "⚠️"
                            logger.info(f"{status_icon} Sony camera detected (PID: {pid_match}): {model}")
                            self._connected_cameras[device_id] = model
                            
                            if self._on_connect:
                                try:
                                    self._on_connect(device_id, model)
                                except Exception as e:
                                    logger.error(f"Error in on_connect callback: {e}")
                    
                    # Check for disconnections
                    disconnected = set(self._connected_cameras.keys()) - set(current_cameras.keys())
                    for device_id in disconnected:
                        model = self._connected_cameras.pop(device_id)
                        logger.info(f"Camera disconnected: {model}")
                        
                        if self._on_disconnect:
                            try:
                                self._on_disconnect(device_id, model)
                            except Exception as e:
                                logger.error(f"Error in on_disconnect callback: {e}")
                    
                    # Poll every 2 seconds
                    time.sleep(2)
                    
                except Exception as e:
                    logger.error(f"Error in monitor loop: {e}")
                    time.sleep(5)  # Wait longer on error
                    
        finally:
            # Uninitialize COM
            pythoncom.CoUninitialize()
                
    def _identify_camera(self, device_id: str) -> Optional[str]:
        """
        Identify camera model from device ID
        
        Args:
            device_id: Windows device ID string
            
        Returns:
            Model name or None if not a known camera
        """
        try:
            # Extract PID from device ID
            # Format: USB\VID_054C&PID_0946\...
            if "PID_" not in device_id:
                return None
                
            pid_start = device_id.index("PID_") + 4
            pid = device_id[pid_start:pid_start + 4].upper()
            
            return SONY_CAMERA_PIDS.get(pid)
            
        except Exception as e:
            logger.error(f"Error identifying camera: {e}")
            return None
            
    def get_connected_cameras(self) -> Dict[str, str]:
        """Get currently connected cameras"""
        return self._connected_cameras.copy()


if __name__ == "__main__":
    # Test code
    logging.basicConfig(
        level=logging.INFO,
        format='%(asctime)s - %(name)s - %(levelname)s - %(message)s'
    )
    
    def on_connect(device_id, model):
        print(f"✅ Camera connected: {model}")
        print(f"   Device ID: {device_id}")
        
    def on_disconnect(device_id, model):
        print(f"❌ Camera disconnected: {model}")
        
    detector = CameraDetector()
    detector.set_callbacks(on_connect, on_disconnect)
    detector.start()
    
    print("Monitoring for Sony cameras... Press Ctrl+C to stop")
    try:
        while True:
            time.sleep(1)
    except KeyboardInterrupt:
        print("\nStopping...")
        detector.stop()
