@echo off
rem Starts Nexmesh (real Docker workspaces). Safe to run again.
powershell -NoProfile -ExecutionPolicy Bypass -File "%~dp0nexmesh.ps1" start
if errorlevel 1 pause
