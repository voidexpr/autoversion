package filterrules

import (
	"errors"
	"math"
	"strconv"
	"strings"
)

// ParseSize parses a max-file-size value: an integer with a unit k, m, g or
// t, with or without a b suffix (binary, either case), or none. It returns
// 0 for none (ownit's parser doesn't take none: its directive handles it).
func ParseSize(text string) (int64, error) {
	if text == "none" {
		return 0, nil
	}
	lower := strings.TrimSuffix(strings.ToLower(text), "b")
	if lower == "" {
		return 0, errors.New("a size like 10M, or none")
	}
	digits, unit := lower[:len(lower)-1], lower[len(lower)-1:]
	shift := map[string]uint{"k": 10, "m": 20, "g": 30, "t": 40}[unit]
	if shift == 0 {
		return 0, errors.New("the unit is k, m, g or t, or kb, mb, gb, tb (binary), or the size is none")
	}
	n, err := strconv.ParseInt(digits, 10, 64)
	if err != nil || n <= 0 || digits[0] == '+' {
		return 0, errors.New("an integer before the unit, 1 or more")
	}
	if n > math.MaxInt64>>shift {
		return 0, errors.New("too large")
	}
	return n << shift, nil
}
