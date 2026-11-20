@echo off
rem Builds dist\xvoice_tip_x64.dll and dist\xvoice_tip_x86.dll with MSVC.
rem 32-bit applications load the x86 one; register both (elevated):
rem   regsvr32 dist\xvoice_tip_x64.dll
rem   %SystemRoot%\SysWOW64\regsvr32 dist\xvoice_tip_x86.dll
setlocal
for /f "usebackq tokens=*" %%i in (`"%ProgramFiles(x86)%\Microsoft Visual Studio\Installer\vswhere.exe" -latest -property installationPath`) do set VS=%%i
if not defined VS (echo Visual Studio with C++ tools not found & exit /b 1)
cd /d "%~dp0"
if not exist ..\dist mkdir ..\dist
for %%a in (x64 x86) do (
	if not exist obj\%%a mkdir obj\%%a
	rem Applications keep the registered DLL loaded and locked, but it can be
	rem renamed; the new build takes over the registered path.
	if exist ..\dist\xvoice_tip_%%a.dll move /y ..\dist\xvoice_tip_%%a.dll ..\dist\xvoice_tip_%%a.dll.old-%RANDOM% >nul
	cmd /c ""%VS%\VC\Auxiliary\Build\vcvarsall.bat" %%a >nul && cl /nologo /O2 /W4 /EHsc /MT /utf-8 /DUNICODE /D_UNICODE /LD /Foobj\%%a\ xvoice_tip.cpp /link /DEF:xvoice_tip.def /IMPLIB:obj\%%a\xvoice_tip.lib ole32.lib uuid.lib user32.lib advapi32.lib /OUT:..\dist\xvoice_tip_%%a.dll" || exit /b 1
)
