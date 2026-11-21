package netjail

// DefaultMasks are the directories the jail always hides behind an empty
// tmpfs, so a filesystem unix socket under one of them cannot be reached.
// /var/run is skipped when it is a symlink into /run.
var DefaultMasks = []string{"/run", "/var/run", "/tmp", "/mnt/wslg", "/run/user"}

// Mask is a directory the jail hides behind an empty tmpfs, and the paths
// under it that stay visible.
type Mask struct {
	// Path is the directory the jail masks.
	Path string
	// Keep are absolute paths under Path that the jail bind mounts back
	// from the host once the mask is in place, so a run's own directory
	// stays readable and writable under a masked parent.
	Keep []string
}
