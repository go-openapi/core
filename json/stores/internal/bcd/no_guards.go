//go:build !guards && !writerguards

package bcd

// code assertions turned off

// code assertions carry out extra checks with a panic outcome

func assertBCDOutCapacity(_ []byte, _ int) {}
func assertBCDDigit(_ bool, _ byte)        {}
func assertBCDNibble(_ bool, _ byte)       {}
