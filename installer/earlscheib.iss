; Earl Scheib EMS Watcher — Inno Setup 6.7.1 Script
; Binary source: dist/earlscheib-artifact.exe (built by make build-windows + CI signing)
; Build command: docker run --rm -v "$PWD:/work" amake/innosetup:6.7.1 iscc /work/installer/earlscheib.iss
; Output: installer/Output/EarlScheibWatcher-Setup.exe

#define MyAppName "Earl Scheib EMS Watcher"
#define MyAppVersion "1.0.0"
#define MyAppPublisher "jjagpal.me"
#define MyAppURL "https://support.jjagpal.me"
#define MyAppExeName "earlscheib.exe"
#define MyDataDir "C:\EarlScheibWatcher"

; ============================================================
; [Setup]
; ============================================================
[Setup]
AppName=Earl Scheib EMS Watcher
AppVersion={#MyAppVersion}
AppPublisher={#MyAppPublisher}
AppPublisherURL={#MyAppURL}
AppSupportURL={#MyAppURL}
AppUpdatesURL={#MyAppURL}
DefaultDirName={#MyDataDir}
DisableDirPage=yes
DefaultGroupName=Earl Scheib EMS Watcher
DisableProgramGroupPage=yes
PrivilegesRequired=admin
OutputDir=Output
OutputBaseFilename=EarlScheibWatcher-Setup
Compression=lzma
SolidCompression=yes
WizardStyle=modern
SetupIconFile=..\winres\app.ico
UninstallDisplayName=Earl Scheib EMS Watcher
UninstallDisplayIcon={app}\earlscheib.exe
DisableWelcomePage=no
WizardResizable=no
ShowLanguageDialog=no

; ============================================================
; [Languages]
; ============================================================
[Languages]
Name: "english"; MessagesFile: "compiler:Default.isl"

; ============================================================
; [Messages]
; Override WelcomePage to include SmartScreen explanation (INST-11)
; ============================================================
[Messages]
WelcomeLabel1=Welcome to the Earl Scheib EMS Watcher Setup
WelcomeLabel2=This wizard will install the Earl Scheib EMS Watcher on your computer.%n%nThe watcher automatically sends CCC ONE estimate files to the follow-up service every 5 minutes.%n%nIMPORTANT: If Windows shows a "Windows protected your PC" dialog when you run this installer, click "More info" and then "Run anyway". This is normal for new business software that hasn't been downloaded many times yet.%n%nClick Next to continue.

; ============================================================
; [Files]
; ============================================================
[Files]
; Main binary — always overwrite on upgrade
Source: "..\dist\earlscheib-artifact.exe"; DestDir: "{app}"; DestName: "earlscheib.exe"; Flags: ignoreversion

; config.ini — only install if not present (preserves upgrade settings, per INST-08)
Source: "config.ini.template"; DestDir: "{app}"; DestName: "config.ini"; Flags: onlyifdoesntexist uninsneveruninstall

; Task XML templates — extract to {tmp} during install, deleted after (not left in install dir)
Source: "tasks\EarlScheibEMSWatcher-SYSTEM.xml"; DestDir: "{tmp}"; Flags: deleteafterinstall
Source: "tasks\EarlScheibEMSWatcher-User.xml"; DestDir: "{tmp}"; Flags: deleteafterinstall

; ============================================================
; [Dirs]
; ============================================================
[Dirs]
Name: "{app}"; Permissions: users-modify system-full

; ============================================================
; [Icons]
; Desktop + Start Menu shortcuts for the Queue Admin UI (ADMIN-11)
; ============================================================
[Icons]
Name: "{commondesktop}\Earl Scheib Queue"; Filename: "{app}\earlscheib.exe"; Parameters: "--admin"; IconFilename: "{app}\earlscheib.exe"; Comment: "View and cancel pending SMS messages"
Name: "{group}\Earl Scheib Queue"; Filename: "{app}\earlscheib.exe"; Parameters: "--admin"; IconFilename: "{app}\earlscheib.exe"; Comment: "View and cancel pending SMS messages"
Name: "{group}\Uninstall Earl Scheib EMS Watcher"; Filename: "{uninstallexe}"

; ============================================================
; [Run]
; Post-install steps in order (INST-03, INST-04, INST-09)
; ============================================================
[Run]
; 1. ACL: SYSTEM=Full, Users=Modify on data dir (per INST-03)
Filename: "{sys}\icacls.exe"; Parameters: """{app}"" /grant ""SYSTEM:(OI)(CI)F"" /grant ""Users:(OI)(CI)M"" /T /Q"; Flags: runhidden; StatusMsg: "Configuring permissions..."

; 2. Scheduled Task registration is handled by Pascal [Code] CurStepChanged(ssPostInstall)
;    (see [Code] section — tries SYSTEM first, falls back to interactive user for mapped drives)

; 3. First scan to prove the pipeline is live (per INST-09)
Filename: "{app}\earlscheib.exe"; Parameters: "--scan"; Flags: runhidden nowait; StatusMsg: "Running first scan..."

; 4. Optional: launch the Queue Admin UI on the finish page (default checked)
Filename: "{app}\earlscheib.exe"; Parameters: "--admin"; Description: "Launch Queue Viewer now"; Flags: nowait postinstall skipifsilent

; ============================================================
; [UninstallRun]
; Remove the Scheduled Task before deleting binary (INST-10)
; ============================================================
[UninstallRun]
Filename: "{sys}\schtasks.exe"; Parameters: "/Delete /TN ""EarlScheibEMSWatcher"" /F"; Flags: runhidden; RunOnceId: "DeleteTask"

; ============================================================
; [Code]
; Pascal script — custom wizard pages + post-install logic
; ============================================================
[Code]
// ---------------------------------------------------------------
// Global state
// ---------------------------------------------------------------
var
  FolderPage: TInputDirWizardPage;    // Step 1: folder picker (UI-06)
  ConnPage: TOutputMsgWizardPage;     // Step 2: connection test (UI-07)
  CCCInfoPage: TOutputMsgWizardPage;  // Step 3: CCC ONE instructions (UI-08)
  CCCCheckBox: TCheckBox;             // "I've done this" checkbox on Step 3
  FWatchFolder: String;               // Chosen watch folder (global)
  FUseMappedFallback: Boolean;        // True if user chose user-mode task fallback

// External Windows API — SetEnvironmentVariable (unicode) from kernel32.dll.
// Used by RunConnectionTest to override EARLSCHEIB_DATA_DIR for the --test subprocess
// so it reads our temp config.ini instead of the final C:\EarlScheibWatcher\config.ini
// (which doesn't exist yet during install).
function SetEnvironmentVariable(lpName, lpValue: String): Boolean;
  external 'SetEnvironmentVariableW@kernel32.dll stdcall';

// ---------------------------------------------------------------
// IsMappedDrive: returns True if path starts with a single drive
// letter that is not C: (heuristic for mapped network drive).
// UNC paths (\\) return False. Local drives (C:\) return False.
// ---------------------------------------------------------------
function IsMappedDrive(Path: String): Boolean;
var
  Drive: String;
begin
  Result := False;
  if Length(Path) < 2 then Exit;
  Drive := UpperCase(Copy(Path, 1, 1));
  // Single letter followed by ':' indicates drive letter
  if (Drive >= 'A') and (Drive <= 'Z') and (Path[2] = ':') then begin
    // C: is local — everything else treated as potentially mapped
    if Drive <> 'C' then
      Result := True;
  end;
end;

// ---------------------------------------------------------------
// DetectCCCOnePath: returns the first existing CCC ONE export
// directory from the standard candidate list. Returns '' if none.
// Scans 4 common CCC ONE install paths (per CONTEXT.md).
// ---------------------------------------------------------------
function DetectCCCOnePath(): String;
var
  Candidates: TArrayOfString;
  I: Integer;
begin
  Result := '';
  // Ordered most-specific to least-specific; first match wins.
  // PartsTrader\Export is the actual path CCC ONE uses on Marco's shop PC
  // (observed 2026-04-21 — CCC reports "EMS Extract File(s) successfully
  // extracted to C:\CCC APPS\CCCONE\CCCONE\DATA\PartsTrader\Export").
  SetArrayLength(Candidates, 8);
  Candidates[0] := 'C:\CCC APPS\CCCONE\CCCONE\DATA\PartsTrader\Export';
  Candidates[1] := 'C:\CCC\APPS\CCCONE\CCCONE\DATA\PartsTrader\Export';
  Candidates[2] := 'C:\CCC APPS\CCCONE\CCCONE\DATA';
  Candidates[3] := 'C:\CCC\APPS\CCCONE\CCCONE\DATA';
  Candidates[4] := 'C:\CCC\APPS\CCCCONE\CCCCONE\DATA';
  Candidates[5] := 'C:\CCC\EMS_Export';
  Candidates[6] := 'C:\Program Files\CCC';
  Candidates[7] := 'C:\Program Files (x86)\CCC';
  for I := 0 to 7 do begin
    if DirExists(Candidates[I]) then begin
      Result := Candidates[I];
      Exit;
    end;
  end;
end;

// ---------------------------------------------------------------
// WriteConfigIni: writes config.ini with the chosen watch_folder.
// Only called if config.ini does not already exist (onlyifdoesntexist
// flag handles upgrade case, but we also write it here for correctness
// in case the [Files] copy step didn't run).
// Keys: watch_folder, webhook_url, log_level (no secret_key — baked into binary).
// ---------------------------------------------------------------
procedure WriteConfigIni(WatchFolder: String);
var
  ConfigPath: String;
  Lines: TArrayOfString;
begin
  ConfigPath := ExpandConstant('{app}\config.ini');
  if FileExists(ConfigPath) then Exit;  // preserve existing config on upgrade
  SetArrayLength(Lines, 6);
  Lines[0] := '[watcher]';
  Lines[1] := 'watch_folder = ' + WatchFolder;
  Lines[2] := 'webhook_url = https://support.jjagpal.me/earlscheibconcord';
  Lines[3] := 'log_level = INFO';
  Lines[4] := '';
  Lines[5] := '; secret_key is baked into the binary — do not add it here';
  SaveStringsToFile(ConfigPath, Lines, False);
end;

// ---------------------------------------------------------------
// RunConnectionTest: HTTPS ping to the webhook status endpoint via
// Inno Setup's built-in DownloadTemporaryFile. Tests the real failure
// mode (no internet / server down) without needing {app}\earlscheib.exe
// — {app} is NOT expandable during pre-install wizard pages.
// Returns True if we can reach the server and it returns a 2xx body.
// ---------------------------------------------------------------
function RunConnectionTest(WatchFolder: String): Boolean;
var
  DownloadedBytes: Int64;
begin
  Result := False;
  try
    // Hit the unauthenticated /status JSON endpoint. Any 2xx = internet
    // works and our server is reachable. Saved to {tmp}, deleted after.
    DownloadedBytes := DownloadTemporaryFile(
      'https://support.jjagpal.me/earlscheibconcord/status',
      'earlscheib-connection-check.json',
      '',
      nil);
    Result := (DownloadedBytes > 0);
  except
    // Any exception — DNS failure, TLS problem, HTTP non-2xx — means
    // the shop's PC cannot reach the service. Return False; the wizard
    // surfaces "Continue anyway / Retry" via the existing UI branch.
    Result := False;
  end;
end;

// ---------------------------------------------------------------
// RegisterScheduledTask: creates the Scheduled Task via schtasks /Create /XML.
// Tries SYSTEM first; falls back to current user if SYSTEM fails or if
// a mapped drive was detected (INST-04, mirrors install.bat prior art).
// ---------------------------------------------------------------
function RegisterScheduledTask(UseFallback: Boolean): Boolean;
var
  SystemXML: String;
  UserXML: String;
  ResultCode: Integer;
begin
  Result := False;
  SystemXML := ExpandConstant('{tmp}\EarlScheibEMSWatcher-SYSTEM.xml');
  UserXML := ExpandConstant('{tmp}\EarlScheibEMSWatcher-User.xml');

  // Delete any existing task first (silent, ignore error)
  Exec(ExpandConstant('{sys}\schtasks.exe'),
       '/Delete /TN "EarlScheibEMSWatcher" /F',
       '', SW_HIDE, ewWaitUntilTerminated, ResultCode);

  if not UseFallback then begin
    // Try SYSTEM account (per INST-04 default)
    if Exec(ExpandConstant('{sys}\schtasks.exe'),
            '/Create /XML "' + SystemXML + '" /TN "EarlScheibEMSWatcher" /F /RU SYSTEM',
            '', SW_HIDE, ewWaitUntilTerminated, ResultCode) then begin
      if ResultCode = 0 then begin
        Result := True;
        Exit;
      end;
    end;
    // SYSTEM failed — fall through to user fallback
    Log('SYSTEM task registration failed (code ' + IntToStr(ResultCode) + '); using user fallback');
  end;

  // User-mode task (sees mapped drives, requires user to be logged on)
  if Exec(ExpandConstant('{sys}\schtasks.exe'),
          '/Create /XML "' + UserXML + '" /TN "EarlScheibEMSWatcher" /F /IT',
          '', SW_HIDE, ewWaitUntilTerminated, ResultCode) then begin
    Result := (ResultCode = 0);
  end;
end;

// ---------------------------------------------------------------
// Wizard page creation (InitializeWizard)
// ---------------------------------------------------------------
procedure InitializeWizard();
var
  DetectedPath: String;
  InfoText: String;
begin
  // -- Page 1: Folder Selection (UI-06) --
  FolderPage := CreateInputDirPage(wpLicense,
    'CCC ONE Export Folder',
    'Where does CCC ONE save EMS estimate files?',
    'The Earl Scheib Watcher monitors this folder for new estimates. ' +
    'It must be a local folder path (not a mapped network drive letter).',
    False, '');
  // CreateInputDirPage does not add a directory-edit field automatically —
  // Add() is required before Values[0] is valid. Without this, setting
  // FolderPage.Values[0] below raises "List index out of bounds (0)."
  FolderPage.Add('CCC ONE export folder:');

  DetectedPath := DetectCCCOnePath();
  if DetectedPath <> '' then
    FolderPage.Values[0] := DetectedPath
  else
    FolderPage.Values[0] := 'C:\CCC APPS\CCCONE\CCCONE\DATA\PartsTrader\Export';

  // -- Page 2: Connection Test (UI-07) --
  ConnPage := CreateOutputMsgPage(FolderPage.ID,
    'Connection Test',
    'Verifying connection to the follow-up service...',
    'Click Next to test the connection. This requires internet access.');

  // -- Page 3: CCC ONE Configuration (UI-08) --
  InfoText :=
    'Before finishing, make sure CCC ONE is configured to save EMS files automatically.' + #13#10 + #13#10 +
    'In CCC ONE, open: Tools > Extract > EMS Extract Preferences' + #13#10 + #13#10 +
    'Check both of these boxes:' + #13#10 +
    '   [x] Lock Estimate' + #13#10 +
    '   [x] Save Workfile' + #13#10 + #13#10 +
    'Set the Output Folder to the path you just entered.' + #13#10 + #13#10 +
    'Once checked, click Save and close the preferences window.' + #13#10 + #13#10 +
    'Check the box below when you have done this:';

  CCCInfoPage := CreateOutputMsgPage(ConnPage.ID,
    'Configure CCC ONE',
    'Set up CCC ONE to export estimates automatically',
    InfoText);

  // Add the "I've done this" checkbox — required to advance (UI-08)
  CCCCheckBox := TCheckBox.Create(WizardForm);
  CCCCheckBox.Parent := CCCInfoPage.Surface;
  CCCCheckBox.Caption := 'I have configured CCC ONE EMS Extract Preferences';
  CCCCheckBox.Left := 0;
  CCCCheckBox.Top := CCCInfoPage.Surface.Height - ScaleY(24);
  CCCCheckBox.Width := CCCInfoPage.Surface.Width;
  CCCCheckBox.Checked := False;
end;

// ---------------------------------------------------------------
// NextButtonClick: page-level validation and side effects
// ---------------------------------------------------------------
function NextButtonClick(CurPageID: Integer): Boolean;
var
  Folder: String;
  TestResult: Boolean;
  MsgResult: Integer;
begin
  Result := True;

  // Folder page validation (UI-06)
  if CurPageID = FolderPage.ID then begin
    Folder := FolderPage.Values[0];

    // Validate folder exists
    if not DirExists(Folder) then begin
      MsgBox('The folder "' + Folder + '" does not exist.' + #13#10 +
             'Please enter a valid folder path or create the folder in Windows Explorer first.',
             mbError, MB_OK);
      Result := False;
      Exit;
    end;

    // Mapped drive detection (per INST-04, Pitfall #3 in CONTEXT.md)
    if IsMappedDrive(Folder) then begin
      MsgResult := MsgBox(
        'The folder "' + Folder + '" appears to be on a mapped network drive.' + #13#10#13#10 +
        'The Windows background task runs as SYSTEM, which cannot see mapped drive letters.' + #13#10#13#10 +
        'Options:' + #13#10 +
        '  - Enter a UNC path instead (e.g. \\server\share\CCC_Export)' + #13#10 +
        '  - Or click OK to use a user-mode task (requires you to be logged in)' + #13#10#13#10 +
        'Click Cancel to go back and change the path.',
        mbConfirmation, MB_OKCANCEL);
      if MsgResult = IDCANCEL then begin
        Result := False;
        Exit;
      end;
      // User chose OK — flag for user-mode task fallback
      FUseMappedFallback := True;
    end else begin
      FUseMappedFallback := False;
    end;

    FWatchFolder := Folder;
  end;

  // Connection test page (UI-07)
  if CurPageID = ConnPage.ID then begin
    WizardForm.NextButton.Enabled := False;
    WizardForm.NextButton.Caption := 'Testing...';
    try
      TestResult := RunConnectionTest(FWatchFolder);
    finally
      WizardForm.NextButton.Enabled := True;
      WizardForm.NextButton.Caption := 'Next >';
    end;

    if not TestResult then begin
      MsgResult := MsgBox(
        'Connection test failed.' + #13#10#13#10 +
        'The watcher could not reach the follow-up service.' + #13#10#13#10 +
        'Possible causes:' + #13#10 +
        '  - No internet connection' + #13#10 +
        '  - Firewall blocking outbound HTTPS' + #13#10#13#10 +
        'Check the log at: ' + ExpandConstant('{tmp}\ems_watcher.log') + #13#10#13#10 +
        'Click Retry to test again, or Ignore to continue anyway.',
        mbError, MB_ABORTRETRYIGNORE);

      if MsgResult = IDABORT then begin
        // Cancel install
        WizardForm.Close;
        Result := False;
        Exit;
      end else if MsgResult = IDRETRY then begin
        // Return False to stay on this page; user clicks Next again to retry
        Result := False;
        Exit;
      end;
      // IDIGNORE falls through — continue anyway
    end;
  end;

  // CCC ONE info page — require checkbox (UI-08)
  if CurPageID = CCCInfoPage.ID then begin
    if not CCCCheckBox.Checked then begin
      MsgBox('Please check the box confirming you have configured CCC ONE before continuing.',
             mbError, MB_OK);
      Result := False;
      Exit;
    end;
  end;
end;

// ---------------------------------------------------------------
// CurStepChanged: write config.ini and register Scheduled Task
// after files are extracted (ssPostInstall).
// ---------------------------------------------------------------
procedure CurStepChanged(CurStep: TSetupStep);
begin
  if CurStep = ssPostInstall then begin
    // Write config.ini with chosen folder (UI-09, INST-02)
    WriteConfigIni(FWatchFolder);

    // Register Scheduled Task (INST-04)
    if not RegisterScheduledTask(FUseMappedFallback) then begin
      MsgBox('Warning: could not register the background task automatically.' + #13#10 +
             'The watcher was installed but will not run on a schedule.' + #13#10 +
             'Open Task Scheduler and create a task manually to run:' + #13#10 +
             '  ' + ExpandConstant('{app}\earlscheib.exe') + ' --scan' + #13#10 +
             'every 5 minutes.',
             mbError, MB_OK);
    end;
  end;
end;

// ---------------------------------------------------------------
// UninstallInitialize: optionally preserve data directory on uninstall.
// Prompts Marco to confirm data removal — gives option to keep logs.
// ---------------------------------------------------------------
procedure UninstallInitialize();
var
  DataDir: String;
  MsgResult: Integer;
begin
  DataDir := ExpandConstant('{app}');
  if DirExists(DataDir) then begin
    MsgResult := MsgBox(
      'Do you want to delete all data in ' + DataDir + '?' + #13#10#13#10 +
      'This includes the log file (ems_watcher.log) and deduplication database.' + #13#10#13#10 +
      'Click Yes to delete everything.' + #13#10 +
      'Click No to keep the log and database files.',
      mbConfirmation, MB_YESNO);
    if MsgResult = IDYES then begin
      DelTree(DataDir, True, True, True);
    end;
  end;
end;
