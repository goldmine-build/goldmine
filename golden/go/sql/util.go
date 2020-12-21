package sql

import (
	"crypto/md5"
	"encoding/hex"

	"go.skia.org/infra/go/jsonutils"
	"go.skia.org/infra/go/skerr"
	"go.skia.org/infra/golden/go/sql/schema"
	"go.skia.org/infra/golden/go/types"
)

// DigestToBytes returns the given digest as bytes. It returns an error if a missing digest is
// passed in or if the bytes are otherwise invalid.
func DigestToBytes(d types.Digest) (schema.DigestBytes, error) {
	if len(d) != 32 {
		return schema.DigestBytes{}, skerr.Fmt("Empty/missing/invalid digest passed in %q", d)
	}
	out := make(schema.DigestBytes, md5.Size)
	_, err := hex.Decode(out, []byte(d))
	return out, skerr.Wrap(err)
}

// SerializeMap returns the given map in JSON and the MD5 of that json string. nil maps will be
// treated as empty maps. Keys will be sorted lexicographically.
func SerializeMap(m map[string]string) (schema.SerializedJSON, []byte) {
	var jsonBytes []byte
	if len(m) == 0 {
		jsonBytes = []byte("{}")
	} else {
		jsonBytes = jsonutils.MarshalStringMap(m)
	}

	h := md5.Sum(jsonBytes)
	return schema.SerializedJSON(jsonBytes), h[:]
}

// TraceValuesShards is the number of shards we use in the TraceValues Table.
const TraceValuesShards = 8

// ComputeTraceValueShard computes a shard in the range [0,TraceValuesShards) based off the given
// trace hash bytes. This shard range was chosen to be a small number (relative to the thousands
// of ranges that the TraceValues database occupies) that is larger than the number of nodes in the
// cockroachdb cluster. See the overall design doc (go/skia-gold-sql) for more explanation about
// data locality and sharding.
func ComputeTraceValueShard(traceID schema.TraceID) byte {
	return traceID[0] % TraceValuesShards
}
