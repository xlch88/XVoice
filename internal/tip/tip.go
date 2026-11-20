// Package tip installs the XVoice text service (tip/xvoice_tip.cpp), the TSF
// component through which text reaches fields as a composition. Its DLLs are
// built into the executable; installing writes them to a directory named
// after their content and registers them machine-wide, which needs elevation.
//
// Updating never asks a DLL to unload itself (TSF still holds pointers into
// it): unregistering makes TSF deactivate the service in every running
// application, and registering the new copy makes TSF load that one instead.
// The old file stays mapped, inert, until those processes exit.
package tip

// Command-line arguments that make xvoice.exe install or uninstall the
// service and exit; the settings window runs them elevated.
const (
	InstallArg   = "--tip-install"
	UninstallArg = "--tip-uninstall"
)

// WindowClass is the class of the message-only window the service keeps on
// each thread with a text focus.
const WindowClass = "XVoiceTipWindow"

type Status struct {
	Supported bool // This build carries the service.
	Installed bool
	Current   bool // The registered copy is the one in this build.
	Active    int  // Threads where the service is running right now.
}
