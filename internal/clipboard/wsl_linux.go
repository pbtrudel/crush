//go:build linux

package clipboard

import (
	"bytes"
	"context"
	"errors"
	"os"
	"os/exec"
	"time"
)

// wslReadTimeout bounds a Windows PowerShell round trip. Startup alone costs
// a few hundred milliseconds, so a short timeout would reject real reads.
const wslReadTimeout = 10 * time.Second

// powershellImageScript asks Windows for the clipboard image and writes the
// encoded PNG straight to stdout. GetImage returns null when the clipboard
// holds no image; exiting 1 keeps that case distinct from an empty stream.
const powershellImageScript = `
Add-Type -AssemblyName System.Windows.Forms
Add-Type -AssemblyName System.Drawing
$img = [System.Windows.Forms.Clipboard]::GetImage()
if ($null -eq $img) { exit 1 }
$ms = New-Object System.IO.MemoryStream
$img.Save($ms, [System.Drawing.Imaging.ImageFormat]::Png)
$out = [Console]::OpenStandardOutput()
$bytes = $ms.ToArray()
$out.Write($bytes, 0, $bytes.Length)
$out.Flush()
`

// isWSL reports whether this Linux process runs under the Windows Subsystem
// for Linux, where the Windows clipboard is reachable through interop.
func isWSL() bool {
	return os.Getenv("WSL_DISTRO_NAME") != "" || os.Getenv("WSL_INTEROP") != ""
}

// readWSL reads an image from the Windows clipboard through PowerShell.
// WSLg publishes Windows images to Linux as image/bmp only, while the native
// clipboard backend requests image/png, so an image copied in Windows never
// reaches it. Text is left alone: WSLg already bridges text both ways.
func readWSL(f Format) ([]byte, error) {
	if f != FormatImage {
		return nil, ErrUnsupported
	}
	if !isWSL() {
		return nil, ErrUnsupported
	}
	ps, err := powershellPath()
	if err != nil {
		return nil, ErrUnsupported
	}

	ctx, cancel := context.WithTimeout(context.Background(), wslReadTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, ps,
		"-NoProfile", "-NonInteractive", "-STA", "-Command", powershellImageScript,
	)
	var stdout bytes.Buffer
	cmd.Stdout = &stdout
	if err := cmd.Run(); err != nil {
		// A non-zero exit is how the script reports "no image", and a
		// timeout means the clipboard never answered; both are empty as
		// far as the caller is concerned.
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) || ctx.Err() != nil {
			return nil, ErrEmpty
		}
		return nil, ErrUnsupported
	}
	if stdout.Len() == 0 {
		return nil, ErrEmpty
	}
	return stdout.Bytes(), nil
}

// powershellPath finds the Windows PowerShell interpreter. Interop normally
// puts it on PATH; the well-known location covers shells that do not inherit
// the Windows path entries.
func powershellPath() (string, error) {
	if path, err := exec.LookPath("powershell.exe"); err == nil {
		return path, nil
	}
	const fallback = "/mnt/c/Windows/System32/WindowsPowerShell/v1.0/powershell.exe"
	if _, err := os.Stat(fallback); err == nil {
		return fallback, nil
	}
	return "", ErrUnsupported
}
