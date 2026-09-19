package capture

import "testing"

// TestNormalizeReaders 验证多队列并发度被夹在 [1,8]：非法值回退单队列，过大值截断到上限。
func TestNormalizeReaders(t *testing.T) {
	cases := []struct {
		in   int
		want int
	}{
		{0, MinReaders},
		{-3, MinReaders},
		{1, 1},
		{2, 2},
		{4, 4},
		{MaxReaders, MaxReaders},
		{MaxReaders + 1, MaxReaders},
		{64, MaxReaders},
	}
	for _, c := range cases {
		if got := normalizeReaders(c.in); got != c.want {
			t.Errorf("normalizeReaders(%d)=%d, want %d", c.in, got, c.want)
		}
	}
	if MinReaders != 1 || MaxReaders != 8 {
		t.Errorf("并发度范围应为 [1,8]，实际 [%d,%d]", MinReaders, MaxReaders)
	}
}
