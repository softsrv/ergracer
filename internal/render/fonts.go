package render

import (
	"fmt"

	"github.com/golang/freetype/truetype"
	"golang.org/x/image/font"
	"golang.org/x/image/font/gofont/gobold"
	"golang.org/x/image/font/gofont/gomono"
	"golang.org/x/image/font/gofont/gomonobold"
	"golang.org/x/image/font/gofont/goregular"
)

// fontFamily identifies which of the Go team's bundled TrueType fonts to
// parse a face from.
type fontFamily int

const (
	fontRegular fontFamily = iota
	fontBold
	fontMono
	fontMonoBold
)

// parsedFonts caches the parsed *truetype.Font for each bundled family so
// repeated calls to loadFace don't re-parse the TTF bytes.
var parsedFonts = map[fontFamily]*truetype.Font{}

func init() {
	sources := map[fontFamily][]byte{
		fontRegular:  goregular.TTF,
		fontBold:     gobold.TTF,
		fontMono:     gomono.TTF,
		fontMonoBold: gomonobold.TTF,
	}
	for family, ttf := range sources {
		f, err := truetype.Parse(ttf)
		if err != nil {
			// The bundled fonts are compiled into the binary and never change
			// at runtime, so a parse failure here indicates a corrupted build,
			// not a recoverable runtime condition.
			panic(fmt.Sprintf("render: parse bundled font %d: %v", family, err))
		}
		parsedFonts[family] = f
	}
}

// loadFace builds a font.Face for the given bundled family at the given
// point size. Callers should not mutate or close the returned face's
// underlying font, since it is shared/cached.
func loadFace(family fontFamily, points float64) font.Face {
	f, ok := parsedFonts[family]
	if !ok {
		panic(fmt.Sprintf("render: unknown font family %d", family))
	}
	return truetype.NewFace(f, &truetype.Options{
		Size: points,
		DPI:  72,
	})
}
