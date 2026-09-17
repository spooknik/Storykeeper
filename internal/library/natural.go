package library

// naturalLess reports whether a should sort before b using natural
// ("numeric-aware") ordering: runs of digits are compared by numeric value
// rather than lexicographically, so "2.mp3" sorts before "10.mp3".
func naturalLess(a, b string) bool {
	ai, bi := 0, 0
	for ai < len(a) && bi < len(b) {
		ca, cb := a[ai], b[bi]
		if isDigit(ca) && isDigit(cb) {
			as := ai
			for ai < len(a) && isDigit(a[ai]) {
				ai++
			}
			bs := bi
			for bi < len(b) && isDigit(b[bi]) {
				bi++
			}
			na := trimLeadingZeros(a[as:ai])
			nb := trimLeadingZeros(b[bs:bi])
			if len(na) != len(nb) {
				return len(na) < len(nb)
			}
			if na != nb {
				return na < nb
			}
			// Numerically equal (possibly differing only in leading zeros);
			// keep comparing the rest of the string.
			continue
		}
		if ca != cb {
			return ca < cb
		}
		ai++
		bi++
	}
	return len(a)-ai < len(b)-bi
}

func isDigit(c byte) bool { return c >= '0' && c <= '9' }

func trimLeadingZeros(s string) string {
	i := 0
	for i < len(s)-1 && s[i] == '0' {
		i++
	}
	return s[i:]
}
