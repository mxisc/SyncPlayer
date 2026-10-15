package subtitles

import (
	"encoding/binary"
	"testing"
)

func TestNativeRecognitionTiming(t *testing.T) {
	cues, err := parseRecognition([]byte(`{"transcription":[{"text":" 日本語 ","offsets":{"from":250,"to":1200}},{"text":"tail","offsets":{"from":1800,"to":4000}}]}`), 85000, 88000)
	if err != nil || len(cues) != 2 || cues[0].StartMS != 85250 || cues[0].EndMS != 86200 || cues[1].EndMS != 88000 {
		t.Fatalf("timing: %+v %v", cues, err)
	}
}
func TestWaveDuration(t *testing.T) {
	data := make([]byte, 44+32000)
	copy(data, "RIFF")
	copy(data[8:], "WAVEfmt ")
	binary.LittleEndian.PutUint32(data[16:], 16)
	binary.LittleEndian.PutUint32(data[28:], 32000)
	copy(data[36:], "data")
	binary.LittleEndian.PutUint32(data[40:], 32000)
	ms, err := waveDuration(data)
	if err != nil || ms != 1000 {
		t.Fatalf("duration %d %v", ms, err)
	}
	if _, err = waveDuration([]byte("invalid")); err == nil {
		t.Fatal("invalid WAV accepted")
	}
}
