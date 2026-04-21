Earl Scheib EMS Watcher
=======================
Version 1.0

DOWNLOAD
--------
Get the latest installer from:

  https://support.jjagpal.me/earlscheibconcord/download

The download is a single file, EarlScheibWatcher-Setup.exe (about 10 MB).
Save it to your Desktop or Downloads folder, then double-click to run.

If you prefer not to run a traditional installer, a portable ZIP
(EarlScheibWatcher-Portable.zip) is also available -- contact App Support.

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

  3. The installer will start normally.

Why this happens: Windows SmartScreen checks how many people have
downloaded a program. New programs that haven't been downloaded many
times yet show this warning automatically. The Earl Scheib Watcher is
digitally signed, so Windows can verify it came from the developer.


FIRST-TIME SETUP (DURING INSTALL)
----------------------------------
The installer will guide you through three steps:

  Step 1 -- Folder: Choose the folder where CCC ONE saves EMS files.
            The installer will suggest the most likely location. If
            CCC ONE is installed in a non-standard location, use the
            Browse button to find it.

            IMPORTANT: If your CCC ONE export folder is on a network
            drive (like Z:\...), enter the full network path instead
            (e.g. \\server\share\CCC_Export). This ensures the
            background task can always find the folder.

  Step 2 -- Connection: The installer tests the connection to the
            follow-up service. If the test fails, check your internet
            connection or contact your IT person. You can click
            "Skip this check" to continue anyway -- the watcher will
            retry automatically.

  Step 3 -- CCC ONE: Make sure CCC ONE is set to export EMS files.
            See "CONFIGURE CCC ONE" below.


CONFIGURE CCC ONE
-----------------
In CCC ONE, open: Tools > Extract > EMS Extract Preferences

Check BOTH of these boxes:
   [x] Lock Estimate
   [x] Save Workfile

Set the "Output Folder" to the same folder you chose in Step 1.

Click Save and close the preferences window.

CCC ONE will now automatically export an EMS file every time you
lock an estimate. The watcher picks it up within 5 minutes.


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

The log file at C:\EarlScheibWatcher\ems_watcher.log shows activity.
Each scan run is logged with a timestamp and count of files processed.


UPGRADING
---------
Run the new installer over the existing installation. Your settings
in config.ini are automatically preserved -- the installer will NOT
overwrite your configured folder path.


UNINSTALLING
------------
Use Windows Settings > Apps (or Control Panel > Programs) and
uninstall "Earl Scheib EMS Watcher". The uninstaller will:
  - Remove the background task from Task Scheduler
  - Remove the program files from C:\EarlScheibWatcher\
  - Remove the entry from Add/Remove Programs

Your log files will be preserved unless you manually delete
C:\EarlScheibWatcher\.


SUPPORT
-------
Contact: support.jjagpal.me
