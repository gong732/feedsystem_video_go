package handlers

import "strconv"

// parseLimit 解析 limit 参数，越界兜底
func parseLimit(s string) (int, error) {
	v, err := strconv.Atoi(s)
	if err != nil || v < 1 || v > 50 {
		return 10, err
	}
	return v, nil
}
