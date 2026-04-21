Earl Scheib EMS Watcher
=======================
Version 1.0 -- Portable ZIP Edition

ALTERNATIVE DISTRIBUTION. Most shops should use the signed installer
(EarlScheibWatcher-Setup.exe) from:

  https://support.jjagpal.me/earlscheibconcord/download

This portable ZIP is a no-installer alternative: unzip it anywhere and
run setup.cmd. Useful when:
  - You cannot run .exe installers on the PC (corporate IT restriction)
  - You prefer not to add an "Add/Remove Programs" entry
  - You want to see what's being installed before running it

Functionally identical to the installer version once setup.cmd finishes.


WHAT THIS DOES
--------------
The EMS Watcher automatically sends CCC ONE estimate files to the
Earl Scheib follow-up service every 5 minutes. Once installed and
configured, it runs silently in the background -- no action required.


IF WINDOWS SHOWS A "WINDOWS PROTECTED YOUR PC" WARNING
-------------------------------------------------------
This is normal for new business software. Follow these steps:

  1. When you see the blue "Windows protected your PC" screen,
     click "More info" (below the main message).

  2. A "Run anyway" button will appear at the bottom of the screen.
     Click it.

  3. The setup wizard will start normally.

Why this happens: Windows SmartScreen checks how many times a program
has been downloaded across the internet. Newly-released business software
that hasn't been widely distributed yet will trigger this warning
automatically. The EMS Watcher is legitimate software from Earl Scheib
Auto Body Concord.


SETUP -- HOW TO INSTALL
------------------------
  1. Extract EarlScheibWatcher-Portable.zip anywhere (your Desktop is fine).

  2. Open the extracted folder and double-click setup.cmd.
     Windows will ask "Do you want to allow this app to make changes?"
     Click YES (this is the normal administrator permission prompt).

  3. A console window opens and walks you through setup:
       - It will suggest the CCC ONE export folder automatically.
         Confirm or type a different path.
       - It runs a connection test to verify your internet connection.
       - It copies the watcher to C:\EarlScheibWatcher\ and creates a
         Windows Scheduled Task to run every 5 minutes.

  4. When setup says "Installation complete!", you are done.

  NOTE: If your CCC ONE export folder is on a network drive (like Z:\...)
  setup will warn you and offer options. Choose "Use UNC path" and enter
  the full network path (e.g. \\server\share\CCC_Export) for the most
  reliable setup. The UNC path works even without the drive letter mapped.


WHAT SETUP WILL ASK
-------------------
  - CCC ONE export folder path
    (setup auto-detects the most likely location and shows it as default;
    just press Enter to accept, or type a different path)

  - Confirmation before running the connection test

  - What to do if the connection test fails
    (Retry / Continue anyway / Cancel)


CONFIGURE CCC ONE
-----------------
In CCC ONE, open: Tools > Extract > EMS Extract Preferences

Check BOTH of these boxes:
   [x] Lock Estimate
   [x] Save Workfile

Set the "Output Folder" to the same folder you chose during setup.

Click Save and close the preferences window.

CCC ONE will now automatically export an EMS file every time you lock
an estimate. The watcher picks it up within 5 minutes.


WHERE THINGS ARE INSTALLED
---------------------------
  Program:  C:\EarlScheibWatcher\earlscheib.exe
  Config:   C:\EarlScheibWatcher\config.ini
  Log file: C:\EarlScheibWatcher\ems_watcher.log
  Task:     Task Scheduler > Task Scheduler Library > EarlScheibEMSWatcher


CHECKING IF THE WATCHER IS RUNNING
------------------------------------
Open Task Scheduler (search "Task Scheduler" in the Start menu).
Find "EarlScheibEMSWatcher" in the Task Scheduler Library.
The "Last Run Time" column shows when it last ran.
The "Last Run Result" should show "The operation completed successfully (0x0)".

The log file at C:\EarlScheibWatcher\ems_watcher.log shows all activity.
Each scan run is logged with a timestamp and count of files processed.


UPGRADING
---------
Run setup.cmd again from the new zip. Your settings in config.ini are
automatically preserved -- setup will NOT overwrite your configured
folder path.


UNINSTALLING
------------
Double-click uninstall.cmd in this folder (or from anywhere on the PC).
Click YES when Windows asks for administrator permission.

The uninstall wizard will:
  - Remove the background task from Task Scheduler
  - Ask whether to keep or delete C:\EarlScheibWatcher\
    (log files, database, and config -- safe to keep)


CHANGING THE WATCH FOLDER LATER
---------------------------------
If you move the CCC ONE export folder to a different location, run:

  C:\EarlScheibWatcher\earlscheib.exe --configure

as an administrator. This re-runs the folder selection and connection
test without reinstalling the Scheduled Task.


SUPPORT
-------
Contact: support.jjagpal.me
