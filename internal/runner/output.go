package runner

import (
	"bytes"
	"unicode/utf8"
)

type Normalizer struct {
	pending []byte
}

func (n *Normalizer) Append(previous, chunk string) string {
	input := make([]byte, 0, len(n.pending)+len(chunk))
	input = append(input, n.pending...)
	input = append(input, chunk...)
	n.pending = nil
	output := []byte(previous)

	for len(input) > 0 {
		switch input[0] {
		case '\x1b':
			consumed, keep, complete := parseEscape(input)
			if !complete {
				n.pending = append(n.pending, input...)
				return string(output)
			}
			if keep {
				output = append(output, input[:consumed]...)
			}
			input = input[consumed:]
		case '\r':
			if newline := bytes.LastIndexByte(output, '\n'); newline >= 0 {
				output = output[:newline+1]
			} else {
				output = output[:0]
			}
			input = input[1:]
		case '\t', '\n':
			output = append(output, input[0])
			input = input[1:]
		default:
			if input[0] < 0x20 || input[0] == 0x7f {
				input = input[1:]
				continue
			}
			if !utf8.FullRune(input) {
				n.pending = append(n.pending, input...)
				return string(output)
			}
			_, size := utf8.DecodeRune(input)
			if size == 1 && input[0] >= utf8.RuneSelf {
				input = input[1:]
				continue
			}
			output = append(output, input[:size]...)
			input = input[size:]
		}
	}

	return string(output)
}

func parseEscape(input []byte) (consumed int, keep, complete bool) {
	if len(input) == 1 {
		return 0, false, false
	}

	switch input[1] {
	case '[':
		for i := 2; i < len(input); i++ {
			if input[i] < 0x40 || input[i] > 0x7e {
				continue
			}
			return i + 1, input[i] == 'm' && validSGRParameters(input[2:i]), true
		}
		return 0, false, false
	case ']':
		for i := 2; i < len(input); i++ {
			if input[i] == '\a' {
				return i + 1, false, true
			}
			if input[i] == '\x1b' {
				if i+1 == len(input) {
					return 0, false, false
				}
				if input[i+1] == '\\' {
					return i + 2, false, true
				}
			}
		}
		return 0, false, false
	default:
		return 1, false, true
	}
}

func validSGRParameters(parameters []byte) bool {
	for _, b := range parameters {
		if (b < '0' || b > '9') && b != ';' {
			return false
		}
	}
	return true
}
