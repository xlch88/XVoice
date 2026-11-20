//go:build windows && amd64

package tip

import _ "embed"

// Built by tip/build.cmd. An x64 system also runs 32-bit applications.
var (
	//go:embed xvoice_tip_x64.dll
	dll64 []byte
	//go:embed xvoice_tip_x86.dll
	dll32 []byte
)
