package pipeline

import (
	"bytes"
	"image"
	"image/color"
	"strings"
	"testing"
)

func TestScanImages_InlineAndCode(t *testing.T) {
	lines := strings.Split("![a](x.png)\nText ![b](y.png) more\n`![c](z.png)` and ![d](w.png)\n```\n![e](v.png)\n```\n![f](u.png \"title\") ![g](t.png)", "\n")
	got := scanImages(lines)
	want := []imageRef{
		{URL: "x.png", Alt: "a", Line: 0},
		{URL: "y.png", Alt: "b", Line: 1, Inline: true},
		{URL: "w.png", Alt: "d", Line: 2, Inline: true},
		{URL: "u.png", Alt: "f", Line: 6},
		{URL: "t.png", Alt: "g", Line: 6},
	}
	if len(got) != len(want) {
		t.Fatalf("got %+v", got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("%d: %+v, want %+v", i, got[i], want[i])
		}
	}
}

func TestSectionAt(t *testing.T) {
	lines := strings.Split("# Title\n## **Setup**\n### Window\ntext\n## Other\n#### Deep\n```\n## not a heading\n```\nx", "\n")
	for line, want := range map[int]string{0: "", 3: "Setup > Window", 5: "Other", 9: "Other > Deep"} {
		if got := sectionAt(lines, line); got != want {
			t.Errorf("line %d: %q, want %q", line, got, want)
		}
	}
}

func TestImageContext_ClipsAroundTheImage(t *testing.T) {
	long := strings.Repeat("parola ", 100)
	lines := []string{"## Sezione", "Prima riga. " + long + "Ultima prima.", "", "![x](a.png)", "", "Dopo è così. " + long, "## Altra", "fuori"}
	got := imageContext(lines, imageRef{URL: "a.png", Line: 3}, 300)
	if !strings.HasPrefix(got, "…") || !strings.Contains(got, "Ultima prima.\n\n[image]\n\nDopo è così.") || !strings.HasSuffix(got, "…") {
		t.Fatalf("context %q", got)
	}
	if strings.Contains(got, "fuori") || strings.Contains(got, "Sezione") || len(got) > 320 {
		t.Errorf("context %d bytes: %q", len(got), got)
	}
}

func TestLinkTargetAndWithBody(t *testing.T) {
	for in, want := range map[string]string{"[[a/b]]": "a/b", "[[a/b.md|x]]": "a/b", "[[a/b#h]]": "a/b", "a/b": ""} {
		if got := linkTarget(in); got != want {
			t.Errorf("%q: %q", in, got)
		}
	}
	if got := string(withBody([]byte("---\ntitle: x\n---\n\nold\n"), "new")); got != "---\ntitle: x\n---\n\nnew\n" {
		t.Errorf("withBody %q", got)
	}
	if got := string(withBody([]byte("---\ntitle: x\n---\n\nold\n"), "")); got != "---\ntitle: x\n---\n" {
		t.Errorf("withBody empty %q", got)
	}
}

func TestDownscale(t *testing.T) {
	src := pngOf(t, 400, 300, color.Gray{Y: 200})
	out, same, err := downscale(src, 10_000)
	if err != nil || same {
		t.Fatalf("scaled: %v %v", same, err)
	}
	cfg, format, err := image.DecodeConfig(bytes.NewReader(out))
	if err != nil || format != "png" || cfg.Width*cfg.Height > 10_000 || cfg.Width < 110 || cfg.Height < 80 {
		t.Fatalf("scaled to %s %d×%d: %v", format, cfg.Width, cfg.Height, err)
	}
	img, _, _ := image.Decode(bytes.NewReader(out))
	if r, g, b, _ := img.At(10, 10).RGBA(); r>>8 != 200 || g>>8 != 200 || b>>8 != 200 {
		t.Errorf("the colour changed: %d %d %d", r>>8, g>>8, b>>8)
	}
	for _, max := range []int{0, 120_000} {
		if out, same, err := downscale(src, max); err != nil || !same || !bytes.Equal(out, src) {
			t.Errorf("max %d: changed (%v, %v)", max, same, err)
		}
	}
	if _, _, err := downscale([]byte("not an image"), 10); err == nil {
		t.Error("no error for bytes that are no image")
	}
}
