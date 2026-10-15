package media

import (
	"bytes"
	"image"
	"image/color"
	"image/png"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

func testCoverPNG(t *testing.T, path string, width int) {
	t.Helper()
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	i := image.NewRGBA(image.Rect(0, 0, width, 1))
	i.Set(0, 0, color.White)
	if err = png.Encode(f, i); err != nil {
		f.Close()
		t.Fatal(err)
	}
	if err = f.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestFixedCoverPrecedesExtractionAndSharedFolderCover(t *testing.T) {
	dir := t.TempDir()
	video := filepath.Join(dir, "sample.mp4")
	// No video exists: fixed covers must not require decoding or probing it.
	shared := filepath.Join(dir, "cover.png")
	testCoverPNG(t, shared, 2)
	s, _ := New()
	defer s.Close()
	rr := httptest.NewRecorder()
	s.serveThumbnail(rr, httptest.NewRequest("GET", "/thumbnail/a.png", nil), video)
	c, err := png.DecodeConfig(bytes.NewReader(rr.Body.Bytes()))
	if err != nil || rr.Code != 200 || c.Width != 2 {
		t.Fatal("shared fixed cover not served")
	}
	specific := filepath.Join(dir, "sample.png")
	testCoverPNG(t, specific, 3)
	rr = httptest.NewRecorder()
	s.serveThumbnail(rr, httptest.NewRequest("GET", "/thumbnail/a.png", nil), video)
	c, err = png.DecodeConfig(bytes.NewReader(rr.Body.Bytes()))
	if err != nil || c.Width != 3 {
		t.Fatal("video-specific cover must take priority")
	}
	rr = httptest.NewRecorder()
	s.serveThumbnail(rr, httptest.NewRequest("HEAD", "/thumbnail/a.png", nil), video)
	if rr.Code != 200 || rr.Body.Len() != 0 {
		t.Fatal("HEAD returned image body")
	}
}

func TestSavedImageSurvivesServerRestart(t *testing.T) {
	dir := t.TempDir()
	video := filepath.Join(dir, "sample.mp4")
	cover := filepath.Join(dir, "sample.png")
	testCoverPNG(t, cover, 4)
	for n := 0; n < 2; n++ {
		s, _ := New()
		rr := httptest.NewRecorder()
		s.serveThumbnail(rr, httptest.NewRequest("GET", "/thumbnail/a.png", nil), video)
		s.Close()
		c, err := png.DecodeConfig(bytes.NewReader(rr.Body.Bytes()))
		if err != nil || rr.Code != 200 || c.Width != 4 {
			t.Fatal("saved image not reused")
		}
	}
	original, err := os.ReadFile(cover)
	if err != nil {
		t.Fatal(err)
	}
	if err = saveCoverImage(cover, []byte("replacement")); err != nil {
		t.Fatal(err)
	}
	unchanged, err := os.ReadFile(cover)
	if err != nil || !bytes.Equal(original, unchanged) {
		t.Fatal("existing image overwritten")
	}
	next := filepath.Join(dir, "other.png")
	if err = saveCoverImage(next, original); err != nil {
		t.Fatal(err)
	}
	if !validCachedThumbnail(next) {
		t.Fatal("image not saved beside video")
	}
}
