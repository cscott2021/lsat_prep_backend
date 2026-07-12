package billing

import "strconv"

func int64ToStr(v int64) string { return strconv.FormatInt(v, 10) }

func strToInt(s string) int {
	n, err := strconv.Atoi(s)
	if err != nil {
		return 0
	}
	return n
}
