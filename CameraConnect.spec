# -*- mode: python ; coding: utf-8 -*-
from PyInstaller.utils.hooks import collect_all
import os

block_cipher = None

# Dynamically find pystray
import pystray
datas = []
binaries = []
hiddenimports = [
    'six', 
    'pystray', 
    'pystray._util', 
    'pystray._base',
    'pystray.backends', 
    'pystray.backends.win32',
    'pythoncom', 
    'wmi',
    'win32com',
    'win32api',
    'win32timezone',
    'PIL', 
    'PIL.Image', 
    'PIL._tkinter_finder'
]

pystray_path = os.path.dirname(pystray.__file__)
if os.path.exists(pystray_path):
    datas.append((pystray_path, 'pystray'))

# 2. Collect All (Safety Net)
for lib in ['pystray', 'PIL', 'wmi', 'six']:
    try:
        tmp_ret = collect_all(lib)
        datas += tmp_ret[0]
        binaries += tmp_ret[1]
        hiddenimports += tmp_ret[2]
    except Exception:
        pass

# 3. Add Config file (named after executable)
if os.path.exists('CameraConnectConfig.yaml'):
    datas.append(('CameraConnectConfig.yaml', '.'))

a = Analysis(
    ['src\\tray_app.py'],
    pathex=['.', 'src'],
    binaries=binaries,
    datas=datas,
    hiddenimports=hiddenimports,
    hookspath=[],
    hooksconfig={},
    runtime_hooks=[],
    excludes=[],
    win_no_prefer_redirects=False,
    win_private_assemblies=False,
    cipher=block_cipher,
    noarchive=False,
)
pyz = PYZ(a.pure, a.zipped_data, cipher=block_cipher)


# Check for rclone
if os.path.exists('rclone.exe'):
    binaries.append(('rclone.exe', '.'))

exe = EXE(
    pyz,
    a.scripts,
    a.binaries,
    a.zipfiles,
    a.datas,
    [],
    name='CameraConnect',
    debug=False,
    bootloader_ignore_signals=False,
    strip=False,
    upx=True,
    upx_exclude=[],
    runtime_tmpdir=None,
    console=False,
    disable_windowed_traceback=False,
    argv_emulation=False,
    target_arch=None,
    codesign_identity=None,
    entitlements_file=None,
)
