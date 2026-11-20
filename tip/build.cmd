@echo off
rem Builds internal\tip\xvoice_tip_x64.dll and xvoice_tip_x86.dll with MSVC.
rem They are committed and embedded into xvoice.exe, which installs them from
rem its settings window, so building the Go program needs no C++ compiler.
rem Rebuild after changing xvoice_tip.cpp.
setlocal
for /f "usebackq tokens=*" %%i in (`"%ProgramFiles(x86)%\Microsoft Visual Studio\Installer\vswhere.exe" -latest -property installationPath`) do set VS=%%i
if not defined VS (echo Visual Studio with C++ tools not found & exit /b 1)
cd /d "%~dp0"
for %%a in (x64 x86) do (
	if not exist obj\%%a mkdir obj\%%a
	rem /Brepro: the same source gives the same bytes, so the installed version only changes with the code.
	cmd /c ""%VS%\VC\Auxiliary\Build\vcvarsall.bat" %%a >nul && cl /nologo /Brepro /O2 /W4 /EHsc /MT /utf-8 /DUNICODE /D_UNICODE /LD /Foobj\%%a\ xvoice_tip.cpp /link /Brepro /DEF:xvoice_tip.def /IMPLIB:obj\%%a\xvoice_tip.lib ole32.lib oleaut32.lib uuid.lib user32.lib advapi32.lib /OUT:obj\%%a\xvoice_tip.dll" || exit /b 1
	copy /y obj\%%a\xvoice_tip.dll ..\internal\tip\xvoice_tip_%%a.dll >nul || exit /b 1
)
