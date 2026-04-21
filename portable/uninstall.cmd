@echo off
setlocal

:: -----------------------------------------------------------------------
:: Earl Scheib EMS Watcher -- Uninstall
::
:: Right-click this file and choose "Run as administrator", or double-click
:: and click Yes when Windows asks for permission.
:: -----------------------------------------------------------------------

:: Check for administrator privileges.
net session >nul 2>&1
if %errorlevel% neq 0 (
    echo Requesting administrator privileges...
    powershell -Command "Start-Process cmd -Verb RunAs -ArgumentList '/c cd /d \"%~dp0\" && \"%~dpnx0\" elevated && pause'"
    exit /b 0
)

cd /d "%~dp0"

echo.
echo Starting Earl Scheib EMS Watcher uninstall...
echo.

:: Try the installed binary first; fall back to the portable copy.
if exist "C:\EarlScheibWatcher\earlscheib.exe" (
    C:\EarlScheibWatcher\earlscheib.exe --uninstall
) else (
    earlscheib.exe --uninstall
)
set EXIT_CODE=%errorlevel%

if %EXIT_CODE% neq 0 (
    echo.
    echo Uninstall returned exit code: %EXIT_CODE%
)

echo.
pause
exit /b %EXIT_CODE%
