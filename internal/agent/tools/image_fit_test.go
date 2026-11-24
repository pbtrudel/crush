package tools

import (
	"bytes"
	"encoding/binary"
	"hash/crc32"
	"image"
	"image/jpeg"
	"image/png"
	"math/rand/v2"
	"testing"

	"github.com/stretchr/testify/require"
)

// noisyImage builds a deterministic image that PNG and JPEG cannot compress
// away, so the size cap really is under pressure.
func noisyImage(width, height int) image.Image {
	img := image.NewRGBA(image.Rect(0, 0, width, height))
	rng := rand.New(rand.NewPCG(1, 2))
	for i := range img.Pix {
		img.Pix[i] = uint8(rng.IntN(256))
	}
	return img
}

// smoothImage builds a gradient, the kind of pixel data a reader can still
// recognize after the shrink.
func smoothImage(width, height int) image.Image {
	img := image.NewRGBA(image.Rect(0, 0, width, height))
	for y := range height {
		for x := range width {
			i := img.PixOffset(x, y)
			img.Pix[i+0] = uint8(x * 255 / width)
			img.Pix[i+1] = uint8(y * 255 / height)
			img.Pix[i+2] = 128
			img.Pix[i+3] = 255
		}
	}
	return img
}

func TestFitImageShrinksLargePNG(t *testing.T) {
	t.Parallel()

	var buf bytes.Buffer
	require.NoError(t, png.Encode(&buf, noisyImage(4000, 3000)))
	require.Greater(t, buf.Len(), MaxViewSize, "the fixture must start over the cap")

	out, mimeType, err := fitImage(buf.Bytes(), MaxViewSize)
	require.NoError(t, err)
	require.LessOrEqual(t, len(out), MaxViewSize)
	require.Equal(t, "image/jpeg", mimeType)

	decoded, err := jpeg.Decode(bytes.NewReader(out))
	require.NoError(t, err, "the shrunk image must still decode")
	require.GreaterOrEqual(t, decoded.Bounds().Dx(), minImageWidth)
	require.Less(t, decoded.Bounds().Dx(), 4000, "the image had to be resized")
}

// A smooth page keeps its readable width: the loop stops at the first width
// that fits instead of shrinking to the floor.
func TestFitImageKeepsReadableWidth(t *testing.T) {
	t.Parallel()

	var buf bytes.Buffer
	require.NoError(t, png.Encode(&buf, smoothImage(4000, 3000)))

	out, _, err := fitImage(buf.Bytes(), MaxViewSize)
	require.NoError(t, err)
	require.LessOrEqual(t, len(out), MaxViewSize)

	decoded, err := jpeg.Decode(bytes.NewReader(out))
	require.NoError(t, err)
	require.GreaterOrEqual(t, decoded.Bounds().Dx(), 800)
}

func TestFitImageRejectsGarbage(t *testing.T) {
	t.Parallel()

	_, _, err := fitImage(bytes.Repeat([]byte{0xff}, MaxViewSize+1), MaxViewSize)
	require.Error(t, err)
}

// pngHeader declares an image without carrying any pixel data, so only the
// header can be read without decoding it.
func pngHeader(t *testing.T, width, height uint32) []byte {
	t.Helper()

	var buf bytes.Buffer
	buf.Write([]byte{0x89, 'P', 'N', 'G', '\r', '\n', 0x1a, '\n'})
	ihdr := make([]byte, 13)
	binary.BigEndian.PutUint32(ihdr[0:4], width)
	binary.BigEndian.PutUint32(ihdr[4:8], height)
	ihdr[8] = 8 // Bit depth
	ihdr[9] = 6 // Colour type: RGBA
	var chunk bytes.Buffer
	_ = binary.Write(&chunk, binary.BigEndian, uint32(len(ihdr)))
	chunk.WriteString("IHDR")
	chunk.Write(ihdr)
	_ = binary.Write(&chunk, binary.BigEndian, crc32.ChecksumIEEE(append([]byte("IHDR"), ihdr...)))
	buf.Write(chunk.Bytes())
	return buf.Bytes()
}

// A header that declares more pixels than any reader can hold must be refused
// from the header alone, before the decoder allocates the pixels.
func TestFitImageRefusesTooManyPixels(t *testing.T) {
	t.Parallel()

	_, _, err := fitImage(pngHeader(t, 100000, 100000), MaxViewSize)
	require.Error(t, err)
	require.Contains(t, err.Error(), "100000x100000 pixels is too large to decode")
	require.NotContains(t, err.Error(), "could not decode the image")
}

// A screenshot with transparent areas must not turn black when it is encoded
// as JPEG: the transparent pixel becomes the white background.
func TestFitImageFlattensTransparencyOntoWhite(t *testing.T) {
	t.Parallel()

	img := image.NewRGBA(image.Rect(0, 0, 3000, 2000))
	for i := range img.Pix {
		if i%4 == 3 {
			img.Pix[i] = 0 // Fully transparent.
		}
	}
	var buf bytes.Buffer
	require.NoError(t, png.Encode(&buf, img))

	out, _, err := fitImage(buf.Bytes(), MaxViewSize)
	require.NoError(t, err)

	decoded, err := jpeg.Decode(bytes.NewReader(out))
	require.NoError(t, err)
	r, g, b, _ := decoded.At(decoded.Bounds().Dx()/2, decoded.Bounds().Dy()/2).RGBA()
	require.GreaterOrEqual(t, r>>8, uint32(240), "red channel must be white, not black")
	require.GreaterOrEqual(t, g>>8, uint32(240), "green channel must be white, not black")
	require.GreaterOrEqual(t, b>>8, uint32(240), "blue channel must be white, not black")
}
