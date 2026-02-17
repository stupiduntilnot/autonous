package tool

import (
	"crypto/sha1"
	"encoding/hex"
	"strings"
)

// Limits controls output truncation boundaries.
type Limits struct {
	MaxLines int
	MaxBytes int
}

// ApplyOutputLimits truncates text by line and byte limits.
func ApplyOutputLimits(text string, limits Limits) (out string, truncatedLines bool, truncatedBytes bool) {
	if limits.MaxLines > 0 {
		lines := strings.Split(text, "\n")
		if len(lines) > limits.MaxLines {
			lines = lines[:limits.MaxLines]
			text = strings.Join(lines, "\n")
			truncatedLines = true
		}
	}

	if limits.MaxBytes > 0 && len([]byte(text)) > limits.MaxBytes {
		b := []byte(text)
		text = string(b[:limits.MaxBytes])
		truncatedBytes = true
	}
	return text, truncatedLines, truncatedBytes
}

// BuildCursor returns a stable cursor hint for paginated follow-up reads.
func BuildCursor(key string, offset int64) string {
	sum := sha1.Sum([]byte(key))
	return hex.EncodeToString(sum[:8]) + ":" + toDecimal(offset)
}

func toDecimal(v int64) string {
	if v == 0 {
		return "0"
	}
	neg := v < 0
	if neg {
		v = -v
	}
	var digits [20]byte
	i := len(digits)
	for v > 0 {
		i--
		digits[i] = byte('0' + v%10)
		v /= 10
	}
	if neg {
		i--
		digits[i] = '-'
	}
	return string(digits[i:])
}
