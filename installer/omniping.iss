; The OmniPing installer for Windows, made with Inno Setup 6:
;
;   go build -ldflags=-H=windowsgui -o OmniPing.exe .
;   ISCC /DAppVersion=1.0.0 installer\omniping.iss
;
; It takes OmniPing.exe from the repository's root folder and writes
; dist\OmniPing-<version>-windows-setup.exe. GitHub Actions does this for
; every release (.github/workflows/release.yml).
;
; OmniPing is installed for the current user only, so no administrator
; rights are needed. Its settings and history are not in the program folder
; but in %LOCALAPPDATA%\OmniPing, where the app keeps them itself. Never put
; a "data" folder next to OmniPing.exe here: that makes it the portable
; version.

#ifndef AppVersion
  #define AppVersion "0.0.0"
#endif
#ifndef Exe
  #define Exe "..\OmniPing.exe"
#endif
; Never change this: Windows knows OmniPing by it, for updates and removal.
; The double brace stands for one; the AppId is {8B49952E-...}.
#define AppId "{{8B49952E-BD4B-4694-AF5B-49A7652465DB}"

[Setup]
AppId={#AppId}
AppName=OmniPing
AppVersion={#AppVersion}
AppPublisher=sausje
AppPublisherURL=https://github.com/sausje/OmniPing
AppSupportURL=https://github.com/sausje/OmniPing/issues
AppUpdatesURL=https://github.com/sausje/OmniPing/releases
AppCopyright=Copyright (C) 2026 sausje
DefaultDirName={autopf}\OmniPing
DisableProgramGroupPage=yes
PrivilegesRequired=lowest
OutputDir=..\dist
OutputBaseFilename=OmniPing-{#AppVersion}-windows-setup
SetupIconFile=..\winres\tray\normal.ico
UninstallDisplayIcon={app}\OmniPing.exe
UninstallDisplayName=OmniPing
; The app holds this mutex while it runs, so Setup asks to quit it first.
AppMutex=Local\OmniPingRunning
CloseApplications=yes
WizardStyle=modern
; The program is for 64-bit Intel and AMD; ARM PCs run it too.
ArchitecturesAllowed=x64compatible
ArchitecturesInstallIn64BitMode=x64compatible
MinVersion=10.0
Compression=lzma2
SolidCompression=yes

[Languages]
Name: "english"; MessagesFile: "compiler:Default.isl"

[Tasks]
; Checked on the first install only (checkedonce): an update leaves Start with
; Windows as it was, also after it was turned off in OmniPing.
Name: "autostart"; Description: "Start OmniPing when I sign in to Windows"; Flags: checkedonce
Name: "desktopicon"; Description: "{cm:CreateDesktopIcon}"; GroupDescription: "{cm:AdditionalIcons}"; Flags: unchecked

[Files]
Source: "{#Exe}"; DestDir: "{app}"; DestName: "OmniPing.exe"; Flags: ignoreversion

[Icons]
Name: "{autoprograms}\OmniPing"; Filename: "{app}\OmniPing.exe"
Name: "{autodesktop}\OmniPing"; Filename: "{app}\OmniPing.exe"; Tasks: desktopicon

[Registry]
; The same entry OmniPing writes when you turn on Start with Windows. The
; uninstaller removes it in [Code], and only when it starts this copy.
Root: HKCU; Subkey: "Software\Microsoft\Windows\CurrentVersion\Run"; ValueType: string; ValueName: "OmniPing"; ValueData: """{app}\OmniPing.exe"" --autostart"; Tasks: autostart

[Run]
Filename: "{app}\OmniPing.exe"; Description: "{cm:LaunchProgram,OmniPing}"; Flags: nowait postinstall skipifsilent

[Code]
const
  RunKey = 'Software\Microsoft\Windows\CurrentVersion\Run';
  // Where Task Manager notes that a startup entry was turned off.
  ApprovedKey = 'Software\Microsoft\Windows\CurrentVersion\Explorer\StartupApproved\Run';

// Start with Windows can also be turned on in OmniPing's settings. Remove
// it with the program, but only when it starts this copy of OmniPing.
procedure RemoveAutostart;
var
  Cmd: String;
begin
  if RegQueryStringValue(HKCU, RunKey, 'OmniPing', Cmd) and
     (Pos(Lowercase(ExpandConstant('{app}\OmniPing.exe')), Lowercase(Cmd)) > 0) then
  begin
    RegDeleteValue(HKCU, RunKey, 'OmniPing');
    RegDeleteValue(HKCU, ApprovedKey, 'OmniPing');
  end;
end;

// Turning Start with Windows on in Setup also undoes "Disabled" in Task
// Manager, as turning it on in OmniPing does.
procedure CurStepChanged(CurStep: TSetupStep);
begin
  if (CurStep = ssPostInstall) and WizardIsTaskSelected('autostart') then
    RegDeleteValue(HKCU, ApprovedKey, 'OmniPing');
end;

procedure CurUninstallStepChanged(CurUninstallStep: TUninstallStep);
var
  AppDir, Msg: String;
begin
  if CurUninstallStep = usUninstall then
    RemoveAutostart;
  if (CurUninstallStep <> usPostUninstall) or UninstallSilent then
    Exit;
  AppDir := ExpandConstant('{localappdata}\OmniPing');
  if not DirExists(AppDir) then
    Exit;
  Msg := 'Also delete your OmniPing settings and history?' + #13#10#13#10 + AppDir + #13#10#13#10 +
    'If you moved the history to another folder in OmniPing''s settings, delete that folder yourself.';
  if SuppressibleMsgBox(Msg, mbConfirmation, MB_YESNO or MB_DEFBUTTON2, IDNO) = IDYES then
    DelTree(AppDir, True, True, True);
end;
