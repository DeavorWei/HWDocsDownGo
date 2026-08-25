@echo off
setlocal

echo [1/3] Checking output directories...
if not exist "build" mkdir build
if not exist "HWDDGoData" mkdir HWDDGoData

echo [2/3] Building HWDocsDownGo.exe...
go build -ldflags="-s -w" -o build\HWDocsDownGo.exe cmd\server\main.go
::go build -ldflags="-s -w -H windowsgui -X main.version=1.0.0" -trimpath -o .\build\HWDocsDownGo.exe .\cmd\server\main.go
if %ERRORLEVEL% NEQ 0 (
    echo [ERROR] Build failed! Please check the Go compiler errors above.
    exit /b %ERRORLEVEL%
)

echo [3/3] Build completed successfully!
echo Binary Output: build\HWDocsDownGo.exe
echo Data Folder:   HWDDGoData\
echo.
echo You can run: build\HWDocsDownGo.exe
endlocal
