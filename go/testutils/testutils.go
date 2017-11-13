// Convenience utilities for testing.
package testutils

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"io/ioutil"
	"os"
	"path"
	"reflect"
	"runtime"
	"testing"

	assert "github.com/stretchr/testify/require"
)

const (
	SMALL_TEST  = "small"
	MEDIUM_TEST = "medium"
	LARGE_TEST  = "large"
)

var (
	small         = flag.Bool(SMALL_TEST, false, "Whether or not to run small tests.")
	medium        = flag.Bool(MEDIUM_TEST, false, "Whether or not to run medium tests.")
	large         = flag.Bool(LARGE_TEST, false, "Whether or not to run large tests.")
	uncategorized = flag.Bool("uncategorized", false, "Only run uncategorized tests.")

	// DEFAULT_RUN indicates whether the given test type runs by default
	// when no filter flag is specified.
	DEFAULT_RUN = map[string]bool{
		SMALL_TEST:  true,
		MEDIUM_TEST: true,
		LARGE_TEST:  true,
	}

	TIMEOUT_SMALL  = "4s"
	TIMEOUT_MEDIUM = "15s"
	TIMEOUT_LARGE  = "2m"

	TIMEOUT_RACE = "5m"

	// TEST_TYPES lists all of the types of tests.
	TEST_TYPES = []string{
		SMALL_TEST,
		MEDIUM_TEST,
		LARGE_TEST,
	}
)

// ShouldRun determines whether the test should run based on the provided flags.
func ShouldRun(testType string) bool {
	if *uncategorized {
		return false
	}

	// Fallback if no test filter is specified.
	if !*small && !*medium && !*large {
		return DEFAULT_RUN[testType]
	}

	switch testType {
	case SMALL_TEST:
		return *small
	case MEDIUM_TEST:
		return *medium
	case LARGE_TEST:
		return *large
	}
	return false
}

// SmallTest is a function which should be called at the beginning of a small
// test: A test (under 2 seconds) with no dependencies on external databases,
// networks, etc.
func SmallTest(t *testing.T) {
	if !ShouldRun(SMALL_TEST) {
		t.Skip("Not running small tests.")
	}
}

// MediumTest is a function which should be called at the beginning of an
// medium-sized test: a test (2-15 seconds) which has dependencies on external
// databases, networks, etc.
func MediumTest(t *testing.T) {
	if !ShouldRun(MEDIUM_TEST) || testing.Short() {
		t.Skip("Not running medium tests.")
	}
}

// LargeTest is a function which should be called at the beginning of a large
// test: a test (> 15 seconds) with significant reliance on external
// dependencies which makes it too slow or flaky to run as part of the normal
// test suite.
func LargeTest(t *testing.T) {
	if !ShouldRun(LARGE_TEST) || testing.Short() {
		t.Skip("Not running large tests.")
	}
}

// AssertCopy is AssertDeepEqual but also checks that none of the direct fields
// have a zero value and none of the direct fields point to the same object.
// This catches regressions where a new field is added without adding that field
// to the Copy method. Arguments must be structs.
func AssertCopy(t *testing.T, a, b interface{}) {
	AssertDeepEqual(t, a, b)

	// Check that all fields are non-zero.
	va := reflect.ValueOf(a)
	vb := reflect.ValueOf(b)
	assert.Equal(t, va.Type(), vb.Type(), "Arguments are different types.")
	for va.Kind() == reflect.Ptr {
		assert.Equal(t, reflect.Ptr, vb.Kind(), "Arguments are different types (pointer vs. non-pointer)")
		va = va.Elem()
		vb = vb.Elem()
	}
	assert.Equal(t, reflect.Struct, va.Kind(), "Not a struct or pointer to struct.")
	assert.Equal(t, reflect.Struct, vb.Kind(), "Arguments are different types (pointer vs. non-pointer)")
	for i := 0; i < va.NumField(); i++ {
		fa := va.Field(i)
		z := reflect.Zero(fa.Type())
		if reflect.DeepEqual(fa.Interface(), z.Interface()) {
			assert.FailNow(t, fmt.Sprintf("Missing field %q (or set to zero value).", va.Type().Field(i).Name))
		}
		if fa.Kind() == reflect.Map || fa.Kind() == reflect.Ptr || fa.Kind() == reflect.Slice {
			fb := vb.Field(i)
			assert.NotEqual(t, fa.Pointer(), fb.Pointer(), "Field %q not deep-copied.", va.Type().Field(i).Name)
		}
	}
}

// TestDataDir returns the path to the caller's testdata directory, which
// is assumed to be "<path to caller dir>/testdata".
func TestDataDir() (string, error) {
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		return "", fmt.Errorf("Could not find test data dir: runtime.Caller() failed.")
	}
	for skip := 0; ; skip++ {
		_, file, _, ok := runtime.Caller(skip)
		if !ok {
			return "", fmt.Errorf("Could not find test data dir: runtime.Caller() failed.")
		}
		if file != thisFile {
			return path.Join(path.Dir(file), "testdata"), nil
		}
	}
}

func readFile(filename string) (io.Reader, error) {
	dir, err := TestDataDir()
	if err != nil {
		return nil, fmt.Errorf("Could not read %s: %v", filename, err)
	}
	f, err := os.Open(path.Join(dir, filename))
	if err != nil {
		return nil, fmt.Errorf("Could not read %s: %v", filename, err)
	}
	return f, nil
}

// ReadFile reads a file from the caller's testdata directory.
func ReadFile(filename string) (string, error) {
	f, err := readFile(filename)
	if err != nil {
		return "", err
	}
	b, err := ioutil.ReadAll(f)
	if err != nil {
		return "", fmt.Errorf("Could not read %s: %v", filename, err)
	}
	return string(b), nil
}

// MustReadFile reads a file from the caller's testdata directory and panics on
// error.
func MustReadFile(filename string) string {
	s, err := ReadFile(filename)
	if err != nil {
		panic(err)
	}
	return s
}

// ReadJsonFile reads a JSON file from the caller's testdata directory into the
// given interface.
func ReadJsonFile(filename string, dest interface{}) error {
	f, err := readFile(filename)
	if err != nil {
		return err
	}
	return json.NewDecoder(f).Decode(dest)
}

// MustReadJsonFile reads a JSON file from the caller's testdata directory into
// the given interface and panics on error.
func MustReadJsonFile(filename string, dest interface{}) {
	if err := ReadJsonFile(filename, dest); err != nil {
		panic(err)
	}
}

// WriteFile writes the given contents to the given file path, reporting any
// error.
func WriteFile(t assert.TestingT, filename, contents string) {
	assert.NoErrorf(t, ioutil.WriteFile(filename, []byte(contents), os.ModePerm), "Unable to write to file %s", filename)
}

// CloseInTest takes an ioutil.Closer and Closes it, reporting any error.
func CloseInTest(t assert.TestingT, c io.Closer) {
	if err := c.Close(); err != nil {
		t.Errorf("Failed to Close(): %v", err)
	}
}

// AssertCloses takes an ioutil.Closer and asserts that it closes.
func AssertCloses(t assert.TestingT, c io.Closer) {
	assert.NoError(t, c.Close())
}

// Remove attempts to remove the given file and asserts that no error is returned.
func Remove(t assert.TestingT, fp string) {
	assert.NoError(t, os.Remove(fp))
}

// RemoveAll attempts to remove the given directory and asserts that no error is returned.
func RemoveAll(t assert.TestingT, fp string) {
	assert.NoError(t, os.RemoveAll(fp))
}

// TempDir is a wrapper for ioutil.TempDir. Returns the path to the directory and a cleanup
// function to defer.
func TempDir(t assert.TestingT) (string, func()) {
	d, err := ioutil.TempDir("", "testutils")
	assert.NoError(t, err)
	return d, func() {
		RemoveAll(t, d)
	}
}

// MarshalJSON encodes the given interface to a JSON string.
func MarshalJSON(t *testing.T, i interface{}) string {
	b, err := json.Marshal(i)
	assert.NoError(t, err)
	return string(b)
}

// MarshalIndentJSON encodes the given interface to an indented JSON string.
func MarshalIndentJSON(t *testing.T, i interface{}) string {
	b, err := json.MarshalIndent(i, "", "  ")
	assert.NoError(t, err)
	return string(b)
}
