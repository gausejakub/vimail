//go:build !darwin && !windows

package worker

// markFromInternet has nothing to set here: there is no quarantine marker
// that Linux desktops act on. Files are written 0600, so they are never
// executable as saved.
func markFromInternet(string) error { return nil }
