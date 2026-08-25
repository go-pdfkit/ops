package ops

import "github.com/go-pdfkit/reader"

// resolve follows an indirect reference. A document that opened cannot fail to
// resolve one — an object it cannot find reads as null, and one it cannot read
// at all makes the whole file fail to open — so there is nothing to handle at
// every use, and pretending otherwise only leaves branches nothing can reach.
// The result is fed to the conversions below, which cope with nothing at all.
func resolve(src *reader.Document, o reader.Object) reader.Object {
	out, _ := src.Resolve(o)
	return out
}

// resolveDict follows a reference and reports the dictionary at the end of it.
func resolveDict(src *reader.Document, o reader.Object) (reader.Dict, bool) {
	return reader.ToDict(resolve(src, o))
}

// resolveArray follows a reference and reports the array at the end of it.
func resolveArray(src *reader.Document, o reader.Object) (reader.Array, bool) {
	return reader.ToArray(resolve(src, o))
}

// resolveFloat follows a reference and reports the number at the end of it.
func resolveFloat(src *reader.Document, o reader.Object) (float64, bool) {
	return reader.ToFloat(resolve(src, o))
}
