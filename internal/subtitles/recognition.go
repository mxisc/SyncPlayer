package subtitles

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"
)

type recognitionEvent struct {
	Type          string
	Message       string
	Code          string
	StartMS       int64
	EndMS         int64
	Cues          []Cue
	InputSeconds  float64
	SpeechSeconds float64
	RMS           float64
	LowSpeech     bool
}

func checkNativeRuntime(root string) error {
	for _, p := range []string{"runtime/ffmpeg/ffmpeg.exe", "runtime/whisper/whisper-cli.exe", "runtime/models/ggml-small.bin"} {
		if _, err := os.Stat(filepath.Join(root, p)); err != nil {
			return errors.New("本地识别组件缺失，请使用完整发布包")
		}
	}
	return nil
}
func nativeCommand(ctx context.Context, path string, args ...string) *exec.Cmd {
	cmd := exec.CommandContext(ctx, path, args...)
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
	return cmd
}
func waveDuration(data []byte) (int64, error) {
	if len(data) < 12 || string(data[:4]) != "RIFF" || string(data[8:12]) != "WAVE" {
		return 0, errors.New("invalid wav")
	}
	rate := uint32(0)
	for i := 12; i+8 <= len(data); {
		n := int(binary.LittleEndian.Uint32(data[i+4 : i+8]))
		kind := string(data[i : i+4])
		i += 8
		if kind == "fmt " && n >= 16 && i+16 <= len(data) {
			rate = binary.LittleEndian.Uint32(data[i+8 : i+12])
		}
		if kind == "data" && rate > 0 {
			available := len(data) - i
			if n > available {
				n = available
			}
			return int64(n) * 1000 / int64(rate), nil
		}
		if n < 0 || n > len(data)-i {
			break
		}
		i += n + (n & 1)
	}
	return 0, errors.New("missing wav samples")
}
func parseRecognition(data []byte, offset, end int64) ([]Cue, error) {
	var result struct {
		Transcription []struct {
			Text    string `json:"text"`
			Offsets struct {
				From int64 `json:"from"`
				To   int64 `json:"to"`
			} `json:"offsets"`
		} `json:"transcription"`
	}
	if err := json.Unmarshal(data, &result); err != nil {
		return nil, err
	}
	cues := []Cue{}
	for _, part := range result.Transcription {
		text := strings.TrimSpace(part.Text)
		from, to := offset+part.Offsets.From, offset+part.Offsets.To
		if to > end {
			to = end
		}
		if from < offset {
			from = offset
		}
		if text != "" && to > from {
			cues = append(cues, Cue{StartMS: from, EndMS: to, Original: text})
		}
	}
	return cues, nil
}
func recognizeChunk(parent context.Context, root, path, language string, start int64, lowSpeech bool) (recognitionEvent, error) {
	ctx, cancel := context.WithTimeout(parent, 5*time.Minute)
	defer cancel()
	dir, err := os.MkdirTemp("", "syncplayer-asr-")
	if err != nil {
		return recognitionEvent{}, err
	}
	defer os.RemoveAll(dir)
	wav := filepath.Join(dir, "audio.wav")
	output := filepath.Join(dir, "result")
	args := []string{"-hide_banner", "-loglevel", "error", "-nostdin", "-ss", fmt.Sprintf("%.3f", float64(start)/1000), "-i", path, "-t", "30", "-vn", "-ac", "1", "-ar", "16000", "-c:a", "pcm_s16le"}
	if lowSpeech {
		args = append(args, "-af", "dynaudnorm=f=500:g=31:p=0.95:m=8")
	}
	args = append(args, wav)
	if err = nativeCommand(ctx, filepath.Join(root, "runtime/ffmpeg/ffmpeg.exe"), args...).Run(); err != nil {
		return recognitionEvent{}, err
	}
	data, err := os.ReadFile(wav)
	if err != nil {
		return recognitionEvent{}, err
	}
	duration, err := waveDuration(data)
	if err != nil {
		return recognitionEvent{}, err
	}
	if duration < 100 {
		return recognitionEvent{Type: "done"}, nil
	}
	if language == "" {
		language = "auto"
	}
	args = []string{"-m", filepath.Join(root, "runtime/models/ggml-small.bin"), "-f", wav, "-l", language, "-t", "4", "-ng", "-oj", "-of", output, "-np", "-bs", "3"}
	if lowSpeech {
		args = append(args, "-nth", "0.8")
	}
	if err = nativeCommand(ctx, filepath.Join(root, "runtime/whisper/whisper-cli.exe"), args...).Run(); err != nil {
		return recognitionEvent{}, err
	}
	data, err = os.ReadFile(output + ".json")
	if err != nil {
		return recognitionEvent{}, err
	}
	cues, err := parseRecognition(data, start, start+duration)
	if err != nil {
		return recognitionEvent{}, err
	}
	return recognitionEvent{Type: "chunk", StartMS: start, EndMS: start + duration, Cues: cues, InputSeconds: float64(duration) / 1000, LowSpeech: lowSpeech}, nil
}
