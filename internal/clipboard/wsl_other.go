//go:build !linux

package clipboard

// readWSL is only meaningful on Linux, where the process can reach the
// Windows clipboard through WSL interop.
func readWSL(Format) ([]byte, error) {
	return nil, ErrUnsupported
}
