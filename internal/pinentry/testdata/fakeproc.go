// Command fakeproc sleeps for the number of seconds given as the first
// argument. Tests build it under names like "pinentry-curses" or "gpg" to
// emulate processes in the process table. A path given as the second
// argument is held open for the sleep, so a fake process can stand in for
// a pinentry dialog that holds a terminal open.
package main

import (
	"os"
	"strconv"
	"time"
)

func main() {
	if len(os.Args) < 2 {
		return
	}
	secs, err := strconv.Atoi(os.Args[1])
	if err != nil {
		return
	}
	var held *os.File
	if len(os.Args) > 2 {
		held, _ = os.OpenFile(os.Args[2], os.O_RDWR, 0)
	}
	time.Sleep(time.Duration(secs) * time.Second)
	if held != nil {
		_ = held.Close()
	}
}
