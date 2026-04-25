@rem startnet.cmd — first thing WinPE runs after wpeinit. Bring up
@rem networking, then hand off to deploy.cmd which fetches its own
@rem latest version from the deploy server (so we don't have to rebuild
@rem boot.wim every time we tweak the deploy logic).
@rem
@rem _DEPLOY_API, _DEPLOY_JOB_ID, _DEPLOY_TOKEN are set as environment
@rem variables by wimboot's imgargs (see services/http-boot/scripts/render
@rem windowsTpl).

@echo off
wpeinit

if not defined _DEPLOY_API (
    echo FATAL: _DEPLOY_API not set
    pause
    exit /b 1
)
if not defined _DEPLOY_JOB_ID (
    echo FATAL: _DEPLOY_JOB_ID not set
    pause
    exit /b 1
)

echo deployserver WinPE starting (job %_DEPLOY_JOB_ID%)

rem Wait up to 30s for an IPv4 address.
set RETRIES=30
:wait_dhcp
ipconfig | findstr /R /C:"IPv4 Address" >nul
if %ERRORLEVEL%==0 goto have_ip
ping -n 2 127.0.0.1 >nul
set /A RETRIES=%RETRIES% - 1
if %RETRIES% LEQ 0 (
    echo FATAL: no DHCP lease in 60s
    pause
    exit /b 2
)
goto wait_dhcp

:have_ip
ipconfig | findstr /R /C:"IPv4 Address"

rem Pull the latest deploy.cmd from the server. We trust the network
rem path because:
rem   1. _DEPLOY_API points at the deploy server's HTTPS endpoint, whose
rem      cert was pinned at iPXE time (chain to embedded CA).
rem   2. The endpoint requires _DEPLOY_TOKEN as a query param, which is
rem      a one-shot bound to this deployment.
mkdir X:\deploy 2>nul
rem Token via Authorization header, not query string. SECURITY.md §4 #2.
curl.exe --cacert X:\deploy\deploy-ca.pem ^
    --silent --show-error --fail ^
    --max-time 30 ^
    --header "Authorization: Bearer %_DEPLOY_TOKEN%" ^
    -o X:\deploy\deploy.cmd ^
    "%_DEPLOY_API%/v1/jobs/%_DEPLOY_JOB_ID%/deploy.cmd"

if not exist X:\deploy\deploy.cmd (
    echo FATAL: could not fetch deploy.cmd
    pause
    exit /b 3
)

call X:\deploy\deploy.cmd
set RC=%ERRORLEVEL%

if %RC% NEQ 0 (
    echo deploy.cmd failed with exit code %RC%
    pause
    exit /b %RC%
)

echo Deployment complete. Rebooting in 10s.
ping -n 11 127.0.0.1 >nul
wpeutil reboot
