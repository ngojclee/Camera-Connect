; Script created by Antigravity
; SEE THE DOCUMENTATION FOR DETAILS ON CREATING INNO SETUP SCRIPT FILES!

#define MyAppName "Camera Connect"
#define MyAppVersion "1.4.1"
#define MyAppPublisher "Le Ngoc"
#define MyAppURL "https://github.com/yourusername/camera-connect"
#define MyAppExeName "CameraConnect.exe"
#define MyAppConfigName "CameraConnectConfig.yaml"

[Setup]
; NOTE: The value of AppId uniquely identifies this application. Do not use the same AppId value in installers for other applications.
; (To generate a new GUID, click Tools | Generate GUID inside the IDE.)
AppId={{A1B2C3D4-E5F6-7890-1234-567890ABCDEF}
AppName={#MyAppName}
AppVersion={#MyAppVersion}
;AppVerName={#MyAppName} {#MyAppVersion}
AppPublisher={#MyAppPublisher}
AppPublisherURL={#MyAppURL}
AppSupportURL={#MyAppURL}
AppUpdatesURL={#MyAppURL}
DefaultDirName={autopf}\{#MyAppName}
DisableProgramGroupPage=yes
; Remove the following line to run in administrative install mode (install for all users.)
PrivilegesRequired=lowest
OutputDir=Output
OutputBaseFilename=Setup_CameraConnect_{#MyAppVersion}
;SetupIconFile=..\src\assets\icon.ico
Compression=lzma
SolidCompression=yes
WizardStyle=modern

[Languages]
Name: "english"; MessagesFile: "compiler:Default.isl"

[Tasks]
Name: "desktopicon"; Description: "{cm:CreateDesktopIcon}"; GroupDescription: "{cm:AdditionalIcons}"; Flags: unchecked
Name: "startup"; Description: "Start application when Windows starts"; GroupDescription: "{cm:AdditionalIcons}"; Flags: unchecked

[Files]
; Main Executable
Source: "..\CameraConnect_Portable\{#MyAppExeName}"; DestDir: "{app}"; Flags: ignoreversion
; Config File
Source: "..\CameraConnect_Portable\{#MyAppConfigName}"; DestDir: "{app}"; Flags: ignoreversion
; Rclone (Optional but included in portable build)
Source: "..\CameraConnect_Portable\rclone.exe"; DestDir: "{app}"; Flags: ignoreversion skipifsourcedoesntexist

; NOTE: Don't use "Flags: ignoreversion" on any shared system files

[Icons]
Name: "{autoprograms}\{#MyAppName}"; Filename: "{app}\{#MyAppExeName}"
Name: "{autodesktop}\{#MyAppName}"; Filename: "{app}\{#MyAppExeName}"; Tasks: desktopicon
Name: "{userstartup}\{#MyAppName}"; Filename: "{app}\{#MyAppExeName}"; Tasks: startup

[Run]
Filename: "{app}\{#MyAppExeName}"; Description: "{cm:LaunchProgram,{#StringChange(MyAppName, '&', '&&')}}"; Flags: nowait postinstall skipifsilent

[UninstallDelete]
; Clean up config files created after installation if they are in the app folder
Type: files; Name: "{app}\*.log"
Type: files; Name: "{app}\*.db"
