@rem capture.cmd — fetched fresh by startnet.cmd for capture jobs (the
@rem server serves this instead of deploy.cmd when the job kind is
@rem 'capture'). Captures the sysprepped OS volume as a golden WIM and
@rem uploads it to the deploy server's object store:
@rem   1. locate the offline Windows volume (the one with \Windows)
@rem   2. locate a scratch NTFS volume with enough free space for the WIM
@rem      (a second partition or attached USB disk — NOT the OS volume)
@rem   3. DISM /Capture-Image to scratch
@rem   4. certutil SHA-256 the WIM
@rem   5. POST /v1/jobs/{id}/capture-upload -> presigned PUT URL
@rem   6. curl -T the WIM to the object store
@rem   7. POST /v1/jobs/{id}/capture-complete -> server registers the
@rem      blob as a new version of the target image
@rem
@rem Auth: identical to deploy.cmd — bearer one-shot token, never a
@rem query-string token.

@echo off
setlocal enableextensions enabledelayedexpansion

set API=%_DEPLOY_API%
set JOB=%_DEPLOY_JOB_ID%
set TOKEN=%_DEPLOY_TOKEN%
set AUTH=Authorization: Bearer %TOKEN%

call :report imaging "WinPE capture bootstrap"

rem --- 1. find the offline Windows volume ---------------------------
set OSVOL=
for %%d in (C D E F G H I J K) do (
    if exist %%d:\Windows\System32\ntoskrnl.exe set OSVOL=%%d:
)
if not defined OSVOL ( call :fatal 20 "no offline Windows volume found" )
call :report imaging "capturing OS volume !OSVOL!"

rem --- 2. find a scratch volume (NTFS, not the OS volume, most free) --
set SCRATCH=
for %%d in (D E F G H I J K L M) do (
    if /i not "%%d:"=="!OSVOL!" (
        if exist %%d:\ (
            if not defined SCRATCH set SCRATCH=%%d:
        )
    )
)
if not defined SCRATCH ( call :fatal 21 "no scratch volume for the WIM; attach a second disk or USB" )
set WIM=!SCRATCH!\capture.wim
if exist !WIM! del /f !WIM!

rem --- 3. capture ---------------------------------------------------
call :report imaging "DISM capture to !WIM! (this takes a while)"
DISM /Capture-Image /ImageFile:!WIM! /CaptureDir:!OSVOL!\ /Name:"golden" /Compress:max /CheckIntegrity
if errorlevel 1 ( call :fatal 22 "DISM Capture-Image failed" )

rem --- 4. hash ------------------------------------------------------
call :report imaging "hashing WIM"
set SHA=
for /f "skip=1 tokens=1" %%h in ('certutil -hashfile !WIM! SHA256') do (
    if not defined SHA set SHA=%%h
)
if not defined SHA ( call :fatal 23 "certutil hash failed" )
for %%s in (!WIM!) do set SIZE=%%~zs

rem --- 5. register + presigned URL ----------------------------------
call :report imaging "registering blob (sha !SHA!)"
set RESP=X:\deploy\capture-upload.json
curl.exe --cacert X:\deploy\deploy-ca.pem --silent --show-error --fail ^
    --max-time 60 ^
    -X POST -H "%AUTH%" -H "Content-Type: application/json" ^
    --data-binary "{\"sha256\":\"!SHA!\",\"size_bytes\":!SIZE!}" ^
    -o %RESP% ^
    "%API%/v1/jobs/%JOB%/capture-upload"
if errorlevel 1 ( call :fatal 24 "capture-upload registration failed" )

rem Parse "upload_url" out of the JSON response (PowerShell ships in
rem the WinPE image; no jq needed).
set UPLOAD_URL=
for /f "usebackq delims=" %%u in (`powershell -NoProfile -Command "(Get-Content '%RESP%' | ConvertFrom-Json).upload_url"`) do set UPLOAD_URL=%%u
if not defined UPLOAD_URL ( call :fatal 25 "no upload_url in response" )

rem --- 6. upload ----------------------------------------------------
call :report imaging "uploading WIM to object store"
curl.exe --silent --show-error --fail --max-time 0 ^
    -T !WIM! "!UPLOAD_URL!"
if errorlevel 1 ( call :fatal 26 "WIM upload failed" )

rem --- 7. finalize --------------------------------------------------
call :report imaging "finalizing capture"
curl.exe --cacert X:\deploy\deploy-ca.pem --silent --show-error --fail ^
    --max-time 60 ^
    -X POST -H "%AUTH%" -H "Content-Type: application/json" ^
    --data-binary "{\"sha256\":\"!SHA!\"}" ^
    "%API%/v1/jobs/%JOB%/capture-complete"
if errorlevel 1 ( call :fatal 27 "capture-complete failed" )

del /f !WIM! 2>nul
call :report completed "golden image captured and registered"
exit /b 0

rem ----------------------------------------------------------------
:report
set PHASE=%~1
set MSG=%~2
curl.exe --cacert X:\deploy\deploy-ca.pem --silent --max-time 10 ^
    -X POST ^
    -H "Content-Type: application/json" ^
    -H "%AUTH%" ^
    --data-binary "{\"phase\":\"%PHASE%\",\"message\":\"%MSG%\"}" ^
    "%API%/v1/jobs/%JOB%/events" >nul 2>&1
exit /b 0

:fatal
set RC=%~1
set MSG=%~2
echo FATAL: %MSG%
call :report failed "%MSG%"
pause
exit /b %RC%
