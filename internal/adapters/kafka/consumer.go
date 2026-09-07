package kafka

import (
	"context"
	"log"
	"ticket-service/config"

	"github.com/IBM/sarama"
)

type MessageHandler func(ctx context.Context, key, value []byte) error

type Consumer struct {
	group   sarama.ConsumerGroup
	topic   string
	handler MessageHandler
}

func NewConsumer(cfg config.Kafka, handler MessageHandler) (*Consumer, error) {
	saramaCfg := sarama.NewConfig()
	saramaCfg.Consumer.Offsets.Initial = sarama.OffsetNewest
	saramaCfg.Consumer.Group.Rebalance.GroupStrategies = []sarama.BalanceStrategy{
		sarama.NewBalanceStrategyRoundRobin(),
	}
	saramaCfg.Version = sarama.V2_1_0_0

	group, err := sarama.NewConsumerGroup(cfg.Brokers, cfg.ConsumerGroup, saramaCfg)
	if err != nil {
		return nil, err
	}

	log.Printf("Kafka consumer group=%s connected to brokers: %v", cfg.ConsumerGroup, cfg.Brokers)
	return &Consumer{group: group, topic: cfg.ConsumerTopic, handler: handler}, nil
}

func (c *Consumer) Start(ctx context.Context) {
	h := &consumerGroupHandler{handler: c.handler}
	for {
		if err := c.group.Consume(ctx, []string{c.topic}, h); err != nil {
			log.Printf("kafka consumer error: %v", err)
		}
		if ctx.Err() != nil {
			return
		}
	}
}

func (c *Consumer) Close() error {
	return c.group.Close()
}

type consumerGroupHandler struct {
	handler MessageHandler
}

func (h *consumerGroupHandler) Setup(_ sarama.ConsumerGroupSession) error   { return nil }
func (h *consumerGroupHandler) Cleanup(_ sarama.ConsumerGroupSession) error { return nil }

func (h *consumerGroupHandler) ConsumeClaim(session sarama.ConsumerGroupSession, claim sarama.ConsumerGroupClaim) error {
	for msg := range claim.Messages() {
		if err := h.handler(session.Context(), msg.Key, msg.Value); err != nil {
			log.Printf("message handling error: %v", err)
		} else {
			session.MarkMessage(msg, "")
		}
	}
	return nil
}
