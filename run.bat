@echo off
REM Sony Camera Auto-Sync - System Tray App
REM Run this file to start the application

echo ========================================
echo   Sony Camera Auto-Sync
echo ========================================
echo.
echo Starting application...
echo.

REM Activate virtual environment and run
call venv\Scripts\activate.bat
python src\tray_app.py

REM If error, pause to see message
if errorlevel 1 (
    echo.
    echo ERROR: Failed to start application!
    echo.
    pause
)
