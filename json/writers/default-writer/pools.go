//nolint:gochecknoglobals  // pools are globals
package writer

import (
	"io"

	"github.com/go-openapi/swag/pools"
)

const (
	defaultCapacityForNumbers = 20
	defaultCapacityForReaders = 4096
	defaultCapacityForEscaped = 512
)

var (
	// Writer-instance pools. Configuration (the *Options structs) is no longer pooled: it is a plain
	// value threaded through the options and stored by value on the writer — see buffered_options.go.
	poolOfUnbuffered = pools.New[Unbuffered]()
	poolOfBuffered   = pools.New[Buffered]()
	poolOfIndented   = pools.New[Indented]()
	poolOfYAML       = pools.New[YAML]()

	poolOfNumberBuffers = pools.NewPoolSlice[byte](
		pools.WithMinimumCapacity(defaultCapacityForNumbers),
	)
	poolOfReadBuffers = pools.NewPoolSlice[byte](
		pools.WithLength(defaultCapacityForReaders),
	)
	poolOfEscapedBuffers = pools.NewPoolSlice[byte]( // TODO: improve that one
		pools.WithMinimumCapacity(defaultCapacityForEscaped),
	)

	poolOfBuffers = pools.NewPoolSlice[byte]()
)

// BorrowUnbuffered recycles an [Unbuffered] writer from the global pool.
//
// [BorrowUnbuffered] is equivalent to [NewUnbuffered], but may save the allocation of new resources if
// they are readily available in the pool.
//
// The caller is responsible for calling [RedeemUnbuffered] after the work is done, and relinquish resources to the pool.
func BorrowUnbuffered(writer io.Writer, _ ...UnbufferedOption) *Unbuffered {
	w := poolOfUnbuffered.Borrow()
	w.w = writer
	w.bw, _ = writer.(io.ByteWriter)
	w.jw = &w.unbuffered

	return w
}

// RedeemUnbuffered relinquishes a borrowed [Unbuffered] writer back to the global pool.
//
// Inner resources are relinquished by this call.
func RedeemUnbuffered(w *Unbuffered) {
	w.redeem() // redeem inner resources
	poolOfUnbuffered.Redeem(w)
}

func BorrowBuffered(writer io.Writer, opts ...BufferedOption) *Buffered {
	w := poolOfBuffered.Borrow()
	w.w = writer
	w.bufferedOptions = bufferedOptionsWithDefaults(opts)
	w.borrowBuffer()
	w.jw = &w.buffered

	return w
}

func RedeemBuffered(w *Buffered) {
	w.redeem() // redeem inner resources
	poolOfBuffered.Redeem(w)
}

func BorrowIndented(writer io.Writer, opts ...IndentedOption) *Indented {
	w := poolOfIndented.Borrow()
	w.indentedOptions = indentedOptionsWithDefaults(opts)

	// The inner Buffered is borrowed for exactly one Borrow/Redeem cycle: [Indented.redeem]
	// hands it back to poolOfBuffered and clears the field, since holding on to a redeemed
	// instance would let two owners share it. A leftover Buffered here can only come from a
	// writer built with [NewIndented] and then passed to [RedeemIndented]: that one is not
	// pool-managed (its working buffer is reclaimed by its own GC cleanup), so it is simply
	// dropped by the assignment below.
	w.Buffered = BorrowBuffered(writer, w.applyBufferedOptions...)
	w.redeemBuffered = w.Buffered // mark for redemption later on

	return w
}

func RedeemIndented(w *Indented) {
	w.redeem() // redeem inner resources
	poolOfIndented.Redeem(w)
}

func BorrowYAML(writer io.Writer, opts ...YAMLOption) *YAML {
	w := poolOfYAML.Borrow()
	w.yamlOptions = yamlOptionsWithDefaults(opts)

	// The inner Buffered is borrowed for exactly one Borrow/Redeem cycle: [YAML.redeem] hands
	// it back to poolOfBuffered and clears the field, since holding on to a redeemed instance
	// would let two owners share it. A leftover Buffered here can only come from a writer built
	// with [NewYAML] and then passed to [RedeemYAML]: that one is not pool-managed (its working
	// buffer is reclaimed by its own GC cleanup), so it is simply dropped by the assignment
	// below.
	w.Buffered = BorrowBuffered(writer, w.applyBufferedOptions...)
	w.redeemBuffered = w.Buffered // mark for redemption later on

	return w
}

func RedeemYAML(w *YAML) {
	w.redeem() // redeem inner resources
	poolOfYAML.Redeem(w)
}
