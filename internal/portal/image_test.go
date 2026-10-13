package portal

import (
	"bytes"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"testing"
)

func bigPNG(w, h int) []byte {
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			img.Set(x, y, color.RGBA{uint8(x % 255), uint8(y % 255), 128, 255})
		}
	}
	var buf bytes.Buffer
	_ = png.Encode(&buf, img)
	return buf.Bytes()
}

// TestSlimImageResize 超大图缩到 1600 并以 JPEG 输出（真机渲染问题根修）。
func TestSlimImageResize(t *testing.T) {
	src := bigPNG(2560, 1440)
	out, mime, ok := slimImage(src, "image/png")
	if !ok || mime != "image/jpeg" {
		t.Fatalf("slim = %v %s", ok, mime)
	}
	img, err := jpeg.Decode(bytes.NewReader(out))
	if err != nil {
		t.Fatal(err)
	}
	if img.Bounds().Dx() != 1600 {
		t.Fatalf("width = %d, want 1600", img.Bounds().Dx())
	}
}

// TestSlimImageSmallKept 小 JPEG 原样透传（不二次压缩）。
func TestSlimImageSmallKept(t *testing.T) {
	img := image.NewRGBA(image.Rect(0, 0, 320, 200))
	var buf bytes.Buffer
	_ = jpeg.Encode(&buf, img, nil)
	src := buf.Bytes()
	if _, _, ok := slimImage(src, "image/jpeg"); ok {
		t.Fatal("small jpeg should stay as-is")
	}
}

// TestSlimImageAlphaKeptPNG 带透明的大 PNG 缩后仍输出 PNG（保透明）。
func TestSlimImageAlphaKeptPNG(t *testing.T) {
	img := image.NewNRGBA(image.Rect(0, 0, 2000, 500))
	for x := 0; x < 2000; x++ {
		img.SetNRGBA(x, 0, color.NRGBA{255, 0, 0, 0})
	}
	var buf bytes.Buffer
	_ = png.Encode(&buf, img)
	out, mime, ok := slimImage(buf.Bytes(), "image/png")
	if !ok || mime != "image/png" {
		t.Fatalf("alpha png = %v %s", ok, mime)
	}
	dec, err := png.Decode(bytes.NewReader(out))
	if err != nil {
		t.Fatal(err)
	}
	if dec.Bounds().Dx() != 1600 {
		t.Fatalf("width = %d, want 1600", dec.Bounds().Dx())
	}
}

// TestSlimImageNotImage 非图片原样透传不拦。
func TestSlimImageNotImage(t *testing.T) {
	if _, _, ok := slimImage([]byte("not an image"), "application/pdf"); ok {
		t.Fatal("non-image should pass through")
	}
}
