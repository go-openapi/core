package writer

var defaultIndent = []byte("  ") //nolint:gochecknoglobals

// IndentedOption configures the [Indented] writer. It threads the configuration value through, so it
// never allocates (see [BufferedOption]).
type IndentedOption func(indentedOptions) indentedOptions

func WithIndent(indent string) IndentedOption {
	return func(o indentedOptions) indentedOptions {
		o.indent = []byte(indent)

		return o
	}
}

func WithIndentBufferedOptions(opts ...BufferedOption) IndentedOption {
	return func(o indentedOptions) indentedOptions {
		o.applyBufferedOptions = opts

		return o
	}
}

type indentedOptions struct {
	indent               []byte
	applyBufferedOptions []BufferedOption
}

func indentedOptionsWithDefaults(opts []IndentedOption) indentedOptions {
	o := indentedOptions{indent: defaultIndent}

	for _, apply := range opts {
		o = apply(o)
	}

	if len(o.indent) == 0 {
		o.indent = defaultIndent
	}

	return o
}
