// SPDX-FileCopyrightText: Copyright 2015-2025 go-swagger maintainers
// SPDX-License-Identifier: Apache-2.0

package types

import "strconv"

//nolint:gochecknoglobals // static values declared for convenience and that won't allocate at every use.
var (
	// Zero numerical value
	Zero = Number{Value: []byte{'0'}}
	// True JSON value
	True = Boolean{Value: true, defined: true}
	// False JSON value
	False = Boolean{Value: false, defined: true}
	// EmptyString is the empty string ""
	EmptyString = String{Value: []byte{}}
	// Null JSON value
	Null = NullType{defined: true}
)

// String represent a JSON string.
type String struct {
	Value []byte
}

func (s String) String() string {
	return string(s.Value)
}

func (s String) IsDefined() bool {
	return s.Value != nil
}

// Number represents a JSON number.
type Number struct {
	Value []byte
}

func (n Number) IsDefined() bool {
	return len(n.Value) != 0
}

func (n Number) String() string {
	return string(n.Value)
}

func (n Number) Preferred() any {
	// TODO: returns the preferred native representation of this Number
	return nil
}

// Boolean represents a JSON boolean value.
type Boolean struct {
	Value   bool
	defined bool
}

func (b *Boolean) Set(v bool) {
	b.Value = v
	b.defined = true
}

func (b Boolean) With(v bool) Boolean {
	b.Value = v
	b.defined = true

	return b
}

func (b Boolean) IsDefined() bool {
	return b.defined
}

func (b Boolean) Bool() bool {
	return b.Value
}

func (b Boolean) String() string {
	return strconv.FormatBool(b.Value)
}

type NullType struct {
	defined bool
}

func (n NullType) IsDefined() bool {
	return n.defined
}

func (n *NullType) Set() {
	n.defined = true
}

func (n *NullType) Unset() {
	n.defined = false
}

func (n NullType) String() string {
	return "null"
}

type Definable interface {
	IsDefined() bool
}

// Nullable is a wrapper type for any type that supports IsDefined() bool.
type Nullable[T Definable] struct {
	Inner  T
	isNull bool
}

func (n *Nullable[T]) SetNull() {
	var zero T
	n.Inner = zero
	n.isNull = true
}

func (n Nullable[T]) WithNull() Nullable[T] {
	var zero T
	n.Inner = zero
	n.isNull = true

	return n
}

func (n Nullable[T]) IsDefined() bool {
	return n.Inner.IsDefined() && !n.isNull
}

func (n Nullable[T]) IsNull() bool {
	return !n.Inner.IsDefined() && n.isNull
}
