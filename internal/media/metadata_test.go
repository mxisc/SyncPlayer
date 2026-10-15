package media

import "testing"

func TestProbeMetadata(t *testing.T) {
	for _, tc := range []struct {
		json     string
		duration float64
		codec    string
		bad      bool
	}{
		{`{"streams":[{"codec_name":"hevc","height":2160}],"format":{"duration":"42.5"}}`, 42.5, "h265", false},
		{`{"streams":[{"codec_name":"h264","height":1080,"duration":"2.75"}],"format":{"duration":"N/A"}}`, 2.75, "h264", false},
		{`{"streams":[{"codec_name":"h264","height":1080}],"format":{"duration":"NaN"}}`, 0, "", true},
		{`{"streams":[]}`, 0, "", true},
	} {
		m, err := parseMetadata([]byte(tc.json))
		if (err != nil) != tc.bad {
			t.Fatalf("unexpected error: %v", err)
		}
		if !tc.bad && (m.Duration != tc.duration || m.Codec != tc.codec) {
			t.Fatalf("unexpected metadata: %+v", m)
		}
	}
}
