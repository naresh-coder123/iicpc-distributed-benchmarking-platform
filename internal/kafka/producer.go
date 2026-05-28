package kafka

import (
	"context"
	"fmt"
	"strings"
	"time"

	kafkago "github.com/segmentio/kafka-go"
)

type Producer struct {
	w *kafkago.Writer
}

type ProducerConfig struct {
	Brokers []string
	Topic   string
}

func NewProducer(cfg ProducerConfig) (*Producer, error) {
	if len(cfg.Brokers) == 0 {
		return nil, fmt.Errorf("kafka brokers required")
	}
	if cfg.Topic == "" {
		cfg.Topic = "metrics"
	}

	w := &kafkago.Writer{
		Addr:         kafkago.TCP(cfg.Brokers...),
		Topic:        cfg.Topic,
		Balancer:     &kafkago.Hash{},
		BatchTimeout: 10 * time.Millisecond,
		Async:        true,
	}

	return &Producer{w: w}, nil
}

func BrokersFromCSV(s string) []string {
	parts := strings.Split(s, ",")
	var out []string
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p != "" {
			out = append(out, p)
		}
	}
	return out
}

func (p *Producer) Close() error {
	return p.w.Close()
}

func (p *Producer) Publish(ctx context.Context, key []byte, value []byte) error {
	return p.w.WriteMessages(ctx, kafkago.Message{
		Key:   key,
		Value: value,
	})
}
