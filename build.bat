@echo off
echo ==========================================
echo Building Camera Connect (Clean & Force)
echo ==========================================


echo 1. Deep Cleaning...
if exist build rmdir /s /q build
if exist dist rmdir /s /q dist
if exist CameraConnect_Portable rmdir /s /q CameraConnect_Portable

echo.
echo 2. Checking Dependencies...
if not exist rclone.exe (
    echo [WARNING] rclone.exe not found!
    if exist setup_rclone.bat (
        echo Found setup_rclone.bat. Attempting to auto-download...
        call setup_rclone.bat
    ) else (
        echo [ERROR] setup_rclone.bat not found. Please download rclone manualy.
    )
)

if not exist rclone.exe (
    echo [WARNING] Still no rclone.exe. Cloud backup feature will not be bundled.
    timeout /t 5
)

if exist venv\Scripts\activate.bat (
    call venv\Scripts\activate.bat
    echo Activated venv
) else if exist .venv\Scripts\activate.bat (
    call .venv\Scripts\activate.bat
    echo Activated .venv
)

echo.
echo.
echo 3. Checking critical libs...
python -m pip install -r requirements.txt
python -m pip install pyinstaller

echo.
echo 4. Building EXE...
python -m PyInstaller CameraConnect.spec --clean --noconfirm

echo.
echo 5. Packaging...
mkdir CameraConnect_Portable
if exist dist\CameraConnect.exe copy dist\CameraConnect.exe CameraConnect_Portable\

echo Copying resources...
if exist CameraConnectConfig.yaml copy CameraConnectConfig.yaml CameraConnect_Portable\
if exist rclone.exe copy rclone.exe CameraConnect_Portable\

echo.
echo ==========================================
echo BUILD COMPLETE!
echo Check folder: CameraConnect_Portable
echo ==========================================

echo.
echo 6. Building Installer (Optional)...
set "ISCC=C:\Program Files (x86)\Inno Setup 6\ISCC.exe"

if exist "%ISCC%" (
    echo Found Inno Setup Compiler. Building installer...
    "%ISCC%" "installer\setup_script.iss"
    
    if errorlevel 1 (
        echo [ERROR] Failed to build installer.
    ) else (
        echo.
        echo ==========================================
        echo INSTALLER BUILD SUCCESSFUL!
        echo Check folder: installer\Output
        echo ==========================================
    )
) else (
    echo [INFO] Inno Setup not found at "%ISCC%"
    echo To build a setup.exe installer:
    echo 1. Download & Install Inno Setup: https://jrsoftware.org/isdl.php
    echo 2. Run this build script again.
)
echo.
