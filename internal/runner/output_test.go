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
		{"retain colon truecolor", "", "\x1b[38:2::18:171:239mcolor\x1b[0m", "\x1b[38:2::18:171:239mcolor\x1b[0m"},
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

func TestNormalizerHomebrewProgressRedraws(t *testing.T) {
	tests := []struct {
		name, input, want string
	}{
		{
			name: "single download uses cursor horizontal absolute",
			input: "Starting\r\n⠲ Cask docker-desktop Extracting 572.7MB\x1b[K\x1b[0G" +
				"⠳ Cask docker-desktop Extracting 572.7MB\x1b[K\x1b[0G" +
				"✔ Cask docker-desktop Extracted\x1b[K\r\nDone\r\n",
			want: "Starting\n✔ Cask docker-desktop Extracted\nDone\n",
		},
		{
			name: "multiple downloads use cursor previous line",
			input: "Starting\r\n⠲ Cask docker-desktop 10%\x1b[K\r\n⠲ Bottle other 20%\x1b[K\x1b[1F" +
				"⠳ Cask docker-desktop 80%\x1b[K\r\n⠳ Bottle other 90%\x1b[K\x1b[1F" +
				"✔ Cask docker-desktop done\x1b[K\r\n✔ Bottle other done\x1b[K\r\n",
			want: "Starting\n✔ Cask docker-desktop done\n✔ Bottle other done\n",
		},
		{
			name:  "cursor up and carriage return",
			input: "Header\nold progress\n\x1b[A\rnew\x1b[K\nDone\n",
			want:  "Header\nnew\nDone\n",
		},
		{
			name:  "screen erase removes stale progress rows",
			input: "Header\nfirst\nsecond\x1b[1F\x1b[Jlast\n",
			want:  "Header\nlast\n",
		},
		{
			name:  "final cursor reset preserves progress and color",
			input: "\x1b[32m⠲ Download 10%\x1b[0m\x1b[1G\x1b[32m✔ Done\x1b[0m\x1b[K\x1b[0G",
			want:  "\x1b[32m✔ Done\x1b[0m",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// A PTY read can split a redraw anywhere, including inside a rune or CSI.
			for split := 0; split <= len(tt.input); split++ {
				var normalizer Normalizer
				got := normalizer.Append("", tt.input[:split])
				got = normalizer.Append(got, tt.input[split:])
				if got = normalizer.Flush(got); got != tt.want {
					t.Fatalf("split %d: got %q, want %q", split, got, tt.want)
				}
			}
			var normalizer Normalizer
			var got string
			for i := range len(tt.input) {
				got = normalizer.Append(got, tt.input[i:i+1])
			}
			if got = normalizer.Flush(got); got != tt.want {
				t.Fatalf("byte-at-a-time: got %q, want %q", got, tt.want)
			}
		})
	}
}
