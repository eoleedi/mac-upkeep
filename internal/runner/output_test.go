package runner

import "testing"

func TestNormalizerAppend(t *testing.T) {
	tests := []struct {
		name, previous, chunk, want string
	}{
		{"plain", "one\n", "two\n", "one\ntwo\n"},
		{"progress replaces current line", "downloading 10%", "\rdownloading 80%", "downloading 80%"},
		{"newline commits progress", "downloading 80%", "\ndone\n", "downloading 80%\ndone\n"},
		{"strip cursor control", "", "safe\x1b[2Jtext", "safetext"},
		{"retain sgr color", "", "\x1b[32mok\x1b[0m", "\x1b[32mok\x1b[0m"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var normalizer Normalizer
			if got := normalizer.Append(tt.previous, tt.chunk); got != tt.want {
				t.Fatalf("Append() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestNormalizerBuffersSplitUTF8AndANSI(t *testing.T) {
	var normalizer Normalizer
	got := normalizer.Append("", string([]byte{0xe2, 0x9c}))
	got = normalizer.Append(got, string([]byte{0x93})+"\x1b[")
	got = normalizer.Append(got, "32mok\x1b[0m")
	if got != "✓\x1b[32mok\x1b[0m" {
		t.Fatalf("split output = %q", got)
	}
}

func TestNormalizerTreatsCRLFAsNewline(t *testing.T) {
	var normalizer Normalizer
	if got := normalizer.Append("", "first\r\nsecond\r\n"); got != "first\nsecond\n" {
		t.Fatalf("Append() = %q, want %q", got, "first\nsecond\n")
	}
}

func TestNormalizerTreatsSplitCRLFAsNewline(t *testing.T) {
	var normalizer Normalizer
	got := normalizer.Append("", "first\r")
	got = normalizer.Append(got, "\nsecond\r")
	got = normalizer.Append(got, "\n")
	if got != "first\nsecond\n" {
		t.Fatalf("split CRLF output = %q, want %q", got, "first\nsecond\n")
	}
}

func TestNormalizerFlushPreservesFinalBareCarriageReturnLine(t *testing.T) {
	var normalizer Normalizer
	got := normalizer.Append("", "downloaded 100%\r")
	if got = normalizer.Flush(got); got != "downloaded 100%" {
		t.Fatalf("Flush() = %q, want %q", got, "downloaded 100%")
	}
}

func TestNormalizerFlushDoesNotDuplicateSplitCRLF(t *testing.T) {
	var normalizer Normalizer
	got := normalizer.Append("", "first\r")
	got = normalizer.Append(got, "\nsecond\r\n")
	if got = normalizer.Flush(got); got != "first\nsecond\n" {
		t.Fatalf("Flush() = %q, want %q", got, "first\nsecond\n")
	}
}

func TestNormalizerDropsOSCAndUnsafeControls(t *testing.T) {
	var normalizer Normalizer
	got := normalizer.Append("", "a\x00b\x1b]0;title\x07c\tline\n")
	if got != "abc\tline\n" {
		t.Fatalf("Append() = %q, want %q", got, "abc\tline\n")
	}
}
