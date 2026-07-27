package writer

var (
	defaultYAMLIndent = []byte("  ")               //nolint:gochecknoglobals
	yamlElementPrefix = []byte{yamlElement, space} //nolint:gochecknoglobals
)

// YAMLOption configures the [YAML] writer. It threads the configuration value through, so it never
// allocates (see [BufferedOption]).
type YAMLOption func(yamlOptions) yamlOptions

func WithYAMLIndent(indent string) YAMLOption {
	return func(o yamlOptions) yamlOptions {
		o.indent = []byte(indent)

		return o
	}
}

func WithYAMLDocHeading(enabled bool) YAMLOption {
	return func(o yamlOptions) yamlOptions {
		o.withDocHeader = enabled

		return o
	}
}

func WithYAMLBufferedOptions(opts ...BufferedOption) YAMLOption {
	return func(o yamlOptions) yamlOptions {
		o.applyBufferedOptions = opts

		return o
	}
}

type yamlOptions struct {
	indent               []byte
	applyBufferedOptions []BufferedOption
	withDocHeader        bool
}

func yamlOptionsWithDefaults(opts []YAMLOption) yamlOptions {
	o := yamlOptions{indent: defaultYAMLIndent}

	for _, apply := range opts {
		o = apply(o)
	}

	if len(o.indent) == 0 {
		o.indent = defaultYAMLIndent
	}

	return o
}
