@echo off
setlocal
cd /d "%~dp0"

echo === onebase build ===
echo.

echo [1/2] onebase.exe  (CLI + сервер)...
set CGO_ENABLED=0
go build -ldflags="-s -w" -o onebase.exe ./cmd/onebase
if %errorlevel% neq 0 ( echo ОШИБКА & pause & exit /b 1 )
echo     OK

echo.
echo [2/2] onebase-gui.exe  (нативное окно WebView2)...
set CGO_ENABLED=1
go build -tags webview -ldflags="-s -w -H windowsgui" -o onebase-gui.exe ./cmd/onebase
if %errorlevel% neq 0 (
    echo     ПРОПУЩЕНО — нет MSVC/CGO. Используйте onebase.exe start.
) else (
    echo     OK
)

echo.
echo Готово!
echo   Двойной клик на onebase-gui.exe  — откроется нативное окно.
echo   Или: onebase.exe start  — откроется в браузере.
pause
