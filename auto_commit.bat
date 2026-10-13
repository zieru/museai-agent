@echo off
setlocal enabledelayedexpansion

echo ========================================
echo Auto Commit, Version Increment, and Push
echo ========================================

:: 1. Menambahkan semua perubahan
echo.
echo [1/4] Menambahkan file yang berubah (git add .)...
git add .

:: 2. Meminta input pesan commit dan melakukan commit
set /p commit_msg="Masukkan pesan commit (tekan Enter untuk menggunakan waktu saat ini): "
if "%commit_msg%"=="" (
    set commit_msg=Auto commit: %date% %time%
)
echo.
echo [2/4] Melakukan commit...
git commit -m "!commit_msg!"

:: 3. Mengambil versi semver tertinggi dari remote GitHub dan lokal
echo.
echo [3/4] Mengambil versi tag terakhir dari GitHub dan lokal...
set CURRENT_TAG=
set NEW_TAG=

for /f "tokens=1,2" %%A in ('powershell -NoProfile -ExecutionPolicy Bypass -File scripts\get_next_tag.ps1') do (
    set CURRENT_TAG=%%A
    set NEW_TAG=%%B
)

if "!NEW_TAG!"=="" (
    set CURRENT_TAG=v1.0.0
    set NEW_TAG=v1.0.1
)

echo Tag tertinggi saat ini : !CURRENT_TAG!
echo Tag baru yang dibuat   : !NEW_TAG!

:: Membuat tag baru
git tag !NEW_TAG!

:: 4. Mendorong (push) kode beserta tag ke GitHub
echo.
echo [4/4] Mendorong kode dan tag ke GitHub...
git push origin HEAD
git push origin !NEW_TAG!

echo.
echo ========================================
echo Selesai: Kode dan tag (!NEW_TAG!) berhasil di-push.
echo ========================================
pause
endlocal
