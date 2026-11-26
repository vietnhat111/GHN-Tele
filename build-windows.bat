@echo off
chcp 65001 >nul
cd /d "%~dp0"
if not exist dist mkdir dist

set CGO_ENABLED=0
set GOOS=windows
set GOARCH=amd64

echo Dang build ghn-tele.exe ...
go build -trimpath -ldflags "-s -w" -o dist\ghn-tele.exe .
if errorlevel 1 goto :err

echo.
echo OK: dist\ghn-tele.exe
pause
exit /b 0

:err
echo.
echo BUILD LOI - xem thong bao phia tren.
pause
exit /b 1
