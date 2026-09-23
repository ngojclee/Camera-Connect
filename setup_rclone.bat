@echo off
echo Downloading Rclone...
powershell -Command "Invoke-WebRequest -Uri 'https://downloads.rclone.org/rclone-current-windows-amd64.zip' -OutFile 'rclone.zip'"

echo Extracting...
powershell -Command "Expand-Archive -Path rclone.zip -DestinationPath rclone_tmp -Force"

echo Installing...
for /r "rclone_tmp" %%f in (rclone.exe) do copy "%%f" .

echo Cleaning up...
del rclone.zip
rmdir /s /q rclone_tmp

echo.
echo Rclone installed successfully!
pause
