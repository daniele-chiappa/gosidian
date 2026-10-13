package pipeline

import (
	"bytes"
	"fmt"
	"image"
	"image/draw"
	"image/png"
	"math"
)

// The images a vision model reads cost tokens by their pixels (about one
// token per 32×32 for Qwen-VL): a 4K screenshot passes the 4096-token
// context Ollama gives a model by default, and a 36-megapixel one costs tens
// of thousands of tokens anywhere (IMP-179). llm.describe_media scales such
// an image down for the model only; the attachment stays as it is.

// maxDecodePixels caps the images scaled down: decoding one takes four bytes
// a pixel.
const maxDecodePixels = 80_000_000

// downscale returns data as a PNG of at most maxPixels pixels, its aspect
// kept, when it has more; else data itself, unchanged is true. The pixels
// of each target pixel's area are averaged: a box filter, which keeps text
// readable when an image shrinks by a large factor.
func downscale(data []byte, maxPixels int) (out []byte, unchanged bool, err error) {
	cfg, _, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil {
		return nil, false, fmt.Errorf("reading the image's size: %w", err)
	}
	w, h := cfg.Width, cfg.Height
	if maxPixels <= 0 || w*h <= maxPixels {
		return data, true, nil
	}
	if w*h > maxDecodePixels {
		return nil, false, fmt.Errorf("the image is %d×%d, more than %d pixels", w, h, maxDecodePixels)
	}
	img, _, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		return nil, false, err
	}
	src := image.NewRGBA(image.Rect(0, 0, w, h))
	draw.Draw(src, src.Bounds(), img, img.Bounds().Min, draw.Src)
	scale := math.Sqrt(float64(maxPixels) / float64(w*h))
	tw, th := max(int(float64(w)*scale), 1), max(int(float64(h)*scale), 1)
	dst := image.NewRGBA(image.Rect(0, 0, tw, th))
	for ty := range th {
		y0, y1 := ty*h/th, max((ty+1)*h/th, ty*h/th+1)
		for tx := range tw {
			x0, x1 := tx*w/tw, max((tx+1)*w/tw, tx*w/tw+1)
			var sum [4]int
			for y := y0; y < y1; y++ {
				row := src.Pix[y*src.Stride+x0*4 : y*src.Stride+x1*4]
				for i := 0; i < len(row); i += 4 {
					sum[0] += int(row[i])
					sum[1] += int(row[i+1])
					sum[2] += int(row[i+2])
					sum[3] += int(row[i+3])
				}
			}
			n := (y1 - y0) * (x1 - x0)
			o := ty*dst.Stride + tx*4
			for c := range 4 {
				dst.Pix[o+c] = uint8(sum[c] / n)
			}
		}
	}
	var b bytes.Buffer
	if err := (&png.Encoder{CompressionLevel: png.BestSpeed}).Encode(&b, dst); err != nil {
		return nil, false, err
	}
	return b.Bytes(), false, nil
}
