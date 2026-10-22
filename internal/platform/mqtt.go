package platform

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/eclipse/paho.golang/autopaho"
	"github.com/eclipse/paho.golang/paho"
	contract "github.com/guolichen007/crane-operations-platform/contracts/go"
	"net/url"
	"time"
)

type Broker struct{ CM *autopaho.ConnectionManager }

func Connect(ctx context.Context, c Config, id string, subscriptions []string, will *contract.Message, receive func(string, []byte) error, up func(), down func()) (*Broker, error) {
	u, e := url.Parse(c.MQTTURL)
	if e != nil || u.Scheme != "tls" {
		return nil, fmt.Errorf("MQTT requires tls URL")
	}
	tlsCfg, e := TLS(c.CertDir, id)
	if e != nil {
		return nil, e
	}
	cfg := autopaho.ClientConfig{ServerUrls: []*url.URL{u}, TlsCfg: tlsCfg, KeepAlive: 300, CleanStartOnInitialConnection: true, SessionExpiryInterval: 3600, ConnectTimeout: Timeout(), ReconnectBackoff: func(n int) time.Duration {
		if n > 6 {
			n = 6
		}
		return time.Duration(1<<n) * time.Second
	}, ClientConfig: paho.ClientConfig{ClientID: id, EnableManualAcknowledgment: true, OnPublishReceived: []func(paho.PublishReceived) (bool, error){func(p paho.PublishReceived) (bool, error) {
		err := receive(p.Packet.Topic, p.Packet.Payload)
		if err == nil {
			err = p.Client.Ack(p.Packet)
		}
		return true, err
	}}}}
	if will != nil {
		payload, _ := json.Marshal(will)
		topic, _ := contract.Topic(will.TenantId, will.ProjectId, will.DeviceId, "shadow/reported")
		cfg.WillMessage = &paho.WillMessage{Topic: topic, QoS: 1, Retain: true, Payload: payload}
	}
	cfg.OnConnectionDown = func() bool {
		if down != nil {
			down()
		}
		return true
	}
	cfg.OnConnectionUp = func(cm *autopaho.ConnectionManager, _ *paho.Connack) {
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
func (b *Broker) Publish(ctx context.Context, m contract.Message, suffix string) error {
	topic, e := contract.Topic(m.TenantId, m.ProjectId, m.DeviceId, suffix)
	if e != nil {
		return e
	}
	payload, e := json.Marshal(m)
	if e != nil {
		return e
	}
	ctx, cancel := context.WithTimeout(ctx, Timeout())
	defer cancel()
	_, e = b.CM.Publish(ctx, &paho.Publish{Topic: topic, QoS: contract.Topics[suffix], Retain: suffix == "shadow/reported", Payload: payload})
	return e
}
