@echo off
setlocal
cd /d "%~dp0"

set "PATH=%~dp0..\.tools\go\bin;%~dp0..\.tools\gopath\bin;%PATH%"
set "GOPATH=%~dp0..\.tools\gopath"
set "GOROOT=%~dp0..\.tools\go"
set "GOSUMDB=sum.golang.org"
set "GONOSUMDB=modernc.org"
set "CGO_ENABLED=0"

echo [1/4] Compiling frontend Vite assets...
cd frontend
call npm.cmd run build
if errorlevel 1 (
    echo [Error] Frontend build failed!
    exit /b 1
)
cd ..

if not exist "build\bin" mkdir "build\bin"

echo.
echo [2/4] Cross-compiling Linux server binary (linux/amd64, pure Go zero CGO)...
set "GOOS=linux"
set "GOARCH=amd64"
set "CGO_ENABLED=0"
go build -ldflags="-s -w" -o "build/bin/audiobook-server-linux-amd64" ./cmd/web
if errorlevel 1 (
    echo [Error] Linux server build failed!
    exit /b 1
)
echo [OK] Output: build/bin/audiobook-server-linux-amd64

echo.
echo [3/4] Compiling Windows headless server binary (windows/amd64)...
set "GOOS=windows"
set "GOARCH=amd64"
set "CGO_ENABLED=0"
go build -ldflags="-s -w" -o "build/bin/audiobook-server-windows-amd64.exe" ./cmd/web
if errorlevel 1 (
    echo [Error] Windows server build failed!
    exit /b 1
)
echo [OK] Output: build/bin/audiobook-server-windows-amd64.exe

echo.
echo [4/4] Packaging Windows Desktop GUI application (Wails safe mode)...
set "GOOS=windows"
set "GOARCH=amd64"
wails build -skipbindings
if errorlevel 1 (
    echo [Error] Wails GUI build failed!
    exit /b 1
)

echo.
echo =======================================================
echo   All targets built successfully into build\bin\ :
echo   1. build\bin\audiobook-server-linux-amd64        (Linux Server)
echo   2. build\bin\audiobook-server-windows-amd64.exe  (Windows CLI Server)
echo   3. build\bin\audiobook.exe                       (Windows GUI Desktop)
echo =======================================================
endlocal
