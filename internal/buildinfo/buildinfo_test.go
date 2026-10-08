package buildinfo

import (
	"strings"
	"testing"
)

// TestFeaturesWellFormed 校验能力清单本身的自洽：
// 名称非空、无重复、且离线回放能力已声明。
// 客户端依赖这些字符串决定"能不能用某项功能"，拼错或漏声明都会让功能静默消失
// （表现是按钮不出现，而不是报错），所以这里对清单本身设一道关。
func TestFeaturesWellFormed(t *testing.T) {
	seen := map[string]bool{}
	for _, f := range Features {
		if strings.TrimSpace(f) == "" {
			t.Fatal("能力清单里出现空名称")
		}
		if seen[f] {
			t.Fatalf("能力清单里出现重复项: %q", f)
		}
		seen[f] = true
	}
	if !seen["capture.offline"] {
		t.Error("缺少 capture.offline：客户端将无法判断本机离线回放能力")
	}
}

// TestVersionFormat 校验版本号形如 主.次.修订（AGENTS.md 的编号规则）。
func TestVersionFormat(t *testing.T) {
	if parts := strings.Split(Version, "."); len(parts) != 3 {
		t.Fatalf("版本号应形如 0.23.3，实际 %q", Version)
	}
}
