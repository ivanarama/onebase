@echo off
setlocal
cd /d "%~dp0"

echo === onebase build ===
echo.

echo [1/2] onebase.exe  (консоль / браузер)...
set CGO_ENABLED=0
go build -ldflags="-s -w" -o onebase.exe ./cmd/onebase
if %errorlevel% neq 0 ( echo ОШИБКА & pause & exit /b 1 )
echo     OK

echo.
echo [2/2] onebase-gui.exe  (нативное окно WebView2)...
set CGO_ENABLED=1
go get github.com/webview/webview_go@latest >nul 2>&1
go build -tags webview -ldflags="-s -w -H windowsgui" -o onebase-gui.exe ./cmd/onebase
if %errorlevel% neq 0 (
    echo     ПРОПУЩЕНО — нет MSVC/CGO. Используйте onebase.exe.
) else (
    echo     OK
)

echo.
echo Готово!  onebase.exe  и  onebase-gui.exe  в текущей папке.
echo Запустите onebase-gui.exe  или  onebase.exe  ^(без аргументов^) — откроется лаунчер.
pause
