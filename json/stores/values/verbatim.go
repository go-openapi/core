package values

// VerbatimValue represents a JSON scalar value together with any non-significant blank space
// that occurred before the value token.
type VerbatimValue struct {
	Value

	blanks []byte
}

func (v VerbatimValue) Blanks() []byte {
	return v.blanks
}

// MakeVerbatimValue builds a [VerbatimValue] from blanks and a [Value].
//
// The caller should make sure that the blanks slice only contain legit JSON blank characters
// (i.e. ' ', '\t', '\n', '\r').
func MakeVerbatimValue(blanks []byte, value Value) VerbatimValue {
	return VerbatimValue{
		Value:  value,
		blanks: blanks,
	}
}
