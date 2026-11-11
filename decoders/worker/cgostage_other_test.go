//go:build !linux

package worker

// Only the Linux stage depends on how the binary is linked.
func stageLinkedWithCgo() bool { return false }
