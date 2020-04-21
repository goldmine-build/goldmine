// Package pubsubsource implements source.Source using Google Cloud PubSub.
package pubsubsource

import (
	"context"
	"encoding/json"
	"fmt"
	"os"

	"cloud.google.com/go/pubsub"
	"go.skia.org/infra/go/metrics2"
	"go.skia.org/infra/go/skerr"
	"go.skia.org/infra/go/sklog"
	"go.skia.org/infra/machine/go/common"
	"go.skia.org/infra/machine/go/machine"
	"go.skia.org/infra/machine/go/machine/source"
	"go.skia.org/infra/machine/go/machineserver/config"
)

const (
	machineEventChannelSize = 100

	// maxParallelReceives is the number of Go routines we want to run.
	maxParallelReceives = 10

	// subscriptionSuffix is the name we append to a topic name to build a
	// subscription name.
	subscriptionSuffix = "-prod"
)

// Source implements source.Source.
type Source struct {
	sub                        *pubsub.Subscription
	started                    bool // Start should only be called once.
	eventsReceivedCounter      metrics2.Counter
	eventsFailedToParseCounter metrics2.Counter
}

// New returns a new *Source.
func New(ctx context.Context, local bool, instanceConfig config.InstanceConfig) (*Source, error) {

	pubsubClient, topic, err := common.NewPubSubClient(ctx, local, instanceConfig)
	if err != nil {
		return nil, skerr.Wrap(err)
	}
	sklog.Infof("pubsub Source started for topic: %q", topic.String())

	// When running in production we have every instance use the same topic name so that
	// they load-balance pulling items from the topic.
	subName := instanceConfig.Source.Topic + subscriptionSuffix
	if local {
		// When running locally create a new topic for every host.
		hostname, err := os.Hostname()
		if err != nil {
			return nil, skerr.Wrapf(err, "Failed to get hostname.")
		}
		subName = fmt.Sprintf("%s-%s", instanceConfig.Source.Topic, hostname)
	}
	sub := pubsubClient.Subscription(subName)
	ok, err := sub.Exists(ctx)
	if err != nil {
		return nil, skerr.Wrapf(err, "Failed checking subscription existence: %q", subName)
	}
	if !ok {
		sub, err = pubsubClient.CreateSubscription(ctx, subName, pubsub.SubscriptionConfig{
			Topic: topic,
		})
		if err != nil {
			return nil, fmt.Errorf("Failed creating subscription: %s", err)
		}
	}
	sklog.Infof("Subsciption: %q", sub.String())

	// How many Go routines should be processing messages.
	sub.ReceiveSettings.MaxOutstandingMessages = maxParallelReceives
	sub.ReceiveSettings.NumGoroutines = maxParallelReceives

	return &Source{
		sub:                        sub,
		eventsReceivedCounter:      metrics2.GetCounter("machineserver_pubsubsource_events_received"),
		eventsFailedToParseCounter: metrics2.GetCounter("machineserver_pubsubsource_events_failed_to_parse"),
	}, nil
}

// Start implements source.Source.
func (s *Source) Start(ctx context.Context) (<-chan machine.Event, error) {
	if s.started {
		return nil, skerr.Fmt("Start can only be called once.")
	}
	s.started = true
	ch := make(chan machine.Event, machineEventChannelSize)
	go func() {
		for {
			if ctx.Err() != nil {
				close(ch)
				return
			}
			err := s.sub.Receive(ctx, func(ctx context.Context, msg *pubsub.Message) {
				s.eventsReceivedCounter.Inc(1)
				// Log to stdout as raw JSON in a single line for StackDriver
				// structured logging.
				fmt.Println(string(msg.Data))
				msg.Ack()
				var event machine.Event
				if err := json.Unmarshal(msg.Data, &event); err != nil {
					sklog.Errorf("Received invalid pubsub event data: %s", err)
					s.eventsFailedToParseCounter.Inc(1)
					return
				}
				ch <- event
			})
			if err != nil {
				sklog.Errorf("Failed receiving pubsub message: %s", err)
			}
		}
	}()
	return ch, nil
}

// Afirm that we implement the interface.
var _ source.Source = (*Source)(nil)
