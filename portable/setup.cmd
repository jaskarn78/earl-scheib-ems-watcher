@echo off
setlocal

:: -----------------------------------------------------------------------
:: Earl Scheib EMS Watcher -- Setup
::
:: Right-click this file and choose "Run as administrator", or double-click
:: and click Yes when Windows asks for permission.
::
:: This script elevates itself to administrator if not already running
:: elevated, then runs the interactive install wizard.
:: -----------------------------------------------------------------------

:: Check for administrator privileges by attempting to create a temp file
:: under a system path that requires elevation.
net session >nul 2>&1
if %errorlevel% neq 0 (
    echo Requesting administrator privileges...
    powershell -Command "Start-Process cmd -Verb RunAs -ArgumentList '/c cd /d \"%~dp0\" && \"%~dpnx0\" elevated && pause'"
    exit /b 0
)

:: Already elevated (either called with 'elevated' arg or via RunAs above).
:: Change to the directory containing this script so relative paths work.
cd /d "%~dp0"

echo.
echo Starting Earl Scheib EMS Watcher setup...
echo.

earlscheib.exe --install
set EXIT_CODE=%errorlevel%

if %EXIT_CODE% neq 0 (
    echo.
    echo Setup did not complete successfully. Exit code: %EXIT_CODE%
    echo.
    echo If you see "not running as administrator", right-click this file
    echo and choose "Run as administrator".
)

echo.
pause
exit /b %EXIT_CODE%
