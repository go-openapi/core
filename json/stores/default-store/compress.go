package store

import (
	"io"
)

func compressionRatio(level int) int {
	//nolint:mnd
	// ratios for text data
	switch level {
	case 0:
		return 1
	case 1:
		return 4
	case 2:
		return 5
	case 3:
		return 6
	case 4:
		return 7
	case 5:
		return 8
	case 6:
		return 9
	case 7:
		return 10
	case 8:
		return 11
	case 9:
		return 12
	default:
		return 9
	}
}

func (s *Store) compressRatioHeuristic(size int) int {
	return max(size/compressionRatio(s.compressionLevel), minCompressedSize+1)
}

func (s *Store) uncompressRatioHeuristic(size int) int {
	if size <= minCompressedSize {
		// compressed size is at minimum: means that we are likely to have a higher than usual compression ratio
		return 2 * size * compressionRatio(s.compressionLevel)
	}

	return size * compressionRatio(s.compressionLevel)
}

func (s *Store) compressString(value []byte, buffer ...[]byte) []byte {
	wrt, redeemWriter := borrowBufferWithRedeem(s.compressRatioHeuristic(len(value)))
	defer redeemWriter()

	deflater := s.compressWriter()
	deflater.Reset(wrt)

	_, err := deflater.Write(value)
	assertCompressDeflateError(err)
	_ = deflater.Close()

	out := ensureEmptyBuffer(wrt.Len(), buffer...)
	out = append(
		out,
		wrt.Bytes()...) // cannot return the slice from wrt, which becomes unusable after the parent writer is redeemed

	return out
}

func (s *Store) uncompressString(value []byte, buffer ...[]byte) []byte {
	rdr, redeemReader := poolOfReaders.BorrowWithRedeem()
	rdr.Set(value)
	wrt, redeemWriter := borrowBufferWithRedeem(
		s.uncompressRatioHeuristic(len(value)),
	) // TODO: avoid the extra copy
	defer redeemWriter()

	inflater, redeemInflater := borrowFlateReaderWithRedeem(rdr, s.dict)

	_, err := io.Copy(wrt, inflater)
	assertCompressInflateError(err)
	_ = inflater.Close()

	redeemInflater()
	redeemReader()

	out := ensureEmptyBuffer(wrt.Len(), buffer...)
	out = append(
		out,
		wrt.Bytes()...) // copy here because we cannot return the slice from wrt, which becomes unusable after the parent writer is redeemed. TODO: modified version of bytes.Buffer to return ownership of the underlying slice

	return out
}

// appendUncompressString decompresses value and appends the result to dst, returning the extended
// slice. It is the append-style counterpart of [Store.uncompressString] used by
// [Store.AppendValueBytes]; the scratch reader/writer/inflater come from pools, so it does not
// allocate beyond growing dst.
func (s *Store) appendUncompressString(dst, value []byte) []byte {
	rdr, redeemReader := poolOfReaders.BorrowWithRedeem()
	rdr.Set(value)
	wrt, redeemWriter := borrowBufferWithRedeem(s.uncompressRatioHeuristic(len(value)))
	defer redeemWriter()

	inflater, redeemInflater := borrowFlateReaderWithRedeem(rdr, s.dict)

	_, err := io.Copy(wrt, inflater)
	assertCompressInflateError(err)
	_ = inflater.Close()

	redeemInflater()
	redeemReader()

	return append(dst, wrt.Bytes()...)
}

func (s *Store) uncompressStringReader(value []byte) (io.Reader, func()) {
	rdr, redeemReader := borrowBufferWithRedeem(
		len(value),
	) // use [bytes.Buffer] rather than [bytes.Reader] because it may be recycled
	_, _ = rdr.Write(
		value,
	) // since we don't use [bytes.Reader] we indulge into an extra copy
	inflater, redeemInflater := borrowFlateReaderWithRedeem(rdr, s.dict)

	return inflater, func() {
		_ = inflater.Close()
		redeemInflater()
		redeemReader()
	}
}
