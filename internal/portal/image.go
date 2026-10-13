package portal

import (
	"bytes"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"sync"
)

// maxSlimDim 门户图库对外图片的最大边长（像素）：微信小程序真机 image 组件
// 对超大图（数 MB / 数千像素）常常不绘制（表现为空白占位），服务端先瘦身。
const maxSlimDim = 1600

// slimCache 瘦身结果缓存：文件 ID 不可变，直接按 ID 缓存，避免每次首页都重编码。
var slimCache = struct {
	sync.Mutex
	m map[string]slimResult
}{m: map[string]slimResult{}}

type slimResult struct {
	data []byte
	mime string
}

const slimCacheMax = 64

func cacheGet(id string) (slimResult, bool) {
	slimCache.Lock()
	defer slimCache.Unlock()
	r, ok := slimCache.m[id]
	return r, ok
}

func cachePut(id string, r slimResult) {
	slimCache.Lock()
	defer slimCache.Unlock()
	if len(slimCache.m) >= slimCacheMax {
		for k := range slimCache.m { // 满了随便清一个，图库场景足够
			delete(slimCache.m, k)
			break
		}
	}
	slimCache.m[id] = r
}

// slimImage 门户图片瘦身：只缩不放；照片输出 JPEG，带透明的输出 PNG；
// 本来就小且不需要缩的返回原样（ok=false 表示用原数据）。
// 解码失败（非图片/格式冷门）也原样透传，不拦。
func slimImage(data []byte, mime string) (out []byte, outMime string, ok bool) {
	img, _, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		return nil, "", false
	}
	b := img.Bounds()
	w, h := b.Dx(), b.Dy()
	if w <= 0 || h <= 0 {
		return nil, "", false
	}
	maxDim := w
	if h > maxDim {
		maxDim = h
	}
	scaled := false
	if maxDim > maxSlimDim {
		img = scaleBox(img, maxSlimDim)
		scaled = true
	}
	// 没缩且本来就不大：保持原文件（避免 JPEG 二次压缩掉画质）。
	if !scaled && len(data) <= 600*1024 && mime != "image/png" {
		return nil, "", false
	}
	opaque := true
	if o, isOpaque := img.(interface{ Opaque() bool }); isOpaque {
		opaque = o.Opaque()
	}
	var buf bytes.Buffer
	if opaque {
		if err := jpeg.Encode(&buf, img, &jpeg.Options{Quality: 82}); err != nil {
			return nil, "", false
		}
		outMime = "image/jpeg"
	} else {
		if err := png.Encode(&buf, img); err != nil {
			return nil, "", false
		}
		outMime = "image/png"
	}
	if buf.Len() >= len(data) && !scaled {
		return nil, "", false
	}
	return buf.Bytes(), outMime, true
}

// scaleBox 等比缩放到最长边不超过 maxDim（只缩不放），盒式均值采样。
func scaleBox(src image.Image, maxDim int) image.Image {
	b := src.Bounds()
	sw, sh := b.Dx(), b.Dy()
	nw, nh := sw, sh
	if sw >= sh {
		nw = maxDim
		nh = sh * maxDim / sw
	} else {
		nh = maxDim
		nw = sw * maxDim / sh
	}
	if nw < 1 {
		nw = 1
	}
	if nh < 1 {
		nh = 1
	}
	dst := image.NewRGBA(image.Rect(0, 0, nw, nh))
	for y := 0; y < nh; y++ {
		sy0 := y * sh / nh
		sy1 := (y + 1) * sh / nh
		if sy1 <= sy0 {
			sy1 = sy0 + 1
		}
		for x := 0; x < nw; x++ {
			sx0 := x * sw / nw
			sx1 := (x + 1) * sw / nw
			if sx1 <= sx0 {
				sx1 = sx0 + 1
			}
			var rr, gg, bb, aa, n uint32
			for yy := sy0; yy < sy1; yy++ {
				for xx := sx0; xx < sx1; xx++ {
					r, g, bl, a := src.At(b.Min.X+xx, b.Min.Y+yy).RGBA()
					rr += r >> 8
					gg += g >> 8
					bb += bl >> 8
					aa += a >> 8
					n++
				}
			}
			dst.SetRGBA(x, y, color.RGBA{uint8(rr / n), uint8(gg / n), uint8(bb / n), uint8(aa / n)})
		}
	}
	return dst
}
