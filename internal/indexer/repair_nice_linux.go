//go:build linux

package indexer

import "syscall"

// lowerPriority yields CPU to interactive work while the background
// health loop runs. Best effort — failure is harmless.
func lowerPriority() {
	_ = syscall.Setpriority(syscall.PRIO_PROCESS, 0, 10)
}
