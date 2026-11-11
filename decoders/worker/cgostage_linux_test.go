package worker

import "syscall"

// stageLinkedWithCgo reports whether this test binary links cgo. It serves as
// its own Linux stage, and a stage confines every thread of its process with
// syscall.AllThreadsSyscall, which the runtime refuses, with ENOTSUP and
// without making the call, in a program linked with cgo.
func stageLinkedWithCgo() bool {
	_, _, errno := syscall.AllThreadsSyscall(syscall.SYS_GETPID, 0, 0, 0)
	return errno == syscall.ENOTSUP
}
