package writer

const (
	comma                = ','
	colon                = ':'
	closingBracket       = '}'
	closingSquareBracket = ']'
	openingBracket       = '{'
	openingSquareBracket = '['
	quote                = '"'
	newline              = '\n'
	space                = ' '
	lowestPrintable      = byte(0x20)
)

var (
	trueBytes  = []byte("true")  //nolint:gochecknoglobals
	falseBytes = []byte("false") //nolint:gochecknoglobals
	nullToken  = []byte("null")  //nolint:gochecknoglobals
)
