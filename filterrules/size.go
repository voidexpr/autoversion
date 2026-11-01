package filterrules

import (
	"errors"
	"math"
	"strconv"
	"strings"
)

// ParseSize parses a max-file-size value: an integer with a unit k, m or g
// (binary, either case), or none. It returns 0 for none.
func ParseSize(text string) (int64, error) {
	if text == "none" {
		return 0, nil
	}
	if text == "" {
		return 0, errors.New("a size like 10M, or none")
	}
	digits, unit := text[:len(text)-1], strings.ToLower(text[len(text)-1:])
	shift := map[string]uint{"k": 10, "m": 20, "g": 30}[unit]
	if shift == 0 {
		return 0, errors.New("the unit is k, m or g (binary), or the size is none")
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
