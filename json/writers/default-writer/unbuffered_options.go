// SPDX-FileCopyrightText: Copyright 2015-2025 go-swagger maintainers
// SPDX-License-Identifier: Apache-2.0

package writer

// UnbufferedOption configures the [Unbuffered] writer. Like the other writer options it threads the configuration
// value through, so it never allocates.
type UnbufferedOption func(unbufferedOptions) unbufferedOptions

// WithUnbufferedUTF8Policy is [WithUTF8Policy] for the [Unbuffered] writer.
//
// It is a separate function only because Go options are typed per writer and [Unbuffered] does not take
// [BufferedOption]s; the knob and its meaning are identical.
func WithUnbufferedUTF8Policy(policy UTF8Policy) UnbufferedOption {
	return func(o unbufferedOptions) unbufferedOptions {
		o.utf8Policy = policy

		return o
	}
}

type unbufferedOptions struct {
	utf8Policy UTF8Policy
}

func unbufferedOptionsWithDefaults(opts []UnbufferedOption) unbufferedOptions {
	var o unbufferedOptions // the zero value is UTF8Strict
	for _, apply := range opts {
		o = apply(o)
	}

	return o
}
