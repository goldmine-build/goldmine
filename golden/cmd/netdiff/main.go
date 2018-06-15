// Simple command line app that compares the images based on their digests.
// This is a simple standalone client to the skia_image_server.
// Primarily used for debugging.
package main

import (
	"flag"
	"fmt"
	"os"

	"google.golang.org/grpc"

	"go.skia.org/infra/go/common"
	"go.skia.org/infra/go/sklog"
	"go.skia.org/infra/golden/go/diff"
	"go.skia.org/infra/golden/go/diffstore"
)

var (
	grpcAddr = flag.String("grpc_address", "localhost:9000", "gRPC service address (e.g., ':9000')")
)

func main() {
	defer common.LogPanic()
	common.Init()
	if flag.NArg() < 2 {
		sklog.Fatalf("Usage: %s digest1 digest2 [digest3 ... digestN]\n", os.Args[0])
	}

	args := flag.Args()
	mainDigest := args[0]
	rightDigests := args[1:]

	// Create the client connection and connect to the server.
	conn, err := grpc.Dial(*grpcAddr, grpc.WithInsecure())
	if err != nil {
		sklog.Fatalf("Unable to connect to grpc service: %s", err)
	}

	codec := diffstore.MetricMapCodec{}
	diffStore, err := diffstore.NewNetDiffStore(conn, "", codec)
	if err != nil {
		sklog.Fatalf("Unable to initialize NetDiffStore: %s", err)
	}

	diffResult, err := diffStore.Get(diff.PRIORITY_NOW, mainDigest, rightDigests)
	if err != nil {
		sklog.Fatalf("Unable to compare digests: %s", err)
	}

	for _, rDigest := range rightDigests {
		fmt.Printf("%s <-> %s\n", mainDigest, rDigest)
		if metrics, ok := diffResult[rDigest].(*diff.DiffMetrics); ok {
			fmt.Printf("    Dimensions are different: %v\n", metrics.DimDiffer)
			fmt.Printf("    Number of pixels different: %v\n", metrics.NumDiffPixels)
			fmt.Printf("    Pixel diff percent: %v\n", metrics.PixelDiffPercent)
			fmt.Printf("    Max RGBA: %v\n", metrics.MaxRGBADiffs)
		} else {
			fmt.Printf("    ERR: No result available.")
		}
	}
}
