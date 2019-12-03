package assertdeep

import (
	"bytes"
	"encoding/json"
	"fmt"
	"reflect"

	"github.com/davecgh/go-spew/spew"
	"github.com/pmezard/go-difflib/difflib"
	"github.com/stretchr/testify/require"
	"go.skia.org/infra/go/deepequal"
	"go.skia.org/infra/go/sktest"
)

// Twiddle this flag for the complete spew.SDump of an object
// This is useful when pumped into a diff tool, but likely extreme
// overkill for most uses. The diff that is created by default should
// be enough for the average test.
var superVerbose = false

// Equal fails the test if the two objects do not pass reflect.DeepEqual.
func Equal(t sktest.TestingT, expected, actual interface{}) {
	if !deepequal.DeepEqual(expected, actual) {
		// The formatting is inspired by stretchr/testify's require.Equal() output.
		extra := ""
		if doDetailedDiff(expected, actual) {
			e := spewConfig.Sdump(expected)
			a := spewConfig.Sdump(actual)

			diff, _ := difflib.GetUnifiedDiffString(difflib.UnifiedDiff{
				A:        difflib.SplitLines(e),
				B:        difflib.SplitLines(a),
				FromFile: "Expected",
				FromDate: "",
				ToFile:   "Actual",
				ToDate:   "",
				Context:  2,
			})

			extra = "\n\nDiff:\n" + diff
		}

		if superVerbose {
			require.FailNow(t, fmt.Sprintf("Objects do not match: \na:\n%s\n\nb:\n%s\n%s", spew.Sdump(expected), spew.Sdump(actual), extra))
		} else {
			require.FailNow(t, fmt.Sprintf("Objects do not match: \na:\n%#v\n\nb:\n%#v\n%s", expected, actual, extra))
		}
	}
}

// doDetailedDiff returns true if doing a detailed diff would help. This means if
// the two objects are the same type and are one of the complicated types:
// e.g. Map, Slice, Struct, etc.
func doDetailedDiff(e, a interface{}) bool {
	if e == nil || a == nil {
		return false
	}

	et := reflect.TypeOf(e)
	ek := et.Kind()
	if ek == reflect.Ptr {
		et = et.Elem()
		ek = et.Kind()
	}
	at := reflect.TypeOf(a)
	ak := at.Kind()
	if ak == reflect.Ptr {
		at = at.Elem()
		ak = at.Kind()
	}

	if et != at {
		return false
	}

	if ek != reflect.Struct && ek != reflect.Map && ek != reflect.Slice && ek != reflect.Array {
		return false
	}
	return true
}

var spewConfig = spew.ConfigState{
	Indent:                  "  ",
	DisablePointerAddresses: true,
	DisableCapacities:       true,
	SortKeys:                true,
}

// Copy is Equal but also checks that none of the direct fields
// have a zero value and none of the direct fields point to the same object.
// This catches regressions where a new field is added without adding that field
// to the Copy method. Arguments must be structs.
func Copy(t sktest.TestingT, a, b interface{}) {
	Equal(t, a, b)

	// Check that all fields are non-zero.
	va := reflect.ValueOf(a)
	vb := reflect.ValueOf(b)
	require.Equal(t, va.Type(), vb.Type(), "Arguments are different types.")
	for va.Kind() == reflect.Ptr {
		require.Equal(t, reflect.Ptr, vb.Kind(), "Arguments are different types (pointer vs. non-pointer)")
		va = va.Elem()
		vb = vb.Elem()
	}
	require.Equal(t, reflect.Struct, va.Kind(), "Not a struct or pointer to struct.")
	require.Equal(t, reflect.Struct, vb.Kind(), "Arguments are different types (pointer vs. non-pointer)")
	for i := 0; i < va.NumField(); i++ {
		fa := va.Field(i)
		z := reflect.Zero(fa.Type())
		if reflect.DeepEqual(fa.Interface(), z.Interface()) {
			require.FailNow(t, fmt.Sprintf("Missing field %q (or set to zero value).", va.Type().Field(i).Name))
		}
		if fa.Kind() == reflect.Map || fa.Kind() == reflect.Ptr || fa.Kind() == reflect.Slice {
			fb := vb.Field(i)
			require.NotEqual(t, fa.Pointer(), fb.Pointer(), "Field %q not deep-copied.", va.Type().Field(i).Name)
		}
	}
}

// JSONRoundTripEqual encodes and decodes an object to/from JSON and asserts
// that the result is deep equal to the original. obj must be a pointer.
func JSONRoundTripEqual(t sktest.TestingT, obj interface{}) {
	val := reflect.ValueOf(obj)
	require.Equal(t, reflect.Ptr, val.Kind(), "JSONRoundTripEqual must be passed a pointer.")
	cpyval := reflect.New(val.Elem().Type())
	cpy := cpyval.Interface()
	buf := bytes.Buffer{}
	require.NoError(t, json.NewEncoder(&buf).Encode(obj))
	require.NoError(t, json.NewDecoder(&buf).Decode(cpy))
	Equal(t, obj, cpy)
}
