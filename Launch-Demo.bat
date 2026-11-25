@echo off
title RayScout Demo Mode
echo Starting RayScout in local Demo Mode...
echo Opening http://localhost:8090 in your browser...
start "" http://localhost:8090
"%~dp0dist\rayscout.exe" -demo -rules "%~dp0rules.json"
pause
