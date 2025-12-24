module go.goldmine.build

require (
	cloud.google.com/go/iam v1.5.2
	cloud.google.com/go/logging v1.13.0
	cloud.google.com/go/pubsub v1.49.0
	cloud.google.com/go/secretmanager v1.14.7
	cloud.google.com/go/storage v1.52.0
	contrib.go.opencensus.io/exporter/stackdriver v0.13.4
	github.com/Jeffail/gabs/v2 v2.6.0
	github.com/Masterminds/sprig v2.22.0+incompatible
	github.com/a8m/envsubst v1.2.0
	github.com/aclements/go-moremath v0.0.0-20190830160640-d16893ddf098
	github.com/bartventer/httpcache v0.12.0
	github.com/bazelbuild/bazel-gazelle v0.45.0
	github.com/blakesmith/ar v0.0.0-20190502131153-809d4375e1fb
	github.com/bradfitz/gomemcache v0.0.0-20190913173617-a41fca850d0b
	github.com/cenkalti/backoff v2.2.1+incompatible
	github.com/cenkalti/backoff/v4 v4.3.0
	github.com/cockroachdb/cockroach-go/v2 v2.1.0
	github.com/davecgh/go-spew v1.1.2-0.20180830191138-d8f796af33cc
	github.com/dustin/go-humanize v1.0.1
	github.com/fiorix/go-web v1.0.1-0.20150221144011-5b593f1e8966
	github.com/flynn/json5 v0.0.0-20160717195620-7620272ed633
	github.com/go-chi/chi/v5 v5.0.8
	github.com/go-python/gpython v0.0.3
	github.com/golang/groupcache v0.0.0-20210331224755-41bb18bfe9da
	github.com/golang/protobuf v1.5.4
	github.com/google/go-github/v80 v80.0.0
	github.com/google/go-licenses v0.0.0-20210816172045-3099c18c36e1
	github.com/google/uuid v1.6.0
	github.com/gorilla/securecookie v1.1.1
	github.com/hashicorp/go-multierror v1.1.1
	github.com/hashicorp/golang-lru v1.0.2
	github.com/invopop/jsonschema v0.7.0
	github.com/jackc/pgconn v1.14.0
	github.com/jackc/pgtype v1.14.0
	github.com/jackc/pgx/v4 v4.18.1
	github.com/jcgregorio/logger v0.1.3
	github.com/kisielk/errcheck v1.5.0
	github.com/miekg/dns v1.1.65
	github.com/mohae/deepcopy v0.0.0-20170929034955-c48cc78d4826
	github.com/nfnt/resize v0.0.0-20180221191011-83c6a9932646
	github.com/otiai10/copy v1.6.0
	github.com/patrickmn/go-cache v2.1.0+incompatible
	github.com/pmezard/go-difflib v1.0.1-0.20181226105442-5d4384ee4fb2
	github.com/prometheus/client_golang v1.22.0
	github.com/r3labs/sse/v2 v2.8.1
	github.com/sendgrid/sendgrid-go v3.11.1+incompatible
	github.com/skia-dev/google-api-go-client v0.10.1-0.20200109184256-16c3d6f408b2
	github.com/skia-dev/protoc-gen-twirp_typescript v0.0.0-20220429132620-ad26708b7787
	github.com/spf13/cobra v1.8.1
	github.com/stretchr/testify v1.10.0
	github.com/syndtr/goleveldb v1.0.0
	github.com/tarm/serial v0.0.0-20180830185346-98f6abe2eb07
	github.com/texttheater/golang-levenshtein v1.0.1
	github.com/twitchtv/twirp v7.1.0+incompatible
	github.com/unrolled/secure v1.17.0
	github.com/urfave/cli/v2 v2.17.0
	github.com/vektra/mockery/v2 v2.38.0
	github.com/willf/bitset v1.1.11
	github.com/xeipuuv/gojsonschema v1.2.0
	github.com/yannh/kubeconform v0.6.3
	github.com/yusufpapurcu/wmi v1.2.4
	github.com/zeebo/bencode v1.0.0
	go.opencensus.io v0.24.0
	golang.org/x/net v0.43.0
	golang.org/x/oauth2 v0.29.0
	golang.org/x/sync v0.16.0
	golang.org/x/term v0.34.0
	golang.org/x/time v0.11.0
	golang.org/x/tools v0.36.0
	google.golang.org/api v0.230.0
	google.golang.org/genproto v0.0.0-20250422160041-2d3770c4ea7f
	google.golang.org/grpc v1.72.0
	google.golang.org/protobuf v1.36.6
	gopkg.in/fsnotify.v1 v1.4.7
	gopkg.in/olivere/elastic.v5 v5.0.86
	gopkg.in/yaml.v2 v2.4.0
	k8s.io/api v0.32.2
	k8s.io/apimachinery v0.32.2
	k8s.io/client-go v0.32.2
	k8s.io/kubectl v0.22.12
	sigs.k8s.io/yaml v1.4.0
)

require (
	aead.dev/mem v0.2.0 // indirect
	aead.dev/minisign v0.3.0 // indirect
	aead.dev/mtls v0.2.1 // indirect
	cel.dev/expr v0.23.1 // indirect
	cloud.google.com/go v0.120.1 // indirect
	cloud.google.com/go/auth v0.16.0 // indirect
	cloud.google.com/go/auth/oauth2adapt v0.2.8 // indirect
	cloud.google.com/go/compute/metadata v0.6.0 // indirect
	cloud.google.com/go/container v1.42.4 // indirect
	cloud.google.com/go/longrunning v0.6.7 // indirect
	cloud.google.com/go/monitoring v1.24.2 // indirect
	cloud.google.com/go/trace v1.11.6 // indirect
	filippo.io/edwards25519 v1.1.0 // indirect
	github.com/Azure/azure-sdk-for-go/sdk/azcore v1.18.0 // indirect
	github.com/Azure/azure-sdk-for-go/sdk/azidentity v1.9.0 // indirect
	github.com/Azure/azure-sdk-for-go/sdk/internal v1.11.1 // indirect
	github.com/Azure/azure-sdk-for-go/sdk/storage/azblob v1.6.1 // indirect
	github.com/Azure/go-ntlmssp v0.0.0-20221128193559-754e69321358 // indirect
	github.com/AzureAD/microsoft-authentication-library-for-go v1.4.2 // indirect
	github.com/GoogleCloudPlatform/opentelemetry-operations-go/detectors/gcp v1.27.0 // indirect
	github.com/GoogleCloudPlatform/opentelemetry-operations-go/exporter/metric v0.51.0 // indirect
	github.com/GoogleCloudPlatform/opentelemetry-operations-go/internal/resourcemapping v0.51.0 // indirect
	github.com/IBM/sarama v1.45.1 // indirect
	github.com/Masterminds/goutils v1.1.1 // indirect
	github.com/Masterminds/semver v1.5.0 // indirect
	github.com/VividCortex/ewma v1.2.0 // indirect
	github.com/acarl005/stripansi v0.0.0-20180116102854-5a71ef0e047d // indirect
	github.com/alecthomas/participle v0.7.1 // indirect
	github.com/apache/thrift v0.21.0 // indirect
	github.com/asaskevich/govalidator v0.0.0-20230301143203-a9d515a09cc2 // indirect
	github.com/aws/aws-sdk-go v1.55.6 // indirect
	github.com/aymanbagabas/go-osc52/v2 v2.0.1 // indirect
	github.com/bazelbuild/buildtools v0.0.0-20240918101019-be1c24cc9a44 // indirect
	github.com/beevik/ntp v1.4.3 // indirect
	github.com/beorn7/perks v1.0.1 // indirect
	github.com/buger/jsonparser v1.1.1 // indirect
	github.com/census-instrumentation/opencensus-proto v0.4.1 // indirect
	github.com/cespare/xxhash/v2 v2.3.0 // indirect
	github.com/charmbracelet/bubbles v0.20.0 // indirect
	github.com/charmbracelet/bubbletea v1.3.4 // indirect
	github.com/charmbracelet/lipgloss v1.0.0 // indirect
	github.com/charmbracelet/x/ansi v0.8.0 // indirect
	github.com/charmbracelet/x/term v0.2.1 // indirect
	github.com/cheggaaa/pb v1.0.29 // indirect
	github.com/chigopher/pathlib v0.15.0 // indirect
	github.com/cncf/xds/go v0.0.0-20250326154945-ae57f3c0d45f // indirect
	github.com/coreos/go-oidc/v3 v3.14.1 // indirect
	github.com/coreos/go-semver v0.3.1 // indirect
	github.com/coreos/go-systemd/v22 v22.5.0 // indirect
	github.com/cosnicolaou/pbzip2 v1.0.5 // indirect
	github.com/cpuguy83/go-md2man/v2 v2.0.4 // indirect
	github.com/dchest/siphash v1.2.3 // indirect
	github.com/decred/dcrd/dcrec/secp256k1/v4 v4.4.0 // indirect
	github.com/docker/go-units v0.5.0 // indirect
	github.com/eapache/go-resiliency v1.7.0 // indirect
	github.com/eapache/go-xerial-snappy v0.0.0-20230731223053-c322873962e3 // indirect
	github.com/eapache/queue v1.1.0 // indirect
	github.com/eclipse/paho.mqtt.golang v1.5.0 // indirect
	github.com/elastic/go-elasticsearch/v7 v7.17.10 // indirect
	github.com/emirpasic/gods v1.12.0 // indirect
	github.com/envoyproxy/go-control-plane/envoy v1.32.4 // indirect
	github.com/envoyproxy/protoc-gen-validate v1.2.1 // indirect
	github.com/erikgeiser/coninput v0.0.0-20211004153227-1c3628e74d0f // indirect
	github.com/fatih/color v1.18.0 // indirect
	github.com/fatih/structs v1.1.0 // indirect
	github.com/felixge/fgprof v0.9.5 // indirect
	github.com/felixge/httpsnoop v1.0.4 // indirect
	github.com/fraugster/parquet-go v0.12.0 // indirect
	github.com/fsnotify/fsnotify v1.8.0 // indirect
	github.com/go-asn1-ber/asn1-ber v1.5.8-0.20250403174932-29230038a667 // indirect
	github.com/go-ini/ini v1.67.0 // indirect
	github.com/go-jose/go-jose/v4 v4.1.0 // indirect
	github.com/go-ldap/ldap/v3 v3.4.11 // indirect
	github.com/go-logr/logr v1.4.2 // indirect
	github.com/go-logr/stdr v1.2.2 // indirect
	github.com/go-ole/go-ole v1.3.0 // indirect
	github.com/go-openapi/analysis v0.23.0 // indirect
	github.com/go-openapi/errors v0.22.1 // indirect
	github.com/go-openapi/jsonpointer v0.21.1 // indirect
	github.com/go-openapi/jsonreference v0.21.0 // indirect
	github.com/go-openapi/loads v0.22.0 // indirect
	github.com/go-openapi/runtime v0.28.0 // indirect
	github.com/go-openapi/spec v0.21.0 // indirect
	github.com/go-openapi/strfmt v0.23.0 // indirect
	github.com/go-openapi/swag v0.23.1 // indirect
	github.com/go-openapi/validate v0.24.0 // indirect
	github.com/go-sql-driver/mysql v1.9.2 // indirect
	github.com/gobwas/httphead v0.1.0 // indirect
	github.com/gobwas/pool v0.2.1 // indirect
	github.com/gobwas/ws v1.4.0 // indirect
	github.com/goccy/go-json v0.10.5 // indirect
	github.com/gogo/protobuf v1.3.2 // indirect
	github.com/golang-jwt/jwt/v4 v4.5.2 // indirect
	github.com/golang-jwt/jwt/v5 v5.2.2 // indirect
	github.com/golang/glog v1.2.4 // indirect
	github.com/golang/snappy v1.0.0 // indirect
	github.com/gomodule/redigo v1.9.2 // indirect
	github.com/google/go-cmp v0.7.0 // indirect
	github.com/google/go-querystring v1.1.0 // indirect
	github.com/google/gofuzz v1.2.0 // indirect
	github.com/google/licenseclassifier v0.0.0-20210722185704-3043a050f148 // indirect
	github.com/google/pprof v0.0.0-20250422154841-e1f9c1950416 // indirect
	github.com/google/s2a-go v0.1.9 // indirect
	github.com/google/shlex v0.0.0-20191202100458-e7afc7fbc510 // indirect
	github.com/googleapis/enterprise-certificate-proxy v0.3.6 // indirect
	github.com/googleapis/gax-go/v2 v2.14.1 // indirect
	github.com/googleapis/gnostic v0.5.5 // indirect
	github.com/gorilla/websocket v1.5.3 // indirect
	github.com/hashicorp/errwrap v1.1.0 // indirect
	github.com/hashicorp/go-uuid v1.0.3 // indirect
	github.com/hashicorp/hcl v1.0.0 // indirect
	github.com/huandu/xstrings v1.5.0 // indirect
	github.com/iancoleman/orderedmap v0.0.0-20190318233801-ac98e3ecb4b0 // indirect
	github.com/iancoleman/strcase v0.3.0 // indirect
	github.com/imdario/mergo v0.3.11 // indirect
	github.com/inconshreveable/mousetrap v1.1.0 // indirect
	github.com/jackc/chunkreader/v2 v2.0.1 // indirect
	github.com/jackc/pgio v1.0.0 // indirect
	github.com/jackc/pgpassfile v1.0.0 // indirect
	github.com/jackc/pgproto3/v2 v2.3.2 // indirect
	github.com/jackc/pgservicefile v0.0.0-20221227161230-091c0ba34f0a // indirect
	github.com/jackc/puddle v1.3.0 // indirect
	github.com/jbenet/go-context v0.0.0-20150711004518-d14ea06fba99 // indirect
	github.com/jcgregorio/slog v0.0.0-20190423190439-e6f2d537f900 // indirect
	github.com/jcmturner/aescts/v2 v2.0.0 // indirect
	github.com/jcmturner/dnsutils/v2 v2.0.0 // indirect
	github.com/jcmturner/gofork v1.7.6 // indirect
	github.com/jcmturner/gokrb5/v8 v8.4.4 // indirect
	github.com/jcmturner/rpc/v2 v2.0.3 // indirect
	github.com/jedib0t/go-pretty/v6 v6.6.7 // indirect
	github.com/jessevdk/go-flags v1.6.1 // indirect
	github.com/jinzhu/copier v0.3.5 // indirect
	github.com/jmespath/go-jmespath v0.4.0 // indirect
	github.com/josharian/intern v1.0.0 // indirect
	github.com/json-iterator/go v1.1.12 // indirect
	github.com/juju/ratelimit v1.0.2 // indirect
	github.com/kevinburke/ssh_config v0.0.0-20190725054713-01f96b0aa0cd // indirect
	github.com/klauspost/compress v1.18.0 // indirect
	github.com/klauspost/cpuid/v2 v2.2.10 // indirect
	github.com/klauspost/filepathx v1.1.1 // indirect
	github.com/klauspost/pgzip v1.2.6 // indirect
	github.com/klauspost/readahead v1.4.0 // indirect
	github.com/klauspost/reedsolomon v1.12.4 // indirect
	github.com/kr/fs v0.1.0 // indirect
	github.com/kr/pretty v0.3.1 // indirect
	github.com/kylelemons/godebug v1.1.0 // indirect
	github.com/lestrrat-go/blackmagic v1.0.2 // indirect
	github.com/lestrrat-go/httpcc v1.0.1 // indirect
	github.com/lestrrat-go/httprc v1.0.6 // indirect
	github.com/lestrrat-go/iter v1.0.2 // indirect
	github.com/lestrrat-go/jwx/v2 v2.1.4 // indirect
	github.com/lestrrat-go/option v1.0.1 // indirect
	github.com/lib/pq v1.10.9 // indirect
	github.com/lithammer/shortuuid/v4 v4.2.0 // indirect
	github.com/lucasb-eyer/go-colorful v1.2.0 // indirect
	github.com/lufia/plan9stats v0.0.0-20250317134145-8bc96cf8fc35 // indirect
	github.com/magiconair/properties v1.8.7 // indirect
	github.com/mailru/easyjson v0.9.0 // indirect
	github.com/mattn/go-colorable v0.1.14 // indirect
	github.com/mattn/go-ieproxy v0.0.12 // indirect
	github.com/mattn/go-isatty v0.0.20 // indirect
	github.com/mattn/go-localereader v0.0.1 // indirect
	github.com/mattn/go-runewidth v0.0.16 // indirect
	github.com/matttproud/golang_protobuf_extensions v1.0.4 // indirect
	github.com/minio/cli v1.24.2 // indirect
	github.com/minio/colorjson v1.0.8 // indirect
	github.com/minio/console v1.7.7-0.20250905210349-2017f33b26e1 // indirect
	github.com/minio/crc64nvme v1.0.1 // indirect
	github.com/minio/csvparser v1.0.0 // indirect
	github.com/minio/dnscache v0.1.1 // indirect
	github.com/minio/dperf v0.6.3 // indirect
	github.com/minio/filepath v1.0.0 // indirect
	github.com/minio/highwayhash v1.0.3 // indirect
	github.com/minio/kms-go/kes v0.3.1 // indirect
	github.com/minio/kms-go/kms v0.5.1-0.20250225090116-4e64ce8d0f35 // indirect
	github.com/minio/madmin-go/v3 v3.0.109 // indirect
	github.com/minio/mc v0.0.0-20250313080218-cf909e1063a9 // indirect
	github.com/minio/md5-simd v1.1.2 // indirect
	github.com/minio/minio v0.0.0-20251203081311-27742d469462 // indirect
	github.com/minio/minio-go/v7 v7.0.91 // indirect
	github.com/minio/mux v1.9.2 // indirect
	github.com/minio/pkg/v3 v3.1.3 // indirect
	github.com/minio/selfupdate v0.6.0 // indirect
	github.com/minio/simdjson-go v0.4.5 // indirect
	github.com/minio/sio v0.4.1 // indirect
	github.com/minio/websocket v1.6.0 // indirect
	github.com/minio/xxml v0.0.3 // indirect
	github.com/minio/zipindex v0.4.0 // indirect
	github.com/mitchellh/copystructure v1.2.0 // indirect
	github.com/mitchellh/go-homedir v1.1.0 // indirect
	github.com/mitchellh/mapstructure v1.5.0 // indirect
	github.com/mitchellh/reflectwalk v1.0.2 // indirect
	github.com/moby/spdystream v0.5.0 // indirect
	github.com/modern-go/concurrent v0.0.0-20180306012644-bacd9c7ef1dd // indirect
	github.com/modern-go/reflect2 v1.0.2 // indirect
	github.com/muesli/ansi v0.0.0-20230316100256-276c6243b2f6 // indirect
	github.com/muesli/cancelreader v0.2.2 // indirect
	github.com/muesli/reflow v0.3.0 // indirect
	github.com/muesli/termenv v0.16.0 // indirect
	github.com/munnerz/goautoneg v0.0.0-20191010083416-a7dc8b61c822 // indirect
	github.com/nats-io/nats.go v1.41.2 // indirect
	github.com/nats-io/nkeys v0.4.11 // indirect
	github.com/nats-io/nuid v1.0.1 // indirect
	github.com/nats-io/stan.go v0.10.4 // indirect
	github.com/ncw/directio v1.0.5 // indirect
	github.com/nsqio/go-nsq v1.1.0 // indirect
	github.com/nxadm/tail v1.4.5 // indirect
	github.com/oklog/ulid v1.3.1 // indirect
	github.com/olekukonko/tablewriter v0.0.5 // indirect
	github.com/onsi/ginkgo v1.14.2 // indirect
	github.com/onsi/gomega v1.35.1 // indirect
	github.com/pelletier/go-toml/v2 v2.2.3 // indirect
	github.com/philhofer/fwd v1.2.0 // indirect
	github.com/pierrec/lz4/v4 v4.1.22 // indirect
	github.com/pkg/browser v0.0.0-20240102092130-5ac0b6a4141c // indirect
	github.com/pkg/errors v0.9.1 // indirect
	github.com/pkg/sftp v1.13.9 // indirect
	github.com/pkg/xattr v0.4.10 // indirect
	github.com/planetscale/vtprotobuf v0.6.1-0.20240319094008-0393e58bdf10 // indirect
	github.com/posener/complete v1.2.3 // indirect
	github.com/power-devops/perfstat v0.0.0-20240221224432-82ca36839d55 // indirect
	github.com/prometheus/client_model v0.6.2 // indirect
	github.com/prometheus/common v0.63.0 // indirect
	github.com/prometheus/procfs v0.16.1 // indirect
	github.com/prometheus/prom2json v1.4.2 // indirect
	github.com/prometheus/prometheus v0.303.0 // indirect
	github.com/puzpuzpuz/xsync/v3 v3.5.1 // indirect
	github.com/rabbitmq/amqp091-go v1.10.0 // indirect
	github.com/rcrowley/go-metrics v0.0.0-20250401214520-65e299d6c5c9 // indirect
	github.com/rivo/uniseg v0.4.7 // indirect
	github.com/rjeczalik/notify v0.9.3 // indirect
	github.com/robertkrimen/otto v0.0.0-20200922221731-ef014fd054ac // indirect
	github.com/rogpeppe/go-internal v1.13.1 // indirect
	github.com/rs/cors v1.11.1 // indirect
	github.com/rs/xid v1.6.0 // indirect
	github.com/rs/zerolog v1.33.0 // indirect
	github.com/russross/blackfriday/v2 v2.1.0 // indirect
	github.com/safchain/ethtool v0.5.10 // indirect
	github.com/santhosh-tekuri/jsonschema/v5 v5.1.1 // indirect
	github.com/secure-io/sio-go v0.3.1 // indirect
	github.com/segmentio/asm v1.2.0 // indirect
	github.com/sendgrid/rest v2.6.9+incompatible // indirect
	github.com/sergi/go-diff v1.3.1 // indirect
	github.com/shirou/gopsutil/v3 v3.24.5 // indirect
	github.com/shoenig/go-m1cpu v0.1.6 // indirect
	github.com/spf13/afero v1.11.0 // indirect
	github.com/spf13/cast v1.7.0 // indirect
	github.com/spf13/jwalterweatherman v1.1.0 // indirect
	github.com/spf13/pflag v1.0.5 // indirect
	github.com/spf13/viper v1.19.0 // indirect
	github.com/spiffe/go-spiffe/v2 v2.5.0 // indirect
	github.com/src-d/gcfg v1.4.0 // indirect
	github.com/stretchr/objx v0.5.2 // indirect
	github.com/subosito/gotenv v1.6.0 // indirect
	github.com/tidwall/gjson v1.18.0 // indirect
	github.com/tidwall/match v1.1.1 // indirect
	github.com/tidwall/pretty v1.2.1 // indirect
	github.com/tinylib/msgp v1.4.0 // indirect
	github.com/tklauser/go-sysconf v0.3.15 // indirect
	github.com/tklauser/numcpus v0.10.0 // indirect
	github.com/valyala/bytebufferpool v1.0.0 // indirect
	github.com/vbauerster/mpb/v8 v8.9.3 // indirect
	github.com/xanzy/ssh-agent v0.2.1 // indirect
	github.com/xdg/scram v1.0.5 // indirect
	github.com/xdg/stringprep v1.0.3 // indirect
	github.com/xeipuuv/gojsonpointer v0.0.0-20180127040702-4e3ac2762d5f // indirect
	github.com/xeipuuv/gojsonreference v0.0.0-20180127040603-bd5ef7bd5415 // indirect
	github.com/xrash/smetrics v0.0.0-20201216005158-039620a65673 // indirect
	github.com/zeebo/errs v1.4.0 // indirect
	github.com/zeebo/xxh3 v1.0.2 // indirect
	go.etcd.io/etcd/api/v3 v3.5.21 // indirect
	go.etcd.io/etcd/client/pkg/v3 v3.5.21 // indirect
	go.etcd.io/etcd/client/v3 v3.5.21 // indirect
	go.mongodb.org/mongo-driver v1.17.3 // indirect
	go.opentelemetry.io/auto/sdk v1.1.0 // indirect
	go.opentelemetry.io/contrib/detectors/gcp v1.35.0 // indirect
	go.opentelemetry.io/contrib/instrumentation/google.golang.org/grpc/otelgrpc v0.60.0 // indirect
	go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp v0.60.0 // indirect
	go.opentelemetry.io/otel v1.35.0 // indirect
	go.opentelemetry.io/otel/metric v1.35.0 // indirect
	go.opentelemetry.io/otel/sdk v1.35.0 // indirect
	go.opentelemetry.io/otel/sdk/metric v1.35.0 // indirect
	go.opentelemetry.io/otel/trace v1.35.0 // indirect
	go.uber.org/atomic v1.11.0 // indirect
	go.uber.org/multierr v1.11.0 // indirect
	go.uber.org/zap v1.27.0 // indirect
	goftp.io/server/v2 v2.0.1 // indirect
	golang.org/x/crypto v0.41.0 // indirect
	golang.org/x/exp v0.0.0-20240719175910-8a7402abbf56 // indirect
	golang.org/x/mod v0.27.0 // indirect
	golang.org/x/sys v0.35.0 // indirect
	golang.org/x/text v0.28.0 // indirect
	golang.org/x/tools/go/vcs v0.1.0-deprecated // indirect
	google.golang.org/genproto/googleapis/api v0.0.0-20250422160041-2d3770c4ea7f // indirect
	google.golang.org/genproto/googleapis/rpc v0.0.0-20250422160041-2d3770c4ea7f // indirect
	gopkg.in/cenkalti/backoff.v1 v1.1.0 // indirect
	gopkg.in/inf.v0 v0.9.1 // indirect
	gopkg.in/ini.v1 v1.67.0 // indirect
	gopkg.in/sourcemap.v1 v1.0.5 // indirect
	gopkg.in/src-d/go-billy.v4 v4.3.2 // indirect
	gopkg.in/src-d/go-git.v4 v4.13.1 // indirect
	gopkg.in/warnings.v0 v0.1.2 // indirect
	gopkg.in/yaml.v3 v3.0.1 // indirect
	k8s.io/klog/v2 v2.130.1 // indirect
	k8s.io/utils v0.0.0-20241104100929-3ea5e8cea738 // indirect
	sigs.k8s.io/structured-merge-diff/v4 v4.4.2 // indirect
)

go 1.25.4

tool github.com/minio/minio
