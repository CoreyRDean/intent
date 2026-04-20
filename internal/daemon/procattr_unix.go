//go:build unix

package daemon

import "syscall"

// procAttrNewGroup returns SysProcAttr that puts the child in its own
// process group, so signals to our PID don't reach it (and so we can
// signal -PGID to take down the entire subtree).
func procAttrNewGroup() *syscall.SysProcAttr {
	return &syscall.SysProcAttr{Setpgid: true}
}
