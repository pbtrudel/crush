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

// JailRunPath is where a jailed run shows its own run directory. /tmp is one
// of DefaultMasks, so the mount point lives in the jail's private tmpfs, and
// no path the child is given names the batch layout that advertises the
// sibling runs of the same batch. /proc/self/mountinfo still names the host
// directory the bind comes from, so a child that reads it learns the batch
// and the run's own name, though no sibling.
const JailRunPath = "/tmp/crush-run"

// Bind is a host directory the jail shows at another path inside it.
type Bind struct {
	// Host is the directory on the host, opened before the masks land: a
	// path under a mask no longer names it afterwards.
	Host string
	// Jail is where the host directory appears inside the jail.
	Jail string
	// Dir is the child's working directory, a path inside Jail. The helper
	// starts in Host, so it has to move the child after the mount for the
	// path it inherits to name the jail's own tree.
	Dir string
}
