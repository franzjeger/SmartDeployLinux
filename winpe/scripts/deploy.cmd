@rem deploy.cmd — fetched fresh by startnet.cmd from the deploy server
@rem on every WinPE boot. Performs the actual install:
@rem   1. partition + format the target disk (GPT, ESP + MSR + Windows)
@rem   2. apply the WIM with DISM
@rem   3. fetch driver packs matched to this hardware, inject with DISM
@rem   4. write unattend.xml into Panther
@rem   5. bcdboot
@rem
@rem Auth: every API call carries `Authorization: Bearer %TOKEN%` (NOT
@rem ?token=... in the query string — that pattern leaks via web logs;
@rem SECURITY.md §4 #2). The job id stays in the URL path because routes
@rem already include it.

@echo off
setlocal enableextensions enabledelayedexpansion

set API=%_DEPLOY_API%
set JOB=%_DEPLOY_JOB_ID%
set TOKEN=%_DEPLOY_TOKEN%
set AUTH=Authorization: Bearer %TOKEN%

call :report imaging "WinPE bootstrap; identifying hardware"

rem --- 1. fingerprint -----------------------------------------------
for /f "tokens=2 delims==" %%v in ('wmic baseboard get manufacturer /value ^| find "="') do set DMI_VENDOR=%%v
for /f "tokens=2 delims==" %%v in ('wmic baseboard get product       /value ^| find "="') do set DMI_BOARD=%%v
for /f "tokens=2 delims==" %%v in ('wmic computersystem get manufacturer /value ^| find "="') do set CS_VENDOR=%%v
for /f "tokens=2 delims==" %%v in ('wmic computersystem get model        /value ^| find "="') do set CS_MODEL=%%v

set FP_FILE=X:\deploy\fp.json
echo {"dmi_vendor":"%CS_VENDOR%","dmi_product":"%CS_MODEL%","dmi_baseboard":"%DMI_BOARD%","pci":[]}> %FP_FILE%

rem --- 2. ask API for plan ------------------------------------------
call :report imaging "fetching deployment plan"

set PLAN=X:\deploy\plan.json
curl.exe --cacert X:\deploy\deploy-ca.pem --silent --show-error --fail ^
    --max-time 60 ^
    --header "%AUTH%" ^
    --data-binary @%FP_FILE% --header "Content-Type: application/json" ^
    -o %PLAN% ^
    "%API%/v1/jobs/%JOB%/plan"
if errorlevel 1 ( call :fatal 10 "plan fetch failed" )

rem --- 3. partition target disk -------------------------------------
call :report imaging "partitioning disk 0 (DESTRUCTIVE)"

(
echo select disk 0
echo clean
echo convert gpt
echo create partition efi size=260
echo format quick fs=fat32 label=System
echo assign letter=S
echo create partition msr size=16
echo create partition primary
echo shrink minimum=500
echo format quick fs=ntfs label=Windows
echo assign letter=W
echo create partition primary
echo format quick fs=ntfs label=Recovery
echo assign letter=R
echo exit
) | diskpart >X:\deploy\diskpart.log 2>&1
if errorlevel 1 ( call :fatal 11 "diskpart failed" )

rem --- 4. download + apply WIM --------------------------------------
call :report imaging "downloading install.wim"

curl.exe --cacert X:\deploy\deploy-ca.pem --silent --show-error --fail ^
    --max-time 0 ^
    --header "%AUTH%" ^
    -o X:\deploy\install.wim ^
    "%API%/v1/jobs/%JOB%/image.wim"
if errorlevel 1 ( call :fatal 12 "wim download failed" )

call :report imaging "applying WIM"
DISM /Apply-Image /ImageFile:X:\deploy\install.wim /Index:1 /ApplyDir:W:\
if errorlevel 1 ( call :fatal 13 "DISM Apply-Image failed" )

rem --- 5. download + inject driver packs ----------------------------
call :report imaging "fetching driver packs"

mkdir W:\Drivers 2>nul
curl.exe --cacert X:\deploy\deploy-ca.pem --silent --show-error --fail ^
    --max-time 0 ^
    --header "%AUTH%" ^
    -o X:\deploy\drivers.zip ^
    "%API%/v1/jobs/%JOB%/drivers.zip"
if errorlevel 1 (
    rem Driver pack fetch failure is non-fatal — the OS may have built-in drivers.
    call :report imaging "no driver pack fetched; continuing"
) else (
    powershell -NoProfile -Command "Expand-Archive -Path X:\deploy\drivers.zip -DestinationPath W:\Drivers -Force"
    DISM /Image:W:\ /Add-Driver /Driver:W:\Drivers /Recurse
)

rem --- 6. unattend.xml ---------------------------------------------
call :report imaging "writing unattend.xml"

mkdir W:\Windows\Panther 2>nul
curl.exe --cacert X:\deploy\deploy-ca.pem --silent --show-error --fail ^
    --max-time 30 ^
    --header "%AUTH%" ^
    -o W:\Windows\Panther\unattend.xml ^
    "%API%/v1/jobs/%JOB%/unattend.xml"
if errorlevel 1 ( call :fatal 14 "unattend fetch failed" )

rem --- 7. bcdboot --------------------------------------------------
call :report imaging "writing boot loader"

bcdboot W:\Windows /s S: /f UEFI
if errorlevel 1 ( call :fatal 15 "bcdboot failed" )

call :report post_install "WinPE work complete; rebooting into specialize"

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
