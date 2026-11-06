package subtitles

import (
	"strings"
	"unicode"
	"unicode/utf8"
)

// Merge matching text and repeated fragments at adjacent chunk boundaries.
func mergeSubtitleCues(existing, incoming []Cue, chunkStart int64) []Cue {
	previous := len(existing)
	for _, cue := range incoming {
		text := strings.Join(strings.Fields(cue.Original), " ")
		for i := 0; i < len(existing); {
			old := existing[i]
			overlap := min(cue.EndMS, old.EndMS) - max(cue.StartMS, old.StartMS)
			if overlap <= 0 {
				i++
				continue
			}
			exact := text != "" && text == strings.Join(strings.Fields(old.Original), " ")
			boundary := i < previous && cue.StartMS >= chunkStart && cue.StartMS < chunkStart+1000 && old.EndMS <= chunkStart+1000
			near := boundary && overlap*5 >= min(cue.EndMS-cue.StartMS, old.EndMS-old.StartMS)*4 &&
				boundaryTextMatch(cue.Original, old.Original)
			// A cue cut off at the prior chunk's end can be a short fragment
			// of the longer cue recognized in the next chunk.
			fragment := boundary && old.EndMS >= chunkStart+900 && cue.StartMS <= chunkStart+100 &&
				cue.EndMS > old.EndMS && overlap*2 >= old.EndMS-old.StartMS &&
				boundaryFragmentMatch(old.Original, cue.Original)
			if !exact && !near && !fragment {
				i++
				continue
			}
			cue.StartMS = min(cue.StartMS, old.StartMS)
			cue.EndMS = max(cue.EndMS, old.EndMS)
			if exact || len(boundaryText(old.Original)) > len(boundaryText(cue.Original)) {
				cue.Original, cue.Translation = old.Original, old.Translation
			}
			if i < previous {
				previous--
			}
			existing = append(existing[:i], existing[i+1:]...)
			i = 0
		}
		existing = append(existing, cue)
	}
	return existing
}

func boundaryFragmentMatch(fragment, full string) bool {
	fragment, full = boundaryText(fragment), boundaryText(full)
	return utf8.RuneCountInString(fragment) >= 12 && len(full) > len(fragment) && strings.Contains(full, fragment)
}

func boundaryText(text string) string {
	return strings.Map(func(r rune) rune {
		if unicode.IsLetter(r) || unicode.IsNumber(r) {
			return unicode.ToLower(r)
		}
		return -1
	}, text)
}

func boundaryTextMatch(a, b string) bool {
	a, b = boundaryText(a), boundaryText(b)
	an, bn := utf8.RuneCountInString(a), utf8.RuneCountInString(b)
	short, long := min(an, bn), max(an, bn)
	return short >= 8 && short*5 >= long*4 && (strings.Contains(a, b) || strings.Contains(b, a))
}
