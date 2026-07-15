package catalog

import (
	"math"
	"strings"
)

// Spark renders a single-row inline braille sparkline — an exact,
// dependency-free port of dtctl's output.MiniGraph (pkg/output), kept here
// so the catalog stays stdlib-only (TestSparkMatchesOutputMiniGraph pins the
// equivalence). Each braille cell is 2 columns × 4 dot rows; values are
// resampled to the pixel width and adjacent points join with vertical runs.
func Spark(values []float64, width int) string {
	if len(values) == 0 {
		return ""
	}

	minVal, maxVal := math.MaxFloat64, -math.MaxFloat64
	for _, v := range values {
		if !math.IsNaN(v) {
			if v < minVal {
				minVal = v
			}
			if v > maxVal {
				maxVal = v
			}
		}
	}
	if minVal == math.MaxFloat64 {
		return ""
	}

	const pixelHeight = 4 // one braille row
	pixelWidth := width * 2
	resampled := sparkResample(values, pixelWidth)
	valRange := maxVal - minVal
	if valRange == 0 {
		valRange = 1
	}

	// pixels[y][x], y = 0 at the top (inverted axis, like the original).
	pixels := make([][]bool, pixelHeight)
	for i := range pixels {
		pixels[i] = make([]bool, pixelWidth)
	}
	set := func(x, y int) {
		if y >= 0 && y < pixelHeight && x >= 0 && x < pixelWidth {
			pixels[y][x] = true
		}
	}
	var prevY int
	for x, v := range resampled {
		normalized := (v - minVal) / valRange
		y := pixelHeight - 1 - int(normalized*float64(pixelHeight-1))
		if y < 0 {
			y = 0
		}
		if y >= pixelHeight {
			y = pixelHeight - 1
		}
		set(x, y)
		if x > 0 {
			lo, hi := prevY, y
			if lo > hi {
				lo, hi = hi, lo
			}
			for py := lo; py <= hi; py++ {
				set(x, py)
			}
		}
		prevY = y
	}

	// Encode 2×4 cells as braille. Dot bit offsets by (dy + dx*4):
	// left column dots 1,2,3,7 then right column dots 4,5,6,8.
	dotOffsets := []int{0x01, 0x02, 0x04, 0x40, 0x08, 0x10, 0x20, 0x80}
	var sb strings.Builder
	for col := 0; col < width; col++ {
		var pattern int
		for dy := 0; dy < 4; dy++ {
			for dx := 0; dx < 2; dx++ {
				x := col*2 + dx
				if x < pixelWidth && pixels[dy][x] {
					pattern |= dotOffsets[dy+dx*4]
				}
			}
		}
		sb.WriteRune('⠀' + rune(pattern))
	}
	return sb.String()
}

// sparkResample linearly resamples values to targetLen points (NaNs lean on
// their finite neighbor) — the exact resampling the original uses.
func sparkResample(values []float64, targetLen int) []float64 {
	if len(values) == 0 || targetLen <= 0 {
		return values
	}
	if len(values) == targetLen {
		return values
	}
	result := make([]float64, targetLen)
	ratio := float64(len(values)-1) / float64(targetLen-1)
	for i := 0; i < targetLen; i++ {
		srcIdx := float64(i) * ratio
		lowIdx := int(srcIdx)
		highIdx := lowIdx + 1
		if highIdx >= len(values) {
			result[i] = values[len(values)-1]
			continue
		}
		frac := srcIdx - float64(lowIdx)
		lowVal := values[lowIdx]
		highVal := values[highIdx]
		switch {
		case math.IsNaN(lowVal):
			result[i] = highVal
		case math.IsNaN(highVal):
			result[i] = lowVal
		default:
			result[i] = lowVal + frac*(highVal-lowVal)
		}
	}
	return result
}
