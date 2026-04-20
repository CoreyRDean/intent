//go:build !unix

package daemon

import "syscall"

// procAttrNewGroup is a no-op on non-unix platforms (Windows). The
// daemon isn't supported there yet anyway.
func procAttrNewGroup() *syscall.SysProcAttr { return nil }
