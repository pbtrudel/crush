package model

import (
	"image"

	uv "github.com/charmbracelet/ultraviolet"
)

// matrixRunes is the classic digital-rain alphabet: half-width katakana
// and digits. Punctuation is left out so stray glyphs never read as text.
var matrixRunes = []rune("ﾊﾐﾋｰｳｼﾅﾓﾆｻﾜﾂｵﾘｱﾎﾃﾏｹﾒｴｶｷﾑﾕﾗｾﾈｽﾀﾇﾍ0123456789")

const (
	// matrixColumnStep controls how many columns apart the rain columns are.
	matrixColumnStep = 4
	// matrixTrailLen is the number of dim glyphs above each bright head.
	matrixTrailLen = 5
)

// drawMatrixRain paints a field of falling glyphs into area. It is a
// background layer: callers draw the real content on top.
func (m *UI) drawMatrixRain(scr uv.Screen, area uv.Rectangle) {
	if area.Dx() <= 0 || area.Dy() <= 0 {
		return
	}
	t := m.com.Styles
	height := area.Dy()
	cycle := height + matrixTrailLen

	for x := area.Min.X; x < area.Max.X; x += matrixColumnStep {
		col := x - area.Min.X
		// Deterministic per-column offset and speed so the field looks
		// random but never jitters between frames.
		offset := int(matrixHash(col) % uint32(cycle))
		speed := 1 + int(matrixHash(col+17)%3)
		head := (offset + (m.animFrame/2)*speed) % cycle

		trailTop := head - matrixTrailLen
		for i := range matrixTrailLen {
			y := trailTop + i
			if y < 0 || y >= height || !matrixCellEmpty(scr, x, area.Min.Y+y) {
				continue
			}
			glyph := t.Chrome.MatrixRain.Render(matrixGlyph(col, y, m.animFrame))
			uv.NewStyledString(glyph).Draw(scr, image.Rect(x, area.Min.Y+y, x+1, area.Min.Y+y+1))
		}
		if head >= 0 && head < height && matrixCellEmpty(scr, x, area.Min.Y+head) {
			glyph := t.Chrome.MatrixRainHead.Render(matrixGlyph(col, head, m.animFrame))
			uv.NewStyledString(glyph).Draw(scr, image.Rect(x, area.Min.Y+head, x+1, area.Min.Y+head+1))
		}
	}
}

// matrixCellEmpty reports whether a screen cell is a plain, unbacked
// space. The rain only paints those cells so text and panel backgrounds
// stay untouched.
func matrixCellEmpty(scr uv.Screen, x, y int) bool {
	cell := scr.CellAt(x, y)
	return cell != nil && cell.Content == " " && cell.Style.Bg == nil
}

// matrixHash is a small deterministic hash for column offsets.
func matrixHash(n int) uint32 {
	h := uint32(n)*2654435761 + 0x9e3779b9
	h ^= h >> 16
	h *= 0x85ebca6b
	h ^= h >> 13
	return h
}

// matrixGlyph picks a glyph for a cell. The frame term makes the trail
// shimmer as it falls.
func matrixGlyph(col, row, frame int) string {
	idx := int(matrixHash(col*131+row*17+frame/4)) % len(matrixRunes)
	return string(matrixRunes[idx])
}
