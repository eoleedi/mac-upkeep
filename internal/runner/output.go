package runner

import (
	"strconv"
	"strings"
	"unicode/utf8"
)

type Normalizer struct {
	pending     []byte
	row         int
	initialized bool
	replaceLine bool
}

func (n *Normalizer) Append(previous, chunk string) string {
	input := make([]byte, 0, len(n.pending)+len(chunk))
	input = append(input, n.pending...)
	input = append(input, chunk...)
	n.pending = nil
	lines := strings.Split(previous, "\n")
	if !n.initialized {
		n.row = len(lines) - 1
		n.initialized = true
	}
	write := func(text string) {
		if n.replaceLine {
			lines[n.row] = ""
			n.replaceLine = false
		}
		lines[n.row] += text
	}

	for len(input) > 0 {
		switch input[0] {
		case '\x1b':
			consumed, keep, complete := parseEscape(input)
			if !complete {
				n.pending = append(n.pending, input...)
				return strings.Join(lines, "\n")
			}
			if keep {
				write(string(input[:consumed]))
			} else if input[1] == '[' {
				n.control(&lines, string(input[2:consumed-1]), input[consumed-1])
			}
			input = input[consumed:]
		case '\r':
			n.replaceLine = true
			input = input[1:]
		case '\n':
			n.row++
			if n.row == len(lines) {
				lines = append(lines, "")
			}
			n.replaceLine = true
			input = input[1:]
		case '\t':
			write("\t")
			input = input[1:]
		default:
			if input[0] < 0x20 || input[0] == 0x7f {
				input = input[1:]
				continue
			}
			if !utf8.FullRune(input) {
				n.pending = append(n.pending, input...)
				return strings.Join(lines, "\n")
			}
			_, size := utf8.DecodeRune(input)
			if size == 1 && input[0] >= utf8.RuneSelf {
				input = input[1:]
				continue
			}
			write(string(input[:size]))
			input = input[size:]
		}
	}

	return strings.Join(lines, "\n")
}

func (n *Normalizer) Flush(previous string) string {
	n.pending = nil
	return previous
}

// Interpret line-oriented redraws rather than passing cursor controls through to
// the outer TUI. In particular, Homebrew uses F and G to redraw download rows.
func (n *Normalizer) control(lines *[]string, parameters string, command byte) {
	count := 1
	if parameters != "" {
		value, err := strconv.Atoi(parameters)
		if err != nil || value < 0 {
			return
		}
		if value > 0 {
			count = value
		}
	}
	switch command {
	case 'A', 'F':
		n.row = max(0, n.row-count)
		n.replaceLine = true
	case 'B', 'E':
		n.row = min(len(*lines)-1, n.row+count)
		n.replaceLine = true
	case 'G':
		if count == 1 {
			n.replaceLine = true
		}
	case 'K':
		if parameters == "2" || ((parameters == "" || parameters == "0") && n.replaceLine) {
			(*lines)[n.row] = ""
		}
	case 'J':
		if parameters == "" || parameters == "0" {
			*lines = (*lines)[:n.row+1]
			if n.replaceLine {
				(*lines)[n.row] = ""
			}
		}
	}
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
		if (b < '0' || b > '9') && b != ';' && b != ':' {
			return false
		}
	}
	return true
}
