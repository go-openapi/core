package writer

// UnbufferedOption configures the [Unbuffered] writer.
//
// [Unbuffered] currently exposes no configuration knobs; the type exists for API symmetry with the
// other writers and for forward compatibility. Like the other writer options it threads the
// configuration value through, so it never allocates.
type UnbufferedOption func(unbufferedOptions) unbufferedOptions

type unbufferedOptions struct{}
