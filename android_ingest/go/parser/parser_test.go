package parser

import (
	"bytes"
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"go.skia.org/infra/go/testutils"
)

func TestParse(t *testing.T) {
	testutils.SmallTest(t)
	r := bytes.NewBufferString(INCOMING)
	in, err := Parse(r)
	assert.NoError(t, err)
	assert.Equal(t, "google-marlin-marlin-O", in.Branch)
	assert.Len(t, in.Metrics, 7)
	f, err := in.Metrics["android.platform.systemui.tests.jank.LauncherJankTests#testAppSwitchGMailtoHome"]["frame-avg-jank"].Float64()
	assert.NoError(t, err)
	assert.Equal(t, 8.4, f)
}

func TestParse2(t *testing.T) {
	testutils.SmallTest(t)
	r := bytes.NewBufferString(INCOMING2)
	in, err := Parse(r)
	assert.NoError(t, err)
	assert.Equal(t, "google-angler-angler-O", in.Branch)
	assert.Len(t, in.Metrics, 1)
	f, err := in.Metrics["coremark"]["score"].Float64()
	assert.NoError(t, err)
	assert.Equal(t, 5439.620216, f)
	assert.Equal(t, "coremarkcom.google.android.performance.CoreMarkTest#coremark", in.ResultsName)
}

type lookupMockGood struct {
}

func (l lookupMockGood) Lookup(buildid int64) (string, error) {
	return "8dcc84f7dc8523dd90501a4feb1f632808337c34", nil
}

type lookupMockBad struct {
}

func (l lookupMockBad) Lookup(buildid int64) (string, error) {
	return "", fmt.Errorf("Failed to find buildid.")
}

func TestConvert(t *testing.T) {
	testutils.SmallTest(t)
	c := New(lookupMockGood{}, "google-marlin-marlin-O")
	r := bytes.NewBufferString(INCOMING)
	benchData, err := c.Convert(r)
	assert.NoError(t, err)
	assert.Equal(t, "8dcc84f7dc8523dd90501a4feb1f632808337c34", benchData.Hash)
	assert.Len(t, benchData.Results, 7)
	assert.Equal(t, 8.4, benchData.Results["android.platform.systemui.tests.jank.LauncherJankTests#testAppSwitchGMailtoHome"]["default"]["frame-avg-jank"])
	assert.Equal(t, "marlin-userdebug", benchData.Key["build_flavor"])
}

func TestConvert2(t *testing.T) {
	testutils.SmallTest(t)
	c := New(lookupMockGood{}, "google-angler-angler-O")
	r := bytes.NewBufferString(INCOMING2)
	benchData, err := c.Convert(r)
	assert.NoError(t, err)
	assert.Equal(t, "8dcc84f7dc8523dd90501a4feb1f632808337c34", benchData.Hash)
	assert.Len(t, benchData.Results, 1)
	assert.Equal(t, 5439.620216, benchData.Results["coremark"]["default"]["score"])
}

func TestConvertFailHashLookup(t *testing.T) {
	testutils.SmallTest(t)
	c := New(lookupMockBad{}, "google-marlin-marlin-O")
	r := bytes.NewBufferString(INCOMING)
	_, err := c.Convert(r)
	assert.Error(t, err)
}

func TestConvertFailWrongBranch(t *testing.T) {
	testutils.SmallTest(t)
	c := New(lookupMockGood{}, "this-isnt-the-branch-youre-looking-for")
	r := bytes.NewBufferString(INCOMING)
	_, err := c.Convert(r)
	assert.Error(t, err)
}

const INCOMING = `{
	"build_id": "3567162",
	"build_flavor": "marlin-userdebug",
	"metrics": {
		"android.platform.systemui.tests.jank.LauncherJankTests#testAppSwitchGMailtoHome": {
			"frame-fps": "9.328892269753897",
			"frame-avg-jank": "8.4",
			"frame-max-frame-duration": "7.834711093388444",
			"frame-max-jank": "10"
		},
		"android.platform.systemui.tests.jank.LauncherJankTests#testHomeScreenSwipe": {
			"gfx-avg-slow-ui-thread": "0.10191099340499558",
			"gfx-max-slow-bitmap-uploads": "0.0",
			"gfx-max-frame-time-95": "8",
			"gfx-max-frame-time-50": "5",
			"gfx-max-slow-ui-thread": "0.25510204081632654",
			"gfx-avg-frame-time-50": "5.0",
			"gfx-max-jank": "0.26",
			"gfx-avg-slow-draw": "0.0",
			"gfx-avg-frame-time-95": "7.4",
			"gfx-max-frame-time-90": "7",
			"gfx-avg-frame-time-90": "6.8",
			"gfx-avg-jank": "0.10200000000000001",
			"gfx-max-missed-vsync": "0.0",
			"gfx-avg-slow-bitmap-uploads": "0.0",
			"gfx-max-high-input-latency": "0.0",
			"gfx-max-frame-time-99": "12",
			"gfx-avg-missed-vsync": "0.0",
			"gfx-avg-frame-time-99": "10.4",
			"gfx-max-slow-draw": "0.0",
			"gfx-avg-high-input-latency": "0.0"
		},
		"android.platform.systemui.tests.jank.LauncherJankTests#testWidgetsContainerFling": {
			"gfx-avg-slow-ui-thread": "0.0968528680643497",
			"gfx-max-slow-bitmap-uploads": "0.0",
			"gfx-max-frame-time-95": "9",
			"gfx-max-frame-time-50": "5",
			"gfx-max-slow-ui-thread": "0.24271844660194172",
			"gfx-avg-frame-time-50": "5.0",
			"gfx-max-jank": "0.5",
			"gfx-avg-slow-draw": "0.0",
			"gfx-avg-frame-time-95": "8.2",
			"gfx-max-frame-time-90": "8",
			"gfx-avg-frame-time-90": "7.2",
			"gfx-avg-jank": "0.294",
			"gfx-max-missed-vsync": "0.24271844660194172",
			"gfx-avg-slow-bitmap-uploads": "0.0",
			"gfx-max-high-input-latency": "0.0",
			"gfx-max-frame-time-99": "15",
			"gfx-avg-missed-vsync": "0.14539655738473806",
			"gfx-avg-frame-time-99": "11.0",
			"gfx-max-slow-draw": "0.0",
			"gfx-avg-high-input-latency": "0.0"
		},
		"android.platform.systemui.tests.jank.LauncherJankTests#testAllAppsContainerSwipe": {
			"gfx-avg-slow-ui-thread": "0.07554138508437006",
			"gfx-max-slow-bitmap-uploads": "0.07598784194528875",
			"gfx-max-frame-time-95": "9",
			"gfx-max-frame-time-50": "5",
			"gfx-max-slow-ui-thread": "0.1508295625942685",
			"gfx-avg-frame-time-50": "5.0",
			"gfx-max-jank": "0.3",
			"gfx-avg-slow-draw": "0.045592705167173245",
			"gfx-avg-frame-time-95": "8.2",
			"gfx-max-frame-time-90": "8",
			"gfx-avg-frame-time-90": "7.4",
			"gfx-avg-jank": "0.16599999999999998",
			"gfx-max-missed-vsync": "0.15232292460015232",
			"gfx-avg-slow-bitmap-uploads": "0.01519756838905775",
			"gfx-max-high-input-latency": "0.0",
			"gfx-max-frame-time-99": "11",
			"gfx-avg-missed-vsync": "0.07567937219606331",
			"gfx-avg-frame-time-99": "10.6",
			"gfx-max-slow-draw": "0.22796352583586624",
			"gfx-avg-high-input-latency": "0.0"
		},
		"android.platform.systemui.tests.jank.LauncherJankTests#testAppSwitchChrometoHome": {
			"frame-fps": "9.059377622237943",
			"frame-avg-jank": "8.6",
			"frame-max-frame-duration": "11.048077785923113",
			"frame-max-jank": "9"
		},
		"android.platform.systemui.tests.jank.LauncherJankTests#testAppSwitchPhotostoHome": {
			"frame-fps": "9.342556065428203",
			"frame-avg-jank": "8.0",
			"frame-max-frame-duration": "7.633792937351717",
			"frame-max-jank": "9"
		},
		"android.platform.systemui.tests.jank.LauncherJankTests#testOpenAllAppsContainer": {
			"gfx-avg-slow-ui-thread": "5.040813095770279",
			"gfx-max-slow-bitmap-uploads": "0.0",
			"gfx-max-frame-time-95": "24",
			"gfx-max-frame-time-50": "7",
			"gfx-max-slow-ui-thread": "5.352112676056338",
			"gfx-avg-frame-time-50": "7.0",
			"gfx-max-jank": "8.17",
			"gfx-avg-slow-draw": "1.5528189571212099",
			"gfx-avg-frame-time-95": "22.4",
			"gfx-max-frame-time-90": "14",
			"gfx-avg-frame-time-90": "12.8",
			"gfx-avg-jank": "7.148000000000001",
			"gfx-max-missed-vsync": "3.867403314917127",
			"gfx-avg-slow-bitmap-uploads": "0.0",
			"gfx-max-high-input-latency": "0.0",
			"gfx-max-frame-time-99": "61",
			"gfx-avg-missed-vsync": "3.4349243386335053",
			"gfx-avg-frame-time-99": "51.4",
			"gfx-max-slow-draw": "2.2535211267605635",
			"gfx-avg-high-input-latency": "0.0"
		}
	},
	"branch": "google-marlin-marlin-O"
}`

const INCOMING2 = `{
   "build_id" : "3842951",
   "metrics" : {
      "coremark" : {
         "score" : "5439.620216"
      }
   },
   "results_name" : "coremarkcom.google.android.performance.CoreMarkTest#coremark",
   "build_flavor" : "angler-userdebug",
   "branch" : "google-angler-angler-O"
}`
