// Package qrutil 提供二维码图片的终端渲染与格式归一化。
//
// 独立成包的原因：CLI 与本地面板都需要它，若放在 cli 包里会导致
// webui → cli 的反向依赖。
package qrutil

import (
	"bytes"
	"fmt"
	"image"
	"image/png"
	"io"

	"github.com/mdp/qrterminal/v3"

	// 注册 JPEG 解码器：微信二维码接口实测返回 JPEG 而非 PNG。
	_ "image/jpeg"
)

// PrintQRToTerminal 把二维码图片（PNG 或 JPEG）渲染成终端字符画。
//
// 微信 /connect/qrcode/{uid} 实际返回 JPEG，因此这里用 image.Decode
// 按文件头自动识别格式，而不是假定 PNG。
func PrintQRToTerminal(w io.Writer, imgData []byte) error {
	img, format, err := image.Decode(bytes.NewReader(imgData))
	if err != nil {
		return fmt.Errorf("解码二维码图片失败（格式 %q）: %w", format, err)
	}
	cfg := qrterminal.Config{
		Level:      qrterminal.L,
		Writer:     w,
		HalfBlocks: true,
		QuietZone:  1,
	}
	return renderImage(img, cfg)
}

// ToPNG 把任意支持格式的二维码图片统一转成 PNG。
//
// 用途：面板用 data URL 展示、CLI 保存文件时都希望格式确定，
// 避免上层对"可能是 JPEG"这件事有隐式依赖。
func ToPNG(imgData []byte) ([]byte, error) {
	img, _, err := image.Decode(bytes.NewReader(imgData))
	if err != nil {
		return nil, fmt.Errorf("解码二维码图片失败: %w", err)
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		return nil, fmt.Errorf("转码 PNG 失败: %w", err)
	}
	return buf.Bytes(), nil
}

// renderImage 按像素矩阵渲染二维码。
//
// 二维码是纯黑白位图，逐块采样比按 URL 重新生成更忠实于原图
// （避免纠错级别/版本与原图不一致导致扫不出来）。
func renderImage(img image.Image, cfg qrterminal.Config) error {
	// 裁掉四周白边，得到真正的二维码区域。
	x0, y0, x1, y1 := trimWhite(img)

	cropW := x1 - x0
	cropH := y1 - y0
	if cropW <= 0 || cropH <= 0 {
		return fmt.Errorf("二维码图片内容为空")
	}

	// 终端宽度适中：41 个模块宽，配合半块字符接近正方形。
	const target = 41
	stepX := float64(cropW) / float64(target)
	stepY := float64(cropH) / float64(target)

	for row := 0; row < target; row++ {
		for col := 0; col < target; col++ {
			// 取块中心像素判断黑白，避免边缘抗锯齿误判。
			px := x0 + int((float64(col)+0.5)*stepX)
			py := y0 + int((float64(row)+0.5)*stepY)
			r, g, b, _ := img.At(px, py).RGBA()
			lum := (r*299 + g*587 + b*114) / 1000
			if lum < 0x8000 {
				// 黑块用两个全块字符，保持纵横比。
				fmt.Fprint(cfg.Writer, "\u2588\u2588")
			} else {
				fmt.Fprint(cfg.Writer, "  ")
			}
		}
		fmt.Fprintln(cfg.Writer)
	}
	return nil
}

// trimWhite 裁掉四周纯白边，返回有效区域。
func trimWhite(img image.Image) (x0, y0, x1, y1 int) {
	b := img.Bounds()
	x0, y0, x1, y1 = b.Min.X, b.Min.Y, b.Max.X, b.Max.Y

	isWhite := func(x, y int) bool {
		r, g, bl, _ := img.At(x, y).RGBA()
		return r > 0xf000 && g > 0xf000 && bl > 0xf000
	}

	// 上边界
	for y := b.Min.Y; y < b.Max.Y; y++ {
		allWhite := true
		for x := b.Min.X; x < b.Max.X; x++ {
			if !isWhite(x, y) {
				allWhite = false
				break
			}
		}
		if !allWhite {
			y0 = y
			break
		}
	}
	// 下边界
	for y := b.Max.Y - 1; y >= y0; y-- {
		allWhite := true
		for x := b.Min.X; x < b.Max.X; x++ {
			if !isWhite(x, y) {
				allWhite = false
				break
			}
		}
		if !allWhite {
			y1 = y + 1
			break
		}
	}
	// 左边界
	for x := b.Min.X; x < b.Max.X; x++ {
		allWhite := true
		for y := y0; y < y1; y++ {
			if !isWhite(x, y) {
				allWhite = false
				break
			}
		}
		if !allWhite {
			x0 = x
			break
		}
	}
	// 右边界
	for x := b.Max.X - 1; x >= x0; x-- {
		allWhite := true
		for y := y0; y < y1; y++ {
			if !isWhite(x, y) {
				allWhite = false
				break
			}
		}
		if !allWhite {
			x1 = x + 1
			break
		}
	}
	return x0, y0, x1, y1
}
