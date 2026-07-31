@echo off
setlocal EnableDelayedExpansion

REM ============================================================
REM  Script: build_installer.bat
REM  Purpose: Cleans old builds, compiles frontend, builds Go,
REM           signs executables, and packages the NSIS installer.
REM ============================================================

set "_SCRIPT_DIR=%~dp0"
set "_PRIVATE_KEY="

REM Parse arguments
:parse_args
if "%~1"=="" goto :args_done
if /i "%~1"=="--key" set "_PRIVATE_KEY=%~2" & shift & shift & goto :parse_args
if /i "%~1"=="-k" set "_PRIVATE_KEY=%~2" & shift & shift & goto :parse_args
shift
goto :parse_args
:args_done

REM --key accepts either the hex private key itself or a path to a file holding
REM it. The file form keeps the key out of the console history. If the path
REM exists but is empty, _PRIVATE_KEY stays the path and cmd/sign rejects it.
if defined _PRIVATE_KEY if exist "%_PRIVATE_KEY%" for /f "usebackq delims=" %%k in ("%_PRIVATE_KEY%") do if not defined _KEY_LINE set "_KEY_LINE=%%k"
if defined _KEY_LINE set "_PRIVATE_KEY=%_KEY_LINE%"

REM Read the version straight out of wails.json, which is the single source of
REM truth. It has to be passed to makensis explicitly: NSIS takes the version
REM from INFO_PRODUCTVERSION in build\windows\installer\wails_tools.nsh, and
REM Wails only rewrites that file during `wails build -nsis`. This script builds
REM without -nsis and invokes makensis itself, so the value baked into that file
REM goes stale the moment wails.json is bumped — which silently produced an
REM installer named after the previous release.
set "_VERSION="
for /f "tokens=2 delims=:," %%v in ('findstr /c:"productVersion" "%_SCRIPT_DIR%wails.json"') do set "_VERSION=%%~v"
set "_VERSION=%_VERSION: =%"
set "_VERSION=%_VERSION:"=%"
if not defined _VERSION goto :no_version
echo Building version %_VERSION%

echo ==========================================
echo [1/5] Cleaning old build files...
echo ==========================================
REM Signatures are removed too. A .sig left over from an earlier version would
REM otherwise sit next to a new installer it does not match.
if exist "%_SCRIPT_DIR%NeoBox_Setup_*.exe" del /f /q "%_SCRIPT_DIR%NeoBox_Setup_*.exe"
if exist "%_SCRIPT_DIR%NeoBox_Setup_*.exe.sig" del /f /q "%_SCRIPT_DIR%NeoBox_Setup_*.exe.sig"
if exist "%_SCRIPT_DIR%build\bin\NeoBox_Setup_*.exe" del /f /q "%_SCRIPT_DIR%build\bin\NeoBox_Setup_*.exe"
if exist "%_SCRIPT_DIR%build\bin\NeoBox_Setup_*.exe.sig" del /f /q "%_SCRIPT_DIR%build\bin\NeoBox_Setup_*.exe.sig"
if exist "%_SCRIPT_DIR%build\bin\NeoBox.exe" del /f /q "%_SCRIPT_DIR%build\bin\NeoBox.exe"
if exist "%_SCRIPT_DIR%build\bin\NeoBox.exe.sig" del /f /q "%_SCRIPT_DIR%build\bin\NeoBox.exe.sig"
echo Clean completed.

echo.
echo ==========================================
echo [2/5] Building frontend assets...
echo ==========================================
pushd "%_SCRIPT_DIR%frontend"
call npm run build
if %ERRORLEVEL% neq 0 goto :frontend_failed
popd

echo.
echo ==========================================
echo [3/5] Compiling Go application...
echo ==========================================
wails build -tags "desktop,production,with_utls,with_clash_api,with_quic,with_wireguard,with_gvisor" -o NeoBox.exe
if %ERRORLEVEL% neq 0 goto :go_failed
echo Successfully compiled NeoBox.exe to build\bin\

if not defined _PRIVATE_KEY goto :skip_sign_exe
echo.
echo ==========================================
echo [3.5/5] Signing NeoBox.exe...
echo ==========================================
go run cmd/sign/main.go -key "%_PRIVATE_KEY%" -file "%_SCRIPT_DIR%build\bin\NeoBox.exe"
if %ERRORLEVEL% neq 0 goto :sign_failed
:skip_sign_exe

echo.
echo ==========================================
echo [4/5] Packaging NSIS installer...
echo ==========================================
if not exist "C:\Program Files (x86)\NSIS\makensis.exe" goto :no_nsis

"C:\Program Files (x86)\NSIS\makensis.exe" /DINFO_PRODUCTVERSION=%_VERSION% /DARG_WAILS_AMD64_BINARY=..\..\bin\NeoBox.exe "%_SCRIPT_DIR%build\windows\installer\project.nsi"
if %ERRORLEVEL% neq 0 goto :nsis_failed

echo.
echo ==========================================
echo [5/5] Finalizing...
echo ==========================================
REM Find the generated installer in the root folder (copied by project.nsi finalize)
for %%f in ("%_SCRIPT_DIR%NeoBox_Setup_v*.exe") do (
    set "_SETUP_FILE=%%f"
)

if not defined _SETUP_FILE goto :no_setup
echo Found setup package: %_SETUP_FILE%

if not defined _PRIVATE_KEY goto :skip_sign_setup
echo Signing setup package...
go run cmd/sign/main.go -key "%_PRIVATE_KEY%" -file "%_SETUP_FILE%"
if %ERRORLEVEL% neq 0 goto :sign_failed
:skip_sign_setup

echo.
echo ============================================================
echo Build completed successfully!
echo ============================================================
exit /b 0

:frontend_failed
echo ERROR: Frontend build failed. 1>&2
popd
exit /b 1

:go_failed
echo ERROR: Go build failed. 1>&2
exit /b 1

:no_nsis
echo ERROR: NSIS makensis.exe not found at C:\Program Files ^(x86^)\NSIS\makensis.exe 1>&2
exit /b 1

:nsis_failed
echo ERROR: NSIS packaging failed. 1>&2
exit /b 1

:no_setup
echo ERROR: Setup file not found in root. 1>&2
exit /b 1

:no_version
echo ERROR: Could not read productVersion from wails.json. 1>&2
echo        Without it the installer would be named after whatever stale 1>&2
echo        version sits in build\windows\installer\wails_tools.nsh. 1>&2
exit /b 1

:sign_failed
echo ERROR: Signing failed -- see the message above. 1>&2
echo        --key takes the 128-character hex private key printed by 1>&2
echo        "go run cmd/keygen/main.go", or a path to a file containing it. 1>&2
echo        Releasing an unsigned build would break the in-app updater, so 1>&2
echo        the build stops here rather than warning and carrying on. 1>&2
exit /b 1
