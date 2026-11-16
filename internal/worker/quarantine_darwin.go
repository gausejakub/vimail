package worker

import (
	"fmt"
	"os/exec"
	"time"
)

// markFromInternet sets com.apple.quarantine on a file written from a
// message, as Mail and browsers do. Gatekeeper only checks files that carry
// it: without it, an application or script saved from an attachment starts
// with no warning.
func markFromInternet(path string) error {
	value := fmt.Sprintf("0081;%x;vimail;", time.Now().Unix())
	return exec.Command("/usr/bin/xattr", "-w", "com.apple.quarantine", value, path).Run()
}
