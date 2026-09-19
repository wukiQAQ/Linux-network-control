//go:build !windows

package updater

import "os/exec"

// execCommand 在 Linux/macOS 上执行外部命令（用于 systemctl 重启）。
func execCommand(name string, args ...string) error {
	return exec.Command(name, args...).Run()
}
