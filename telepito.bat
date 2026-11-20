@echo off
setlocal EnableExtensions EnableDelayedExpansion
chcp 65001 >nul
title RealPassGAL telepítő

rem ===== Beállítás =====
set "REPO=gali-master/RealPassGAL"
set "ZIP=RealPassGAL.zip"
set "URL=https://github.com/%REPO%/releases/latest/download/%ZIP%"

echo.
echo  ================================================
echo     RealPassGAL telepítő
echo     Egy fájl. Két lakat. Csak ti ketten.
echo  ================================================
echo.

rem ----- 1. Hova? -----
set "DEST=%USERPROFILE%\RealPassGAL"
echo  Javasolt hely: %DEST%
set "IN="
set /p "IN= Enter = elfogadom, vagy írj be másik mappát: "
if defined IN set "DEST=!IN:"=!"
echo.
echo  A program ide kerül: %DEST%
choice /c IN /n /m " Mehet? [I]gen / [N]em: "
if errorlevel 2 goto :megse

rem ----- 2. Letöltés és ellenőrzés -----
set "TMPD=%TEMP%\RealPassGAL_telepito"
if exist "%TMPD%" rd /s /q "%TMPD%"
mkdir "%TMPD%"
echo.
echo  Letöltés a GitHubról...
curl.exe -L --fail --progress-bar -o "%TMPD%\%ZIP%" "%URL%"
if errorlevel 1 goto :hiba_letoltes
curl.exe -L --fail -s -o "%TMPD%\%ZIP%.sha256" "%URL%.sha256"
if errorlevel 1 goto :hiba_letoltes
powershell -NoProfile -Command "$e=((Get-Content -LiteralPath '%TMPD%\%ZIP%.sha256' -Raw).Trim() -split '\s+')[0].ToLower(); $a=(Get-FileHash -LiteralPath '%TMPD%\%ZIP%' -Algorithm SHA256).Hash.ToLower(); if($e -ne $a){exit 1}"
if errorlevel 1 goto :hiba_hash
echo  Ellenőrzőösszeg rendben – a letöltött csomag sértetlen.

rem ----- 3. Kicsomagolás (az adatok mappához NEM nyúl) -----
tasklist /fi "imagename eq RealPassGAL.exe" | find /i "RealPassGAL.exe" >nul
if not errorlevel 1 (
  echo.
  echo  A RealPassGAL most fut. Zárd be, aztán nyomj meg egy billentyűt.
  pause >nul
)
powershell -NoProfile -Command "Expand-Archive -Force -LiteralPath '%TMPD%\%ZIP%' -DestinationPath '%TMPD%\x'"
if errorlevel 1 goto :hiba_kicsomagolas
if not exist "%DEST%" mkdir "%DEST%"
robocopy "%TMPD%\x" "%DEST%" /E /XD adatok /NFL /NDL /NJH /NJS /NP >nul
if errorlevel 8 goto :hiba_masolas
echo  Fájlok a helyükön. (A meglévő kulcsaidhoz az adatok mappában nem nyúltam.)

rem ----- 4. WinFsp (a meghajtóként való felcsatoláshoz) -----
set "WINFSP_OK="
reg query "HKLM\SOFTWARE\WOW6432Node\WinFsp" /v InstallDir >nul 2>&1 && set "WINFSP_OK=1"
reg query "HKLM\SOFTWARE\WinFsp" /v InstallDir >nul 2>&1 && set "WINFSP_OK=1"
if defined WINFSP_OK (
  echo  WinFsp: már telepítve van.
) else (
  echo.
  echo  A felcsatoláshoz a WinFsp nevű ingyenes illesztő kell ^(rendszergazdai jog szükséges^).
  choice /c IN /n /m " Telepítsem most? [I]gen / [N]em: "
  if not errorlevel 2 (
    for %%f in ("%DEST%\winfsp\*.msi") do start "" /wait msiexec /i "%%~ff"
  )
)

rem ----- 5. Parancsikon -----
echo.
choice /c IN /n /m " Tegyek parancsikont az asztalra? [I]gen / [N]em: "
if not errorlevel 2 (
  powershell -NoProfile -Command "$s=(New-Object -ComObject WScript.Shell).CreateShortcut([Environment]::GetFolderPath('Desktop')+'\RealPassGAL.lnk'); $s.TargetPath='%DEST%\RealPassGAL.exe'; $s.WorkingDirectory='%DEST%'; $s.Save()"
)

rd /s /q "%TMPD%" 2>nul
echo.
echo  Kész! A program: %DEST%\RealPassGAL.exe
echo.
choice /c IN /n /m " Megnézed a bemutatót most? [I]gen / [N]em: "
if not errorlevel 2 start "" "%DEST%\RealPassGAL.exe" -demo
goto :eof

:megse
echo  Rendben, nem telepítettem semmit.
pause
goto :eof
:hiba_letoltes
echo  HIBA: a letöltés nem sikerült. Van internet? Elérhető a github.com?
pause & goto :eof
:hiba_hash
echo  HIBA: a letöltött csomag ellenőrzőösszege NEM egyezik. Biztonsági okból nem telepítem.
pause & goto :eof
:hiba_kicsomagolas
echo  HIBA: a kicsomagolás nem sikerült.
pause & goto :eof
:hiba_masolas
echo  HIBA: a fájlok másolása nem sikerült (jogosultság?). Próbálj másik mappát.
pause & goto :eof
