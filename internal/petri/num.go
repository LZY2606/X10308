package petri

import "strconv"

func writeInt(b interface{ WriteString(string) (int, error) }, v int64) {
	b.WriteString(strconv.FormatInt(v, 10))
}
