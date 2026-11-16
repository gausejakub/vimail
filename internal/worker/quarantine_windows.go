package worker

import "os"

// markFromInternet writes the Mark of the Web (the Zone.Identifier stream,
// zone 3: Internet) on a file written from a message, as Outlook and
// browsers do. Office's Protected View and macro blocking and SmartScreen
// only apply to files that carry it.
func markFromInternet(path string) error {
	return os.WriteFile(path+":Zone.Identifier", []byte("[ZoneTransfer]\r\nZoneId=3\r\n"), 0600)
}
