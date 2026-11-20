//go:build windows && !amd64

package tip

// No ARM64 build of the service yet.
var dll64, dll32 []byte
