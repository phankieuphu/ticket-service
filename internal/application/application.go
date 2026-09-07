package application

import (
	"context"
	"log"
	"ticket-service/config"
	"ticket-service/internal/adapters/cache"
	database_provider "ticket-service/internal/adapters/database/provider"
	"ticket-service/internal/adapters/kafka"

	ginhttp "ticket-service/internal/adapters/http"

	"github.com/joho/godotenv"
)

type Application struct {
}

func AccountApplication(ctx context.Context) {
	godotenv.Load()
	cfg := config.LoadConfig()

	// database
	database, err := database_provider.NewMySQLClient(*cfg)
	if err != nil {
		log.Fatalf("failed to init database: %v", err)
	}
	_ = database

	// Redis cache
	redisCache, err := cache.NewRedisCache(cfg.Redis)
	if err != nil {
		log.Fatalf("failed to init Redis: %v", err)
	}
	_ = redisCache

	// Kafka producer
	kafkaProducer, err := kafka.NewProducer(cfg.Kafka)
	if err != nil {
		log.Fatalf("failed to init Kafka producer: %v", err)
	}
	defer kafkaProducer.Close()

	// Kafka consumer
	kafkaConsumer, err := kafka.NewConsumer(cfg.Kafka, func(ctx context.Context, key, value []byte) error {
		log.Printf("kafka message received key=%s value=%s", key, value)
		return nil
	})
	if err != nil {
		log.Fatalf("failed to init Kafka consumer: %v", err)
	}
	defer kafkaConsumer.Close()

	// HTTP server (gin)
	httpServer := ginhttp.NewServer(cfg.API)

	log.Println("Ticket application start")

	go kafkaConsumer.Start(ctx)
	httpServer.Start()
}
