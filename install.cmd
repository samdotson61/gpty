@echo off
rem One-click installer for gpty (Windows). Double-click this file, or run it
rem from a terminal. Installs MSYS2 + tmux, ensures Go, builds gpty.exe and
rem gmux.exe, installs the tmux config, and puts this folder on your PATH.
powershell -NoProfile -ExecutionPolicy Bypass -File "%~dp0install.ps1" %*
echo.
pause
