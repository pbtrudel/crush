package tools

import (
	"bytes"
	"fmt"
	"image"
	"image/draw"
	_ "image/gif"
	"image/jpeg"
	_ "image/png"

	"github.com/disintegration/imaging"
	_ "golang.org/x/image/webp"
)

const (
	// maxImageWidth is the widest the first attempt is: past this a
	// screenshot loses detail without getting easier to read.
	maxImageWidth = 2000
	// minImageWidth is the narrowest a shrunk image may become. Below it the
	// text in a screenshot is gone, and an error is the honest answer.
	minImageWidth = 400
	// maxImageFileSize is the hard cap on an image file. Past it the file is
	// refused by size alone, before a byte of it is read.
	maxImageFileSize = 25 << 20
	// maxImagePixels is the most pixels a decoder may be asked for. A header
	// can declare a picture far larger than memory, and the check runs on the
	// header, so the allocation never happens.
	maxImagePixels = 50_000_000
)

// fitImage shrinks an image until its encoded form fits maxBytes and returns
// it as JPEG. Only a body that cannot be decoded is a failure: a picture the
// model cannot read is worth an error, a picture that is merely large is not.
func fitImage(data []byte, maxBytes int) ([]byte, string, error) {
	config, _, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil {
		return nil, "", fmt.Errorf("could not decode the image: %w", err)
	}
	if pixels := int64(config.Width) * int64(config.Height); pixels > maxImagePixels {
		return nil, "", fmt.Errorf("%dx%d pixels is too large to decode", config.Width, config.Height)
	}

	img, _, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		return nil, "", fmt.Errorf("could not decode the image: %w", err)
	}

	bounds := img.Bounds()
	width := min(bounds.Dx(), maxImageWidth)
	for width >= minImageWidth {
		shrunk := img
		if width < bounds.Dx() {
			shrunk = imaging.Resize(img, width, 0, imaging.Lanczos)
		}
		// JPEG has no alpha, and a transparent area would encode as black.
		shrunk = flattenOnWhite(shrunk)
		for _, quality := range []int{85, 70} {
			var buf bytes.Buffer
			if err := jpeg.Encode(&buf, shrunk, &jpeg.Options{Quality: quality}); err != nil {
				return nil, "", fmt.Errorf("could not encode the image: %w", err)
			}
			if buf.Len() <= maxBytes {
				return buf.Bytes(), "image/jpeg", nil
			}
		}
		width = width * 3 / 4
	}
	return nil, "", fmt.Errorf("could not fit the image under %d bytes without dropping below %d pixels wide", maxBytes, minImageWidth)
}

// flattenOnWhite composites img over a white background, so a screenshot with
// transparent areas does not turn black when it is encoded as JPEG.
func flattenOnWhite(img image.Image) image.Image {
	bounds := img.Bounds()
	dst := image.NewRGBA(image.Rect(0, 0, bounds.Dx(), bounds.Dy()))
	draw.Draw(dst, dst.Bounds(), image.White, image.Point{}, draw.Src)
	draw.Draw(dst, dst.Bounds(), img, bounds.Min, draw.Over)
	return dst
}
