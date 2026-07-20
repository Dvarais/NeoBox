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

echo ==========================================
echo [1/5] Cleaning old build files...
echo ==========================================
if exist "%_SCRIPT_DIR%NeoBox_Setup_*.exe" del /f /q "%_SCRIPT_DIR%NeoBox_Setup_*.exe"
if exist "%_SCRIPT_DIR%build\bin\NeoBox_Setup_*.exe" del /f /q "%_SCRIPT_DIR%build\bin\NeoBox_Setup_*.exe"
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
go run cmd/sign/main.go -key %_PRIVATE_KEY% -file "%_SCRIPT_DIR%build\bin\NeoBox.exe"
if %ERRORLEVEL% neq 0 echo WARNING: Failed to sign NeoBox.exe 1>&2
:skip_sign_exe

echo.
echo ==========================================
echo [4/5] Packaging NSIS installer...
echo ==========================================
if not exist "C:\Program Files (x86)\NSIS\makensis.exe" goto :no_nsis

"C:\Program Files (x86)\NSIS\makensis.exe" /DARG_WAILS_AMD64_BINARY=..\..\bin\NeoBox.exe "%_SCRIPT_DIR%build\windows\installer\project.nsi"
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
go run cmd/sign/main.go -key %_PRIVATE_KEY% -file "%_SETUP_FILE%"
if %ERRORLEVEL% neq 0 echo WARNING: Failed to sign setup package 1>&2
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
