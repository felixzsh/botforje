//go:build unix

package loadtest

import "syscall"

// processCPUSeconds is the CPU time this process consumed, user plus system,
// read from the kernel's own accounting.
func processCPUSeconds() float64 {
	var usage syscall.Rusage
	if err := syscall.Getrusage(syscall.RUSAGE_SELF, &usage); err != nil {
		return 0
	}
	return float64(usage.Utime.Sec) + float64(usage.Utime.Usec)/1e6 +
		float64(usage.Stime.Sec) + float64(usage.Stime.Usec)/1e6
}
