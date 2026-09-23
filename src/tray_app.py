"""
Camera Connect - Main GUI Application
A standard desktop application with Dashboard, Manual Import, and Settings tabs.
"""
import sys
import os
import threading
import logging
import tkinter as tk
from tkinter import ttk, messagebox, filedialog, scrolledtext
from pathlib import Path
from datetime import datetime
import pythoncom
import pystray 
from PIL import Image
import winshell
from win32com.client import Dispatch

# Add src to path
sys.path.insert(0, str(Path(__file__).parent))

from main import CameraSyncCore
from config import Config
from backup_manager import CloudBackupManager

# Setup Logger
logger = logging.getLogger('gui')
logger.setLevel(logging.DEBUG)  # Enable debug logs

# Setup root logger for all modules
logging.basicConfig(
    level=logging.DEBUG,
    format='%(asctime)s - %(levelname)s - %(message)s',
    datefmt='%H:%M:%S'
)

class TextHandler(logging.Handler):
    """Logging handler that writes to a ScrolledText widget"""
    def __init__(self, text_widget):
        super().__init__()
        self.text_widget = text_widget

    def emit(self, record):
        try:
            msg = self.format(record)
            def append():
                try:
                    self.text_widget.configure(state='normal')
                    self.text_widget.insert('end', msg + '\n')
                    self.text_widget.see('end')
                    self.text_widget.configure(state='disabled')
                except:
                    pass
            self.text_widget.after(0, append)
        except:
            pass

class DashboardFrame(ttk.Frame):
    """Dashboard tab with Status and Logs"""
    def __init__(self, parent, app):
        super().__init__(parent)
        self.app = app
        self.pack(fill='both', expand=True, padx=10, pady=10)
        
        # Status Section
        status_frame = ttk.LabelFrame(self, text="Status", padding=10)
        status_frame.pack(fill='x', pady=(0, 10))
        
        self.status_var = tk.StringVar(value="Stopped")
        self.status_label = ttk.Label(status_frame, textvariable=self.status_var, font=('Segoe UI', 12, 'bold'))
        self.status_label.pack(side='left', padx=10)
        
        self.toggle_btn = ttk.Button(status_frame, text="Start Sync Service", command=self.toggle_service, width=20)
        self.toggle_btn.pack(side='right', padx=10)
        
        # Log Section
        log_frame = ttk.LabelFrame(self, text="Activity Log", padding=5)
        log_frame.pack(fill='both', expand=True)
        
        self.log_text = scrolledtext.ScrolledText(log_frame, height=15, state='disabled', font=('Segoe UI Emoji', 10))
        self.log_text.pack(fill='both', expand=True)
        
        # Setup logging
        handler = TextHandler(self.log_text)
        formatter = logging.Formatter('%(asctime)s - %(message)s', datefmt='%H:%M:%S')
        handler.setFormatter(formatter)
        
        # Capture root logger and set level to INFO
        root_logger = logging.getLogger()
        root_logger.setLevel(logging.INFO)
        root_logger.addHandler(handler)
        
    def toggle_service(self):
        if self.app.running:
            self.app.stop_sync()
            self.status_var.set("Stopped")
            self.status_label.config(foreground='red')
            self.toggle_btn.config(text="Start Sync Service")
        else:
            self.app.start_sync()
            self.status_var.set("Running...")
            self.status_label.config(foreground='green')
            self.toggle_btn.config(text="Stop Sync Service")

class ManualImportFrame(ttk.Frame):
    """Manual Import Tab"""
    def __init__(self, parent, app):
        super().__init__(parent)
        self.app = app
        self.pack(fill='both', expand=True, padx=10, pady=10)
        
        # Controls
        ctrl_frame = ttk.Frame(self)
        ctrl_frame.pack(fill='x', pady=(0, 10))
        
        self.scan_btn = ttk.Button(ctrl_frame, text="🔄 Scan for Cameras", command=self.scan_cameras)
        self.scan_btn.pack(side='left')
        
        # Camera Selection
        cam_frame = ttk.LabelFrame(self, text="Select Camera", padding=10)
        cam_frame.pack(fill='x', pady=(0, 10))
        
        self.camera_var = tk.StringVar()
        self.camera_combo = ttk.Combobox(cam_frame, textvariable=self.camera_var, state='readonly')
        self.camera_combo.pack(fill='x')
        self.camera_combo.bind("<<ComboboxSelected>>", self.on_camera_select)
        
        # Date Selection (Checkboxes list)
        list_frame = ttk.LabelFrame(self, text="Select Dates to Import", padding=10)
        list_frame.pack(fill='both', expand=True, pady=(0, 10))
        
        # Tools
        tools_frame = ttk.Frame(list_frame)
        tools_frame.pack(fill='x', pady=(0, 5))
        ttk.Button(tools_frame, text="✅ Select All", command=self.select_all).pack(side='left', padx=(0, 5))
        ttk.Button(tools_frame, text="⬜ Clear", command=self.clear_all).pack(side='left')

        self.canvas = tk.Canvas(list_frame)
        scrollbar = ttk.Scrollbar(list_frame, orient="vertical", command=self.canvas.yview)
        self.scrollable_frame = ttk.Frame(self.canvas)
        
        self.scrollable_frame.bind(
            "<Configure>",
            lambda e: self.canvas.configure(scrollregion=self.canvas.bbox("all"))
        )
        
        self.canvas.create_window((0, 0), window=self.scrollable_frame, anchor="nw")
        self.canvas.configure(yscrollcommand=scrollbar.set)
        
        self.canvas.pack(side="left", fill="both", expand=True)
        scrollbar.pack(side="right", fill="y")
        
        # Action Buttons
        btn_frame = ttk.Frame(self)
        btn_frame.pack(fill='x', pady=10)
        
        self.import_move_btn = ttk.Button(
            btn_frame, 
            text="✂️ Move Selected", 
            command=lambda: self.start_import(mode='move'), 
            state='disabled'
        )
        self.import_move_btn.pack(side='right', padx=5)
        
        self.import_copy_btn = ttk.Button(
            btn_frame, 
            text="📥 Copy Selected", 
            command=lambda: self.start_import(mode='copy'), 
            state='disabled'
        )
        self.import_copy_btn.pack(side='right')
        
        self.check_vars = {}
        self.camera_map = {} # model -> id

    def scan_cameras(self):
        # Allow scanning even if service loop not running (sync_app is initialized)
        if not self.app.sync_app:
             messagebox.showwarning("Error", "Sync Core not initialized.")
             return

        # Conflict Check: If Service is ON
        if self.app.running:
            answer = messagebox.askyesno(
                "Service Running", 
                "⚠️ Auto-Sync Service is RUNNING\n\n"
                "To prevent conflicts, the Auto-Sync Service must be STOPPED before Manual Scanning.\n\n"
                "Do you want to STOP the Service and proceed with the Scan?"
            )
            if answer:
                 self.app.dash_tab.toggle_service() # Trigger stop
                 self._wait_for_service_stop()
            return # Return wait or cancel

        self._perform_scan()

    def _wait_for_service_stop(self):
        if self.app.running:
             # Still stopping
             self.scan_btn.configure(state='disabled', text="Stopping Service...")
             self.after(500, self._wait_for_service_stop)
        else:
             # Stopped
             self.scan_btn.configure(state='normal', text="🔄 Scan for Cameras")
             self._perform_scan()

    def _perform_scan(self):
        # This triggers force scan if detector not running
        cameras = self.app.sync_app.get_connected_cameras()
        
        if not cameras:
            # Clear UI if no cameras
            self.camera_map = {}
            self.camera_combo['values'] = []
            self.camera_var.set('')
            self.on_camera_select(None)
            
            messagebox.showinfo("No Cameras", "No cameras found.\nPlease connect camera in MTP or Mass Storage mode.")
            return
            
        # Update list
        self.camera_map = {c['model']: c['id'] for c in cameras}
        new_models = list(self.camera_map.keys())
        self.camera_combo['values'] = new_models
        
        # Preserve selection if valid, else select first
        current = self.camera_var.get()
        if current in new_models:
            self.camera_combo.current(new_models.index(current))
        else:
            self.camera_combo.current(0)
            self.on_camera_select(None)
            
    def on_camera_select(self, event):
        model = self.camera_var.get()
        dev_id = self.camera_map.get(model)
        
        # Clear existing list
        for widget in self.scrollable_frame.winfo_children():
            widget.destroy()
        self.check_vars.clear()
        
        if not dev_id: 
            self.import_copy_btn.configure(state='disabled')
            self.import_move_btn.configure(state='disabled')
            return
        
        # Check Mass Storage for Move button
        # Move is risky and difficult on MTP, so we disable it
        if self.app.sync_app.is_camera_mass_storage(model):
            self.import_move_btn.configure(text="✂️ Move Selected")
            # State will be enabled in _show_dates if dates found
        else:
            self.import_move_btn.configure(state='disabled', text="✂️ Move (Mass Storage Only)")

        # Scan in background to keep UI responsive
        threading.Thread(target=self._scan_dates_bg, args=(dev_id,), daemon=True).start()
        
    def _scan_dates_bg(self, dev_id):
        try:
            pythoncom.CoInitialize() # Safety for COM in threads
            dates = self.app.sync_app.scan_camera_dates(dev_id)
            self.after(0, lambda: self._show_dates(dates))
        except Exception as e:
            logger.error(f"Scan failed: {e}")

    def _show_dates(self, dates):
        # Clear existing list first to prevent duplicates
        for widget in self.scrollable_frame.winfo_children():
            widget.destroy()
        self.check_vars.clear()

        if not dates:
            ttk.Label(self.scrollable_frame, text="No date folders found").pack(pady=5)
            self.import_copy_btn.configure(state='disabled')
            self.import_move_btn.configure(state='disabled')
            return
            
        for date_str in dates:
            var = tk.BooleanVar()
            self.check_vars[date_str] = var
            ttk.Checkbutton(self.scrollable_frame, text=date_str, variable=var).pack(anchor='w', pady=2, padx=5)
        
        self.import_copy_btn.configure(state='normal')
        
        # Only enable move if mass storage
        model = self.camera_var.get()
        if self.app.sync_app.is_camera_mass_storage(model):
            self.import_move_btn.configure(state='normal')
        else:
            self.import_move_btn.configure(state='disabled')
        
    def select_all(self):
        for var in self.check_vars.values():
            var.set(True)

    def clear_all(self):
        for var in self.check_vars.values():
            var.set(False)

    def start_import(self, mode='copy'):
        selected = [d for d, v in self.check_vars.items() if v.get()]
        if not selected:
            messagebox.showwarning("Selection", "Select at least one date folder.")
            return

        model = self.camera_var.get()
        dev_id = self.camera_map.get(model)
        
        if not dev_id: return
        
        # Check if this camera is actively being synced by Auto-Service
        if self.app.sync_app.is_syncing(dev_id):
             messagebox.showerror(
                 "Busy", 
                 f"Camera '{model}' is currently being Auto-Synced!\n"
                 "Please wait for the sync to finish or Stop the Service."
             )
             return
        
        if mode == 'move':
            if not messagebox.askyesno("Confirm Move", 
                "⚠️ Are you sure you want to MOVE files?\n\n"
                "This will DELETE files from the camera after copying.\n"
                "Ensure you have a backup of the destination drive."):
                return
        
        threading.Thread(target=self._import_bg, args=(dev_id, selected, mode), daemon=True).start()
        
    def _import_bg(self, dev_id, selected, mode):
        try:
            pythoncom.CoInitialize()
            count = self.app.sync_app.queue_manual_sync(dev_id, selected, mode=mode)
            
            # Refresh dates after import to show current status
            new_dates = self.app.sync_app.scan_camera_dates(dev_id)
            
            self.after(0, lambda: [
                messagebox.showinfo("Import Complete", f"Successfully imported {count} files."),
                self._show_dates(new_dates)
            ])
        except Exception as e:
            logger.error(f"Queue failed: {e}")
            self.after(0, lambda: messagebox.showerror("Import Failed", str(e)))

class SettingsFrame(ttk.Frame):
    """Settings Tab"""
    def __init__(self, parent, app):
        super().__init__(parent)
        self.app = app
        self.config = app.config
        self.pack(fill='both', expand=True, padx=10, pady=10)
        
        # Scroll container for settings if window is small
        self.main_canvas = tk.Canvas(self)
        scrollbar = ttk.Scrollbar(self, orient="vertical", command=self.main_canvas.yview)
        scroll_frame = ttk.Frame(self.main_canvas)
        
        scroll_frame.bind(
            "<Configure>",
            lambda e: self.main_canvas.configure(scrollregion=self.main_canvas.bbox("all"))
        )
        
        # LAYOUT FIX: Ensure inner frame expands to canvas width
        self.win_id = self.main_canvas.create_window((0, 0), window=scroll_frame, anchor="nw")
        self.main_canvas.bind("<Configure>", self._on_canvas_configure)
        
        self.main_canvas.configure(yscrollcommand=scrollbar.set)
        
        self.main_canvas.pack(side="left", fill="both", expand=True)
        scrollbar.pack(side="right", fill="y")
        
        # --- UI Build ---
        
        # DESTINATION
        dest_frame = ttk.LabelFrame(scroll_frame, text="📂 Destination Image", padding=10)
        dest_frame.pack(fill='x', pady=(0, 10))
        
        
        ttk.Label(dest_frame, text="Folder:").grid(row=0, column=0, sticky='w', pady=5)
        self.dest_var = tk.StringVar()
        ttk.Entry(dest_frame, textvariable=self.dest_var).grid(row=0, column=1, sticky='ew', padx=5)
        ttk.Button(dest_frame, text="...", width=3, command=self.browse).grid(row=0, column=2)
        
        ttk.Label(dest_frame, text="Photo Template:").grid(row=1, column=0, sticky='w', pady=5)
        self.photo_tpl_var = tk.StringVar()
        ttk.Entry(dest_frame, textvariable=self.photo_tpl_var).grid(row=1, column=1, sticky='ew', padx=5, columnspan=2)
        
        ttk.Label(dest_frame, text="Video Template:").grid(row=2, column=0, sticky='w', pady=5)
        self.video_tpl_var = tk.StringVar()
        ttk.Entry(dest_frame, textvariable=self.video_tpl_var).grid(row=2, column=1, sticky='ew', padx=5, columnspan=2)
        
        dest_frame.columnconfigure(1, weight=1)
        
        ttk.Label(dest_frame, text="Examples: {camera}/{yyyy}/{yyyy}-{mm}-{dd}/Video  or  Video", foreground='gray').grid(row=3, column=1, sticky='w', padx=5, columnspan=2)


        # MODE
        mode_frame = ttk.LabelFrame(scroll_frame, text="🔄 Sync Mode", padding=10)
        mode_frame.pack(fill='x', pady=(0, 10))
        
        self.mode_var = tk.StringVar()
        ttk.Radiobutton(mode_frame, text="Copy (Safer)", variable=self.mode_var, value="copy").pack(anchor='w')
        ttk.Radiobutton(mode_frame, text="Move (Delete from camera)", variable=self.mode_var, value="move").pack(anchor='w')
        
        # Overwrite option
        self.overwrite_var = tk.BooleanVar()
        ttk.Checkbutton(mode_frame, text="Overwrite existing files (no prompt)", variable=self.overwrite_var).pack(anchor='w', pady=(5,0))
        
        # Scan mode
        ttk.Label(mode_frame, text="Scan Mode:", font=('', 9, 'bold')).pack(anchor='w', pady=(10,2))
        self.scan_mode_var = tk.StringVar()
        ttk.Radiobutton(mode_frame, text="Scan Once (Stop after first sync)", variable=self.scan_mode_var, value="once").pack(anchor='w', padx=20)
        ttk.Radiobutton(mode_frame, text="Continuous (Keep monitoring for new files)", variable=self.scan_mode_var, value="continuous").pack(anchor='w', padx=20)
        
        # Poll interval
        poll_frame = ttk.Frame(mode_frame)
        poll_frame.pack(fill='x', pady=(5,0), padx=20)
        ttk.Label(poll_frame, text="Scan interval:").pack(side='left')
        self.poll_var = tk.IntVar()
        poll_spin = ttk.Spinbox(poll_frame, from_=1, to=300, textvariable=self.poll_var, width=10)
        poll_spin.pack(side='left', padx=5)
        ttk.Label(poll_frame, text="seconds (for Continuous mode)", foreground='gray').pack(side='left')
        
        # Tray option removed from here
        
        # EXTENSIONS
        ext_frame = ttk.LabelFrame(scroll_frame, text="📁 Extensions", padding=10)
        ext_frame.pack(fill='x', pady=(0, 10))
        
        ttk.Label(ext_frame, text="Photos:").grid(row=0, column=0, sticky='w')
        self.photo_var = tk.StringVar()
        ttk.Entry(ext_frame, textvariable=self.photo_var).grid(row=0, column=1, sticky='ew', padx=5)
        
        ttk.Label(ext_frame, text="Videos:").grid(row=1, column=0, sticky='w')
        self.video_var = tk.StringVar()
        ttk.Entry(ext_frame, textvariable=self.video_var).grid(row=1, column=1, sticky='ew', padx=5)
        ttk.Label(ext_frame, text="Videos:").grid(row=1, column=0, sticky='w')
        self.video_var = tk.StringVar()
        ttk.Entry(ext_frame, textvariable=self.video_var).grid(row=1, column=1, sticky='ew', padx=5)
        ext_frame.columnconfigure(1, weight=1)

        # STARTUP & BEHAVIOR
        startup_frame = ttk.LabelFrame(scroll_frame, text="⚙️ App Behavior & Startup", padding=10)
        startup_frame.pack(fill='x', pady=(0, 10))
        
        self.startup_var = tk.BooleanVar()
        self.chk_startup = ttk.Checkbutton(
            startup_frame, 
            text="Start with Windows", 
            variable=self.startup_var,
            command=self._toggle_startup_ui
        )
        self.chk_startup.pack(anchor='w')
        
        # Sub-options frame
        self.startup_sub_frame = ttk.Frame(startup_frame, padding=(20, 0, 0, 0))
        self.startup_sub_frame.pack(fill='x')
        
        self.startup_minimized_var = tk.BooleanVar()
        self.chk_minimized = ttk.Checkbutton(
            self.startup_sub_frame,
            text="Start Minimized (System Tray)",
            variable=self.startup_minimized_var
        )
        self.chk_minimized.pack(anchor='w')
        
        self.startup_sync_var = tk.BooleanVar()
        self.chk_sync = ttk.Checkbutton(
            self.startup_sub_frame,
            text="Auto-Start Sync Service",
            variable=self.startup_sync_var
        )
        self.chk_sync.pack(anchor='w')
        
        # General App Behavior
        ttk.Separator(startup_frame, orient='horizontal').pack(fill='x', pady=10)
        
        # Tray option (Moved here)
        self.minimize_var = tk.BooleanVar()
        ttk.Checkbutton(
            startup_frame, 
            text="Minimize to System Tray on close/minimize", 
            variable=self.minimize_var
        ).pack(anchor='w')

        # CLOUD BACKUP
        backup_frame = ttk.LabelFrame(scroll_frame, text="☁️ Cloud Backup (Google Drive)", padding=10)
        backup_frame.pack(fill='x', pady=(0, 10))
        
        # Enable Checkbox
        self.backup_enabled_var = tk.BooleanVar()
        ttk.Checkbutton(
            backup_frame, 
            text="Enable Auto-Upload to Cloud (One-way Sync)", 
            variable=self.backup_enabled_var,
            command=self._toggle_backup_ui
        ).grid(row=0, column=0, columnspan=2, sticky='w')
        
        # Remote Name
        ttk.Label(backup_frame, text="Rclone Remote:").grid(row=1, column=0, sticky='w', pady=5)
        self.remote_var = tk.StringVar()
        self.remote_entry = ttk.Entry(backup_frame, textvariable=self.remote_var, width=15)
        self.remote_entry.grid(row=1, column=1, sticky='w', padx=5)
        
        ttk.Label(backup_frame, text="(e.g., 'gdrive')").grid(row=1, column=2, sticky='w')
        
        # Remote Path
        ttk.Label(backup_frame, text="Cloud Folder:").grid(row=2, column=0, sticky='w', pady=5)
        self.remote_path_var = tk.StringVar()
        self.remote_path_entry = ttk.Entry(backup_frame, textvariable=self.remote_path_var)
        self.remote_path_entry.grid(row=2, column=1, sticky='ew', padx=5, columnspan=2)
        
        # Free Space Option
        self.backup_delete_var = tk.BooleanVar()
        ttk.Checkbutton(
            backup_frame, 
            text="🗑️ Free Up Space (Delete local files after successful upload)", 
            variable=self.backup_delete_var
        ).grid(row=3, column=0, columnspan=3, sticky='w', pady=(5,0))

        # Config Button
        ttk.Button(backup_frame, text="⚙️ Configure New Cloud Remote", command=self.configure_rclone).grid(row=4, column=0, columnspan=3, sticky='ew', pady=(10,0))
        
        backup_frame.columnconfigure(1, weight=1)

        # ACTIONS
        btn_frame = ttk.Frame(scroll_frame)
        btn_frame.pack(fill='x', pady=(20, 50)) # Added more bottom padding
        
        ttk.Button(btn_frame, text="💾 Save Configuration", command=self.save_config).pack(side='right', padx=(5, 20)) # Added right padding
        ttk.Button(btn_frame, text="🔄 Reload", command=self.load_ui).pack(side='right', padx=5)

        self.load_ui()
    
    def _toggle_startup_ui(self):
        state = 'normal' if self.startup_var.get() else 'disabled'
        self.chk_minimized.configure(state=state)
        self.chk_sync.configure(state=state)

    def _toggle_backup_ui(self):
        state = 'normal' if self.backup_enabled_var.get() else 'disabled'
        self.remote_entry.configure(state=state)
        self.remote_path_entry.configure(state=state)

    def configure_rclone(self):
        try:
            mgr = CloudBackupManager(self.config)
            if not mgr.is_installed():
                messagebox.showwarning("Rclone Missing", "rclone.exe not found.\nPlease download it and place it in the app folder.")
                return
            mgr.configure_remote()
        except Exception as e:
            messagebox.showerror("Error", str(e))
        
    def _on_canvas_configure(self, event):
        """Resize inner window to match canvas width"""
        self.main_canvas.itemconfig(self.win_id, width=event.width)

    def browse(self):
        d = filedialog.askdirectory()
        if d: self.dest_var.set(d)

    def load_ui(self):
        self.dest_var.set(self.config.get("destination.base_path", ""))
        self.photo_tpl_var.set(self.config.get("destination.photo_template", "{camera}/{yyyy}/{yyyy}-{mm}-{dd}"))
        self.video_tpl_var.set(self.config.get("destination.video_template", "{camera}/{yyyy}/{yyyy}-{mm}-{dd}"))
        self.mode_var.set(self.config.get("general.sync_mode", "copy"))
        self.overwrite_var.set(self.config.get("general.overwrite_existing", True))
        self.scan_mode_var.set(self.config.get("general.scan_mode", "continuous"))
        self.poll_var.set(self.config.get("general.poll_interval", 3))
        self.photo_var.set(", ".join(self.config.get("file_types.photos", [])))
        self.video_var.set(", ".join(self.config.get("file_types.videos", [])))
        self.minimize_var.set(self.config.get("general.minimize_to_tray", False))
        self.startup_var.set(self.config.get("general.start_with_windows", False))
        self.startup_minimized_var.set(self.config.get("general.startup_minimized", True))
        self.startup_sync_var.set(self.config.get("general.startup_auto_sync", False))
        self._toggle_startup_ui()
        
        self.backup_enabled_var.set(self.config.get("backup.enabled", False))
        self.remote_var.set(self.config.get("backup.remote_name", "drive"))
        self.remote_path_var.set(self.config.get("backup.remote_path", "CameraBackup"))
        self.backup_delete_var.set(self.config.get("backup.delete_local_after_upload", False))
        self._toggle_backup_ui()

    def save_config(self):
        try:
            # Validate Move mode
            if self.mode_var.get() == "move":
                response = messagebox.askyesno(
                    "Move Mode Warning",
                    "Move mode works best with Mass Storage (camera as USB drive).\n\n"
                    "With MTP mode, delete may:\n"
                    "• Show confirmation dialogs\n"
                    "• Be blocked by camera\n"
                    "• Require manual confirmation\n\n"
                    "Recommended: Use Copy mode for MTP.\n\n"
                    "Continue with Move mode?",
                    icon='warning'
                )
                if not response:
                    return  # Cancel save
            
            self.config.set("destination.base_path", self.dest_var.get())
            self.config.set("destination.photo_template", self.photo_tpl_var.get())
            self.config.set("destination.video_template", self.video_tpl_var.get())
            self.config.set("general.sync_mode", self.mode_var.get())
            self.config.set("general.overwrite_existing", self.overwrite_var.get())
            self.config.set("general.scan_mode", self.scan_mode_var.get())
            self.config.set("general.poll_interval", self.poll_var.get())
            self.config.set("general.minimize_to_tray", self.minimize_var.get())
            self.config.set("general.start_with_windows", self.startup_var.get())
            self.config.set("general.startup_minimized", self.startup_minimized_var.get())
            self.config.set("general.startup_auto_sync", self.startup_sync_var.get())
            
            # Manage Shortcut
            self.app.manage_startup_shortcut(self.startup_var.get())
            
            p = [x.strip() for x in self.photo_var.get().split(',') if x.strip()]
            v = [x.strip() for x in self.video_var.get().split(',') if x.strip()]
            self.config.set("file_types.photos", p)
            self.config.set("file_types.videos", v)
            
            # Backup
            self.config.set("backup.enabled", self.backup_enabled_var.get())
            self.config.set("backup.remote_name", self.remote_var.get())
            self.config.set("backup.remote_path", self.remote_path_var.get())
            self.config.set("backup.delete_local_after_upload", self.backup_delete_var.get())
            
            self.config.save()
            messagebox.showinfo("Saved", "Configuration saved successfully!")
        except Exception as e:
            messagebox.showerror("Error", str(e))

class CameraConnectApp(tk.Tk):
    def __init__(self):
        super().__init__()
        self.title("Camera Connect")
        self.geometry("800x600")
        
        try:
            # Set taskbar icon if available
            self.iconbitmap("camera.ico") 
        except:
            pass

        self.config = Config()
        self.running = False
        self.sync_app = CameraSyncCore(config=self.config) # Initialize core logic immediately

        # Main Layout: Tabs
        self.notebook = ttk.Notebook(self)
        self.notebook.pack(fill='both', expand=True, padx=5, pady=5)
        
        self.dash_tab = DashboardFrame(self.notebook, self)
        self.import_tab = ManualImportFrame(self.notebook, self)
        self.settings_tab = SettingsFrame(self.notebook, self)
        
        self.notebook.add(self.dash_tab, text="  📊 Dashboard  ")
        self.notebook.add(self.import_tab, text="  📸 Manual Import  ")
        self.notebook.add(self.settings_tab, text="  ⚙️ Settings  ")
        
        # Handle Exit
        self.protocol("WM_DELETE_WINDOW", self.on_close)
        
        # Handle Minimize to Tray
        self.bind("<Unmap>", self._on_unmap)
        self.tray_icon = None
        
        # Auto-start logic (Check if launched with argument or just config)
        if len(sys.argv) > 1 and sys.argv[1] == "--startup":
             logger.info("🚀 Launched from Search/Startup")
             
             # 1. Window State (Minimized vs Normal)
             if self.config.get("general.startup_minimized", True):
                 self.withdraw()
                 self._show_tray_icon()
                 logger.info("   -> Minimized to Tray")
             
             # 2. Service State (Auto-Start Sync)
             if self.config.get("general.startup_auto_sync", False):
                 logger.info("   -> Auto-Starting Service")
                 self.dash_tab.toggle_service() 

    def manage_startup_shortcut(self, enable):
        """Create or delete shortcut in Startup folder"""
        try:
            startup_folder = winshell.startup()
            shortcut_path = os.path.join(startup_folder, "CameraConnect.lnk")
            
            if enable:
                target = sys.executable
                wDir = os.path.dirname(target)
                
                shell = Dispatch('WScript.Shell')
                shortcut = shell.CreateShortCut(shortcut_path)
                shortcut.Targetpath = target
                shortcut.WorkingDirectory = wDir
                shortcut.Arguments = "--startup"
                shortcut.IconLocation = target
                shortcut.save()
            else:
                if os.path.exists(shortcut_path):
                    os.remove(shortcut_path)
                    
        except Exception as e:
            logger.error(f"Startup Shortcut Error: {e}")

    def _on_unmap(self, event):
        if str(event.widget) == ".": # Check if it's main window
            if self.state() == 'iconic':
                if self.config.get("general.minimize_to_tray", False):
                     # Minimize to tray
                     self.withdraw()
                     self._show_tray_icon()

    def _show_tray_icon(self):
        try:
            image = Image.open("camera.ico")
        except:
            # Fallback simple icon
            image = Image.new('RGB', (64, 64), color=(30, 144, 255))
            
        menu = pystray.Menu(
            pystray.MenuItem("Dashboard", self._show_window, default=True),
            pystray.Menu.SEPARATOR,
            pystray.MenuItem(
                "Start Sync Service", 
                self._tray_start_sync,
                enabled=lambda item: not self.running
            ),
            pystray.MenuItem(
                "Stop Sync Service", 
                self._tray_stop_sync,
                enabled=lambda item: self.running
            ),
            pystray.Menu.SEPARATOR,
            pystray.MenuItem("Exit", self._quit_from_tray)
        )
        self.tray_icon = pystray.Icon("CameraConnect", image, "Camera Connect", menu)
        threading.Thread(target=self.tray_icon.run, daemon=True).start()
    
    def _tray_start_sync(self, icon, item):
        self.after(0, self.dash_tab.toggle_service)
        
    def _tray_stop_sync(self, icon, item):
        self.after(0, self.dash_tab.toggle_service)
        
    def _show_window(self, icon=None, item=None):
        if self.tray_icon:
            self.tray_icon.stop()
        self.after(0, self.deiconify)
        
    def _quit_from_tray(self, icon=None, item=None):
        if self.tray_icon:
            self.tray_icon.stop()
        self.after(0, self.on_close_tray)

    def on_close_tray(self):
        # Force close without confirm loop from tray
        if self.running:
             self.stop_sync()
        self.destroy()
        sys.exit(0)

    def start_sync(self):
        # Only start the loop thread if not running
        if not self.running:
            threading.Thread(target=self._run_start_sync, daemon=True).start()
            self.running = True

    def _run_start_sync(self):
        try:
            pythoncom.CoInitialize() # Initialize COM for this thread
            self.sync_app.start() # This starts the monitor loop
            logger.info("Service Started Successfully.")
        except Exception as e:
            logger.error(f"❌ Failed to start service: {e}")
            self.running = False # Reset flag

    def stop_sync(self):
        if self.running:
            # Run stop in background to avoid UI freeze
            def _stop():
                try:
                    self.sync_app.stop()
                    self.running = False
                    logger.info("Service Stopped.")
                except Exception as e:
                    logger.error(f"Error stopping service: {e}")
            
            threading.Thread(target=_stop, daemon=True).start()

    def on_close(self):
        if self.tray_icon:
            try: self.tray_icon.stop()
            except: pass

        if self.running:
            if messagebox.askyesno("Exit", "Sync service is running. Stop and exit?"):
                self.stop_sync()
                self.destroy()
        else:
            self.destroy()
        sys.exit(0)

if __name__ == "__main__":
    app = CameraConnectApp()
    app.mainloop()
