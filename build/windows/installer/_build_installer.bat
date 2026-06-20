@echo off
setlocal
cd /d "%~dp0"
"%ProgramFiles(x86)%\NSIS\makensis.exe" -DARG_WAILS_AMD64_BINARY=..\..\bin\NeoBox.exe project.nsi
exit /b %ERRORLEVEL%
