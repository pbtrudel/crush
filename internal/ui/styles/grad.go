package styles

import (
	"fmt"
	"image/color"
	"math"
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/lucasb-eyer/go-colorful"
	"github.com/rivo/uniseg"
)

// ForegroundGrad returns a slice of strings representing the input string
// rendered with a horizontal gradient foreground from color1 to color2. Each
// string in the returned slice corresponds to a grapheme cluster in the input
// string. If bold is true, the rendered strings will be bolded.
func ForegroundGrad(base lipgloss.Style, input string, bold bool, color1, color2 color.Color) []string {
	if input == "" {
		return []string{""}
	}
	if len(input) == 1 {
		style := base.Foreground(color1)
		if bold {
			style.Bold(true)
		}
		return []string{style.Render(input)}
	}
	var clusters []string
	gr := uniseg.NewGraphemes(input)
	for gr.Next() {
		clusters = append(clusters, string(gr.Runes()))
	}

	ramp := lipgloss.Blend1D(len(clusters), color1, color2)
	for i, c := range ramp {
		style := base.Foreground(c)
		if bold {
			style.Bold(true)
		}
		clusters[i] = style.Render(clusters[i])
	}
	return clusters
}

// ApplyForegroundGrad renders a given string with a horizontal gradient
// foreground.
func ApplyForegroundGrad(base lipgloss.Style, input string, color1, color2 color.Color) string {
	if input == "" {
		return ""
	}
	var o strings.Builder
	clusters := ForegroundGrad(base, input, false, color1, color2)
	for _, c := range clusters {
		fmt.Fprint(&o, c)
	}
	return o.String()
}

// ApplyBoldForegroundGrad renders a given string with a horizontal gradient
// foreground.
func ApplyBoldForegroundGrad(base lipgloss.Style, input string, color1, color2 color.Color) string {
	if input == "" {
		return ""
	}
	var o strings.Builder
	clusters := ForegroundGrad(base, input, true, color1, color2)
	for _, c := range clusters {
		fmt.Fprint(&o, c)
	}
	return o.String()
}

// shimmerWidth is the half-width, in gradient units, of the moving
// highlight. A value of 0.18 lights roughly a fifth of the run at a time.
const shimmerWidth = 0.18

// ApplyShimmerGrad renders input with a horizontal gradient from color1 to
// color2 and a moving highlight centered at phase (0..1). The highlight
// wraps around the ends so a sweep loops seamlessly. phase is normally
// derived from an animation frame counter.
func ApplyShimmerGrad(base lipgloss.Style, input string, color1, color2, highlight color.Color, phase float64) string {
	if input == "" {
		return ""
	}
	var clusters []string
	gr := uniseg.NewGraphemes(input)
	for gr.Next() {
		clusters = append(clusters, string(gr.Runes()))
	}
	if len(clusters) == 1 {
		return base.Foreground(color1).Render(input)
	}

	ramp := lipgloss.Blend1D(len(clusters), color1, color2)
	phase = math.Mod(phase, 1)
	if phase < 0 {
		phase++
	}
	for i, c := range ramp {
		t := float64(i) / float64(len(clusters)-1)
		d := math.Abs(t - phase)
		if d > 0.5 {
			d = 1 - d
		}
		weight := math.Max(0, 1-d/shimmerWidth)
		weight *= weight
		clusters[i] = base.Foreground(blendColor(c, highlight, weight*0.9)).Render(clusters[i])
	}
	return strings.Join(clusters, "")
}

// VerticalGradient renders height rows of char with a top-to-bottom
// gradient. It is used for the sidebar rail.
func VerticalGradient(base lipgloss.Style, height int, char string, color1, color2 color.Color) string {
	if height <= 0 {
		return ""
	}
	ramp := lipgloss.Blend1D(height, color1, color2)
	lines := make([]string, height)
	for i := range height {
		lines[i] = base.Foreground(ramp[i]).Render(char)
	}
	return strings.Join(lines, "\n")
}

// PulseColor blends c toward highlight by amount (0..1). It is used to
// breathe status LEDs without allocating a new style per frame.
func PulseColor(c, highlight color.Color, amount float64) color.Color {
	return blendColor(c, highlight, amount)
}

// blendColor blends a and b in CIELAB. A t of 0 returns a and 1 returns b.
func blendColor(a, b color.Color, t float64) color.Color {
	if t <= 0 {
		return a
	}
	if t >= 1 {
		return b
	}
	ca, okA := colorful.MakeColor(a)
	cb, okB := colorful.MakeColor(b)
	if !okA || !okB {
		return a
	}
	return ca.BlendLab(cb, t)
}
