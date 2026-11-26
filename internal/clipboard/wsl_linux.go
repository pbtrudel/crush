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
// encoded bytes straight to stdout; exiting 1 means "no image", which keeps
// that case distinct from an empty stream. GetImage alone is not enough: it
// only reads bitmap formats, so it returns null for images that browsers and
// Electron apps publish solely as "PNG", and for an image file copied in
// Explorer. Those are tried before and after it, PNG first because it keeps
// transparency that the bitmap round trip drops.
const powershellImageScript = `
Add-Type -AssemblyName System.Windows.Forms
Add-Type -AssemblyName System.Drawing
$out = [Console]::OpenStandardOutput()
function Emit([byte[]]$bytes) {
	if ($null -eq $bytes -or $bytes.Length -eq 0) { return }
	$out.Write($bytes, 0, $bytes.Length)
	$out.Flush()
	exit 0
}
$data = [System.Windows.Forms.Clipboard]::GetDataObject()
if ($null -eq $data) { exit 1 }
if ($data.GetDataPresent('PNG')) {
	$png = $data.GetData('PNG')
	if ($png -is [System.IO.Stream]) {
		if ($png.CanSeek) { $png.Position = 0 }
		$ms = New-Object System.IO.MemoryStream
		$png.CopyTo($ms)
		Emit $ms.ToArray()
	}
}
$img = [System.Windows.Forms.Clipboard]::GetImage()
if ($null -ne $img) {
	$ms = New-Object System.IO.MemoryStream
	$img.Save($ms, [System.Drawing.Imaging.ImageFormat]::Png)
	Emit $ms.ToArray()
}
if ([System.Windows.Forms.Clipboard]::ContainsFileDropList()) {
	foreach ($file in [System.Windows.Forms.Clipboard]::GetFileDropList()) {
		if ($file -match '\.(png|jpe?g)$' -and (Test-Path -LiteralPath $file -PathType Leaf)) {
			Emit ([System.IO.File]::ReadAllBytes($file))
		}
	}
}
exit 1
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
