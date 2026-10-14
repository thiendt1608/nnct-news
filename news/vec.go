package news

import (
	"strconv"
	"strings"
)

// vecLiteral formats a vector as a pgvector text literal ('[0.1,0.2,...]'),
// to be passed as a text arg and cast with $n::halfvec.
func vecLiteral(v []float32) string {
	var b strings.Builder
	b.WriteByte('[')
	for i, x := range v {
		if i > 0 {
			b.WriteByte(',')
		}
		b.WriteString(strconv.FormatFloat(float64(x), 'g', -1, 32))
	}
	b.WriteByte(']')
	return b.String()
}
