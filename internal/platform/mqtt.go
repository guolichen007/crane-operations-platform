package platform

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/eclipse/paho.golang/autopaho"
	"github.com/eclipse/paho.golang/paho"
	contract "github.com/guolichen007/crane-operations-platform/contracts/go"
	"math"
	"net/url"
	"sync/atomic"
	"time"
)

type Broker struct{ CM *autopaho.ConnectionManager }

var MQTTReconnects atomic.Int64

// R9 A3 broker-connection monitoring (process-wide, read by /metrics and the broker preflight):
// failed connection attempts (TCP / TLS / CONNACK refusal), the unix time the connection was last
// lost (0 = never) and whether it is currently down. Never logs credentials or payloads.
var (
	MQTTConnectFailures atomic.Int64
	MQTTDownSince       atomic.Int64
	mqttLastError       atomic.Value // string: error class only
)

// MQTTLastErrorClass is the class of the last failed connection attempt ("" if none).
func MQTTLastErrorClass() string {
	if v, ok := mqttLastError.Load().(string); ok {
		return v
	}
	return ""
}

// MQTTDownSeconds is how long the broker connection has been down (0 when up).
func MQTTDownSeconds(now time.Time) int64 {
	if since := MQTTDownSince.Load(); since > 0 {
		return max(int64(0), now.Unix()-since)
	}
	return 0
}

func errorClass(err error) string {
	var refused *autopaho.ConnackError
	switch {
	case errors.As(err, &refused):
		return "connack_refused"
	case errors.Is(err, context.DeadlineExceeded):
		return "timeout"
	default:
		var tlsErr interface{ Timeout() bool }
		if errors.As(err, &tlsErr) && tlsErr.Timeout() {
			return "timeout"
		}
		return "connect_failed"
	}
}

var ErrMessageExpired = errors.New("message expired before publish")

func Connect(ctx context.Context, c Config, id string, subscriptions []string, will func() contract.Envelope, receive func(string, []byte, time.Duration) error, up func(), down func()) (*Broker, error) {
	u, e := url.Parse(c.MQTTURL)
	if e != nil || u.Scheme != "tls" {
		return nil, fmt.Errorf("MQTT requires tls URL")
	}
	tlsCfg, e := TLS(c.CertDir, id)
	if e != nil {
		return nil, e
	}
	cfg := autopaho.ClientConfig{ServerUrls: []*url.URL{u}, TlsCfg: tlsCfg, KeepAlive: 10, CleanStartOnInitialConnection: false, SessionExpiryInterval: 3600, ConnectTimeout: Timeout(), ReconnectBackoff: func(n int) time.Duration {
		if n > 6 {
			n = 6
		}
		return time.Duration(1<<n) * time.Second
	}, ClientConfig: paho.ClientConfig{ClientID: id, EnableManualAcknowledgment: true, OnPublishReceived: []func(paho.PublishReceived) (bool, error){func(p paho.PublishReceived) (bool, error) {
		remaining := time.Duration(-1)
		if p.Packet.Properties != nil && p.Packet.Properties.MessageExpiry != nil {
			remaining = time.Duration(*p.Packet.Properties.MessageExpiry) * time.Second
		}
		err := receive(p.Packet.Topic, p.Packet.Payload, remaining)
		if err == nil {
			err = p.Client.Ack(p.Packet)
		}
		return true, err
	}}}}
	if will != nil {
		delay := uint32(c.WillDelay)
		cfg.WillProperties = &paho.WillProperties{WillDelayInterval: &delay}
		cfg.ConnectPacketBuilder = func(packet *paho.Connect, _ *url.URL) (*paho.Connect, error) {
			m := will()
			if err := contract.Validate(m); err != nil {
				return nil, err
			}
			payload, _ := json.Marshal(m)
			topic, _ := contract.Topic(m.TenantId, m.ProjectId, m.DeviceId, "shadow/reported")
			packet.WillMessage = &paho.WillMessage{Topic: topic, QoS: 1, Retain: true, Payload: payload}
			packet.WillProperties = &paho.WillProperties{WillDelayInterval: &delay}
			return packet, nil
		}
	}
	cfg.OnConnectError = func(err error) {
		MQTTConnectFailures.Add(1)
		mqttLastError.Store(errorClass(err))
		MQTTDownSince.CompareAndSwap(0, time.Now().Unix())
	}
	cfg.OnConnectionDown = func() bool {
		MQTTDownSince.CompareAndSwap(0, time.Now().Unix())
		if down != nil {
			down()
		}
		return true
	}
	var connections atomic.Int64
	cfg.OnConnectionUp = func(cm *autopaho.ConnectionManager, _ *paho.Connack) {
		MQTTDownSince.Store(0)
		if connections.Add(1) > 1 {
			MQTTReconnects.Add(1)
		}
		go func() {
			timeout, cancel := context.WithTimeout(ctx, Timeout())
			defer cancel()
			opts := []paho.SubscribeOptions{}
			for _, t := range subscriptions {
				opts = append(opts, paho.SubscribeOptions{Topic: t, QoS: 1})
			}
			if len(opts) > 0 {
				ack, err := cm.Subscribe(timeout, &paho.Subscribe{Subscriptions: opts})
				if err != nil {
					return
				}
				for _, reason := range ack.Reasons {
					if reason >= 128 {
						return
					}
				}
			}
			if up != nil {
				up()
			}
		}()
	}
	cm, e := autopaho.NewConnection(ctx, cfg)
	if e != nil {
		return nil, e
	}
	return &Broker{CM: cm}, nil
}
func (b *Broker) Publish(ctx context.Context, m contract.Envelope, suffix string) error {
	topic, e := contract.Topic(m.TenantId, m.ProjectId, m.DeviceId, suffix)
	if e != nil {
		return e
	}
	payload, e := json.Marshal(m)
	if e != nil {
		return e
	}
	if _, e = contract.Decode(topic, payload); e != nil {
		return e
	}
	properties, e := PublishProperties(m, time.Now())
	if e != nil {
		return e
	}
	ctx, cancel := context.WithTimeout(ctx, Timeout())
	defer cancel()
	_, e = b.CM.Publish(ctx, &paho.Publish{Topic: topic, QoS: contract.Topics[suffix], Retain: suffix == "shadow/reported", Payload: payload, Properties: properties})
	return e
}
func PublishProperties(m contract.Envelope, now time.Time) (*paho.PublishProperties, error) {
	if m.Kind != "WATCH_START" && m.Kind != "WATCH_RENEW" && m.Kind != "WATCH_STOP" {
		return nil, nil
	}
	v := contract.Payload[contract.WatchCommandPayload](m)
	remaining := v.ExpiresAt.Sub(now).Seconds()
	if remaining <= 0 {
		return nil, ErrMessageExpired
	}
	expiry := uint32(math.Ceil(remaining))
	if expiry > uint32(v.TtlSeconds) {
		expiry = uint32(v.TtlSeconds)
	}
	return &paho.PublishProperties{MessageExpiry: &expiry}, nil
}
