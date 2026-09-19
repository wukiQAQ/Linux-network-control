//go:build windows

package updater

import "os/exec"

// execCommand 在 Windows 上执行外部命令（本地开发用，真实场景服务端运行在 Linux）。
func execCommand(name string, args ...string) error {
	return exec.Command(name, args...).Run()
}
