package types

import (
	"fmt"
	"reflect"

	"github.com/go-openapi/swag/conv"
)

// ToNumber convert any native go numerical type to a JSON Number (complex numbers are not supported).
func ToNumber[T conv.Numerical](n T) Number {
	iface := any(n)
	typ := reflect.TypeOf(iface)
	target := reflect.TypeFor[T]()

	if !typ.AssignableTo(target) {
		panic(fmt.Errorf("unsupported type: %T", n))
	}

	v := reflect.ValueOf(n)
	switch {
	case v.CanUint():
		u := v.Uint()
		return Number{Value: []byte(conv.FormatUinteger(u))}

	case v.CanFloat():
		f := v.Float()
		return Number{Value: []byte(conv.FormatFloat(f))}

	case v.CanInt():
		i := v.Int()
		return Number{Value: []byte(conv.FormatInteger(i))}

	default:
		panic(fmt.Errorf("unsupported underlying type: %T", n))
	}
}
