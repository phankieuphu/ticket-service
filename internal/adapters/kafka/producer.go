package kafka

import (
	"context"
	"log"
	"ticket-service/config"

	"github.com/IBM/sarama"
)

type Producer struct {
	producer sarama.SyncProducer
}

func NewProducer(cfg config.Kafka) (*Producer, error) {
	saramaCfg := sarama.NewConfig()
	saramaCfg.Producer.Return.Successes = true
	saramaCfg.Producer.Return.Errors = true
	saramaCfg.Producer.RequiredAcks = sarama.WaitForAll
	saramaCfg.Producer.Retry.Max = 3

	p, err := sarama.NewSyncProducer(cfg.Brokers, saramaCfg)
	if err != nil {
		return nil, err
	}

	log.Printf("Kafka producer connected to brokers: %v", cfg.Brokers)
	return &Producer{producer: p}, nil
}

func (p *Producer) Publish(_ context.Context, topic, key string, payload []byte) error {
	msg := &sarama.ProducerMessage{
		Topic: topic,
		Key:   sarama.StringEncoder(key),
		Value: sarama.ByteEncoder(payload),
	}
	partition, offset, err := p.producer.SendMessage(msg)
	if err != nil {
		return err
	}
	log.Printf("published to topic=%s partition=%d offset=%d", topic, partition, offset)
	return nil
}

func (p *Producer) Close() error {
	return p.producer.Close()
}
