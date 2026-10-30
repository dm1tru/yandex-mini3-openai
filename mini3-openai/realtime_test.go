package main

import (
	"encoding/binary"
	"testing"
)

func TestPCM16to24ResampleLengthAndConstantSignal(t *testing.T) {
	for _, samples := range []int{1, 2, 3, 4, 1600} {
		input := make([]byte, samples*2)
		for i := 0; i < samples; i++ {
			binary.LittleEndian.PutUint16(input[i*2:], uint16(int16(1234)))
		}
		output, err := pcm16to24(input)
		if err != nil {
			t.Fatalf("%d input samples: %v", samples, err)
		}
		wantSamples := (samples*3 + 1) / 2
		if len(output) != wantSamples*2 {
			t.Fatalf("%d input samples: got %d output samples, want %d", samples, len(output)/2, wantSamples)
		}
		for i := 0; i < wantSamples; i++ {
			if got := int16(binary.LittleEndian.Uint16(output[i*2:])); got != 1234 {
				t.Fatalf("%d input samples: output[%d]=%d, want 1234", samples, i, got)
			}
		}
	}
}

func TestPCM16to24RejectsOddByteCount(t *testing.T) {
	if _, err := pcm16to24([]byte{1}); err == nil {
		t.Fatal("expected odd-length PCM to fail")
	}
}
