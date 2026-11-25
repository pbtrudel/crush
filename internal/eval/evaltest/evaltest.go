// Package evaltest holds the helpers the tests of the eval packages share.
package evaltest

import (
	"os"
	"syscall"
	"testing"

	"github.com/stretchr/testify/require"
)

// WriteExecutable writes data to path as an executable script a test can
// run.
//
// The write holds syscall.ForkLock for reading, the lock the runtime holds
// for writing around a fork. A write leaves the file open until it returns,
// and a fork in another goroutine during that window leaves the child
// holding the write descriptor until the child execs. An exec of the file in
// that window then fails with ETXTBSY (https://go.dev/issue/22315), which is
// what flaked the parallel tests of these packages: a fork inherited the
// descriptor of a fake go another test had just written, and that test's own
// exec of the go then failed. The lock makes the write and any fork take
// turns, so no fork can inherit the descriptor.
func WriteExecutable(t *testing.T, path string, data []byte) {
	t.Helper()
	syscall.ForkLock.RLock()
	err := os.WriteFile(path, data, 0o700)
	syscall.ForkLock.RUnlock()
	require.NoError(t, err)
}
