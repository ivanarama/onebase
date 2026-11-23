@echo off
setlocal
pushd "%~dp0" || exit /b 1
if not exist "demo\config\app.yaml" (
    ".\onebase.exe" --portable init demo
    if errorlevel 1 goto failed
)
if not exist "demo\auth.sqlite" (
    ".\onebase.exe" --portable migrate --project demo --sqlite demo\auth.sqlite
    if errorlevel 1 goto failed
)
".\onebase.exe" --portable ibases list | "%SystemRoot%\System32\findstr.exe" /L /C:"Windows-Auth-2751" >nul
if errorlevel 1 (
    ".\onebase.exe" --portable ibases add --name Windows-Auth-2751 --source file --path demo --sqlite demo\auth.sqlite --port 27451
    if errorlevel 1 goto failed
)
start "" "%~dp0onebase-gui.exe" --portable start
if errorlevel 1 goto failed
popd
exit /b 0

:failed
echo OneBase could not start the test base. See the error above.
pause
popd
exit /b 1
