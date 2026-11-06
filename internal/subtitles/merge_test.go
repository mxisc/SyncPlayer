package subtitles

import "testing"

func TestMergeSubtitleChunkBoundary(t *testing.T) {
	old := Cue{StartMS: 1624320, EndMS: 1624960, Original: "previous", Translation: "上一句"}
	next := Cue{StartMS: 1624000, EndMS: 1632000, Original: "new line", Translation: "新一句"}
	got := mergeSubtitleCues([]Cue{old}, []Cue{next}, 1624000)
	if len(got) != 2 || got[1] != next {
		t.Fatalf("overlap discarded a distinct subtitle: %+v", got)
	}
	repeated := Cue{StartMS: 1631000, EndMS: 1633000, Original: "new  line", Translation: "另一个译法"}
	got = mergeSubtitleCues(got, []Cue{repeated}, 1631000)
	if len(got) != 2 || got[1].StartMS != next.StartMS || got[1].EndMS != repeated.EndMS || got[1].Translation != next.Translation {
		t.Fatalf("duplicate subtitle did not preserve coverage and translation: %+v", got)
	}
}

func TestMergeSubtitleBoundaryVariants(t *testing.T) {
	old := Cue{StartMS: 1694853, EndMS: 1695733, Original: "Keep pushing.", Translation: "继续用力。"}
	next := Cue{StartMS: 1694733, EndMS: 1696173, Original: "Oh, keep pushing!", Translation: "继续挺住"}
	got := mergeSubtitleCues([]Cue{old}, []Cue{next}, 1694733)
	if len(got) != 1 || got[0] != next {
		t.Fatalf("expected one complete boundary subtitle: %+v", got)
	}
	got = mergeSubtitleCues(nil, []Cue{old, next}, 1694733)
	if len(got) != 2 {
		t.Fatalf("fuzzy deduplication must not apply within a chunk: %+v", got)
	}
}

func TestMergeClippedSubtitleFragment(t *testing.T) {
	old := Cue{StartMS: 1698307, EndMS: 1699867, Original: "getting closer now", Translation: "越来越近。"}
	next := Cue{StartMS: 1698867, EndMS: 1703827, Original: "You are getting closer now, one step at a time.", Translation: "你正一步一步靠近。"}
	got := mergeSubtitleCues([]Cue{old}, []Cue{next}, 1698867)
	if len(got) != 1 || got[0].StartMS != old.StartMS || got[0].EndMS != next.EndMS || got[0].Translation != next.Translation {
		t.Fatalf("expected the full line without its repeated fragment: %+v", got)
	}
	old.EndMS -= 200
	if got := mergeSubtitleCues([]Cue{old}, []Cue{next}, 1698867); len(got) != 2 {
		t.Fatalf("do not discard an overlapping line away from the chunk edge: %+v", got)
	}
}
