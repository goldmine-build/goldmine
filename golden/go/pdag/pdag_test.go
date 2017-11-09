package pdag

import (
	"fmt"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"go.skia.org/infra/go/testutils"
	"go.skia.org/infra/go/util"

	assert "github.com/stretchr/testify/require"
)

// The simulated duration of each function in ms.
const FN_DURATION_MS = 500

type extype struct {
	data  map[string]int
	mutex sync.Mutex
}

func TestSimpleTopology(t *testing.T) {
	testutils.SmallTest(t)
	rootFn := func(ctx interface{}) error {
		d := ctx.(*extype)
		d.data["val"] = 0
		return nil
	}

	sinkFn := func(ctx interface{}) error {
		d := ctx.(*extype)
		d.data["val2"] = d.data["val"] * 100
		return nil
	}

	// Create a two node topology with a source and a sink.
	root := NewNode(rootFn)
	root.Child(sinkFn)

	// Create a context and trigger in the root node.
	d := &extype{data: map[string]int{}}
	err := root.Trigger(d)

	assert.Nil(t, err)
	assert.Equal(t, 2, len(d.data))
	assert.Equal(t, d.data["val"], 0)
	assert.Equal(t, d.data["val2"], 0)
}

func TestGenericTopology(t *testing.T) {
	testutils.SmallTest(t)
	orderCh := make(chan string, 5)

	rootFn := func(ctx interface{}) error {
		d := ctx.(*extype)
		d.data["val"] = 0
		orderCh <- "a"
		return nil
	}

	bFn := incFn(1, orderCh, "b")
	cFn := incFn(10, orderCh, "c")
	dFn := incFn(100, orderCh, "d")

	sinkFn := func(ctx interface{}) error {
		d := ctx.(*extype)
		d.data["val2"] = d.data["val"] * 100
		orderCh <- "e"
		return nil
	}

	// Create a topology that fans out to aFn, bFn, cFn and
	// then collects the results in a sink function.
	root := NewNode(rootFn)
	NewNode(sinkFn,
		root.Child(bFn),
		root.Child(cFn),
		root.Child(dFn))

	// Create a context and trigger in the root node.
	d := &extype{data: map[string]int{}}
	start := time.Now()
	err := root.Trigger(d)
	delta := time.Now().Sub(start)

	assert.Nil(t, err)

	// Make sure the functions are roughly called in parallel.
	assert.True(t, delta < (2*FN_DURATION_MS*time.Millisecond))
	assert.Equal(t, len(d.data), 2)
	assert.Equal(t, len(orderCh), 5)
	assert.Equal(t, d.data["val"], 111)
	assert.Equal(t, d.data["val2"], 11100)

	// Make sure the functions are called in the right order.
	assert.Equal(t, <-orderCh, "a")
	parallel := []string{<-orderCh, <-orderCh, <-orderCh}
	sort.Strings(parallel)
	assert.Equal(t, []string{"b", "c", "d"}, parallel)
	assert.Equal(t, <-orderCh, "e")
}

func TestError(t *testing.T) {
	testutils.SmallTest(t)
	errFn := func(c interface{}) error {
		return fmt.Errorf("Not Implemented")
	}

	root := NewNode(NoOp)
	root.Child(NoOp).
		Child(NoOp).
		Child(errFn).
		Child(NoOp)

	err := root.Trigger(nil)
	assert.NotNil(t, err)
	assert.Equal(t, "Not Implemented", err.Error())
}

func TestComplexCallOrder(t *testing.T) {
	testutils.SmallTest(t)
	aFn := orderFn("a")
	bFn := orderFn("b")
	cFn := orderFn("c")
	dFn := orderFn("d")
	eFn := orderFn("e")
	fFn := orderFn("f")
	gFn := orderFn("g")

	a := NewNode(aFn).setName("a")
	b := a.Child(bFn).setName("b")
	c := a.Child(cFn).setName("c")
	b.Child(dFn).setName("d")
	e := NewNode(eFn, b, c).setName("e")
	NewNode(fFn, b, e).setName("f")
	e.Child(gFn).setName("g")

	// Create a context and trigger in the root node.
	data := make(chan string, 100)
	a.verbose = true
	assert.NoError(t, a.Trigger(data))
	close(data)
	o := ""
	for c := range data {
		o += c
	}
	bPos := strings.Index(o, "b")
	dPos := strings.Index(o, "d")
	assert.True(t, (bPos >= 0) && (dPos > bPos))

	// make sure d is called after b
	results := map[string]bool{
		"abcefg": true,
		"abcegf": true,
		"acbefg": true,
		"acbegf": true,
	}
	o = o[0:dPos] + o[dPos+1:]
	assert.True(t, results[o])

	// Make a call an node in the DAG and make the call order works.
	data = make(chan string, 100)
	b.verbose = true
	assert.NoError(t, b.Trigger(data))
	close(data)
	o = ""
	for c := range data {
		o += c
	}
	expSet := util.NewStringSet([]string{"bedfg", "bdefg", "bedgf", "bdegf"})
	assert.True(t, expSet[o])
}

func orderFn(msg string) ProcessFn {
	return func(ctx interface{}) error {
		ctx.(chan string) <- msg
		return nil
	}
}

func incFn(increment int, ch chan<- string, chVal string) ProcessFn {
	return func(ctx interface{}) error {
		d := ctx.(*extype)
		d.mutex.Lock()
		d.data["val"] += increment
		d.mutex.Unlock()
		ch <- chVal
		time.Sleep(time.Millisecond * FN_DURATION_MS)
		return nil
	}
}
