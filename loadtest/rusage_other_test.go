//go:build !unix

package loadtest

// processCPUSeconds is unavailable where the platform exposes no process CPU
// accounting; the report leaves the field at zero instead of inventing one.
func processCPUSeconds() float64 { return 0 }
