// Package money formats amounts held as integer paise (INR).
package money

import (
	"fmt"
	"strings"
)

// Format formats paise as ₹1,23,456.78 (Indian digit grouping).
func Format(paise int64) string {
	sign := ""
	if paise < 0 {
		sign, paise = "-", -paise
	}
	whole := fmt.Sprint(paise / 100)
	if len(whole) > 3 {
		head, tail := whole[:len(whole)-3], whole[len(whole)-3:]
		var groups []string
		for len(head) > 2 {
			groups = append([]string{head[len(head)-2:]}, groups...)
			head = head[:len(head)-2]
		}
		if head != "" {
			groups = append([]string{head}, groups...)
		}
		whole = strings.Join(groups, ",") + "," + tail
	}
	return fmt.Sprintf("%s₹%s.%02d", sign, whole, paise%100)
}
