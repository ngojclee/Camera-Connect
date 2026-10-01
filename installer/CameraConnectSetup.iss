#ifndef AppVersion
  #define AppVersion "3.0.0.7"
#endif

#ifndef SourceBinDir
  #define SourceBinDir "..\\build\\bin"
#endif

#ifndef OutputDir
  #define OutputDir "..\\build\\installer"
#endif

#ifndef UIRuntime
  #define UIRuntime "harness"
#endif

#ifndef UIRuntimeRequested
  #define UIRuntimeRequested UIRuntime
#endif

#ifndef UIBinaryName
  #define UIBinaryName "CameraConnect.exe"
#endif

[Setup]
AppId={{3C9D7D7F-6942-461D-A2AC-286668079728}
AppName=Camera Connect
AppVersion={#AppVersion}
AppVerName=Camera Connect {#AppVersion}
DefaultDirName={autopf64}\Camera Connect
DisableDirPage=no
DefaultGroupName=Camera Connect
DisableProgramGroupPage=yes
UninstallDisplayIcon={app}\{#UIBinaryName}
OutputDir={#OutputDir}
OutputBaseFilename=CameraConnectSetup-v{#AppVersion}-windows-amd64
SetupIconFile=..\assets\icon.ico
Compression=lzma2
SolidCompression=yes
ArchitecturesAllowed=x64compatible
ArchitecturesInstallIn64BitMode=x64compatible
PrivilegesRequired=admin
WizardStyle=modern
UsePreviousAppDir=yes
UsePreviousTasks=yes
CloseApplications=force
RestartApplications=no
AppMutex=CameraConnectAgent_Mutex,CameraConnectUI_Mutex

[Languages]
Name: "english"; MessagesFile: "compiler:Default.isl"

[Tasks]
Name: "desktopicon"; Description: "Create a desktop shortcut"; GroupDescription: "Additional shortcuts"

[Files]
Source: "{#SourceBinDir}\CameraConnectAgent.exe"; DestDir: "{app}"; Flags: ignoreversion
Source: "{#SourceBinDir}\{#UIBinaryName}"; DestDir: "{app}"; Flags: ignoreversion
Source: "{#SourceBinDir}\build-metadata.json"; DestDir: "{app}"; Flags: ignoreversion skipifsourcedoesntexist

[Icons]
Name: "{autoprograms}\Camera Connect"; Filename: "{app}\{#UIBinaryName}"; WorkingDir: "{app}"
Name: "{autodesktop}\Camera Connect"; Filename: "{app}\{#UIBinaryName}"; WorkingDir: "{app}"; Tasks: desktopicon

[Registry]
; Remove the stale HKLM autostart value older installers wrote — startup is
; owned by the agent's "Start with Windows" setting (HKCU Run key).
Root: HKA; Subkey: "Software\Microsoft\Windows\CurrentVersion\Run"; ValueName: "CameraConnect"; Flags: deletevalue

[Run]
Filename: "{app}\CameraConnectAgent.exe"; Parameters: "--minimized"; Flags: nowait postinstall skipifsilent runasoriginaluser
Filename: "{app}\{#UIBinaryName}"; Description: "Launch Camera Connect"; Flags: nowait postinstall skipifsilent runasoriginaluser

[Code]
function TryStopProcess(const ImageName: string; const ForceKill: Boolean): Boolean;
var
  ResultCode: Integer;
  Params: string;
begin
  if ForceKill then
    Params := '/IM "' + ImageName + '" /T /F'
  else
    Params := '/IM "' + ImageName + '" /T';

  Result := Exec(ExpandConstant('{sys}\taskkill.exe'), Params, '', SW_HIDE, ewWaitUntilTerminated, ResultCode);
  if Result then
    Log(Format('taskkill executed for %s (force=%d), exit=%d', [ImageName, Ord(ForceKill), ResultCode]))
  else
    Log(Format('taskkill failed to start for %s (force=%d)', [ImageName, Ord(ForceKill)]));
end;

procedure StopRunningProcesses();
begin
  Log('Stopping Camera Connect processes before install/uninstall...');
  TryStopProcess('CameraConnect.exe', False);
  TryStopProcess('CameraConnectUI.exe', False);
  TryStopProcess('CameraConnectAgent.exe', False);
  Sleep(1200);
  TryStopProcess('CameraConnect.exe', True);
  TryStopProcess('CameraConnectUI.exe', True);
  TryStopProcess('CameraConnectAgent.exe', True);
  Sleep(400);
end;

function PrepareToInstall(var NeedsRestart: Boolean): String;
begin
  StopRunningProcesses();
  Result := '';
end;

function InitializeSetup(): Boolean;
begin
  Log(Format('Installer UI runtime requested=%s effective=%s', ['{#UIRuntimeRequested}', '{#UIRuntime}']));
  Result := True;
end;

procedure CurUninstallStepChanged(CurUninstallStep: TUninstallStep);
begin
  if CurUninstallStep = usUninstall then
    StopRunningProcesses();
end;
