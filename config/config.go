package config

import (
	"os"
	"strconv"
	"time"
)

type Config struct {
	AWS
	Database
	API
	Kafka
	Redis
	JWT
}

type AWS struct {
	Region string
}

type Database struct {
	Host            string
	Port            int
	Username        string
	Password        string
	Database        string
	MaxOpenConns    int
	MaxIdleConns    int
	ConnMaxLifetime time.Duration
	Driver          string
}

type API struct {
	Port         string
	ReadTimeout  time.Duration
	WriteTimeout time.Duration
}

type Kafka struct {
	Brokers       []string
	ProducerTopic string
	ConsumerTopic string
	ConsumerGroup string
}

type Redis struct {
	Host     string
	Port     string
	Password string
	DB       int
}

func (r Redis) Addr() string {
	return r.Host + ":" + r.Port
}

type JWT struct {
	Secret    string
	ExpiresIn time.Duration
}

func LoadConfig() *Config {
	return &Config{
		AWS: AWS{
			Region: GetEnv("AWS_REGION", "ap-southeast-1"),
		},
		Database: Database{
			Host:            GetEnv("DB_HOST", "localhost"),
			Port:            getEnvInt("DB_PORT", 3306),
			Username:        GetEnv("DB_USERNAME", "root"),
			Password:        GetEnv("DB_PASSWORD", ""),
			Database:        GetEnv("DB_DATABASE", "booking_system"),
			MaxOpenConns:    getEnvInt("DB_MAX_OPEN_CONNS", 10),
			MaxIdleConns:    getEnvInt("DB_MAX_IDLE_CONNS", 5),
			ConnMaxLifetime: time.Duration(getEnvInt("DB_CONN_MAX_LIFETIME_SEC", 300)) * time.Second,
			Driver:          GetEnv("DB_DRIVER", "mysql"),
		},
		API: API{
			Port:         GetEnv("API_PORT", "8080"),
			ReadTimeout:  time.Duration(getEnvInt("API_READ_TIMEOUT_SEC", 30)) * time.Second,
			WriteTimeout: time.Duration(getEnvInt("API_WRITE_TIMEOUT_SEC", 30)) * time.Second,
		},
		Kafka: Kafka{
			Brokers:       []string{GetEnv("KAFKA_BROKERS", "localhost:9092")},
			ProducerTopic: GetEnv("KAFKA_PRODUCER_TOPIC", "account.events"),
			ConsumerTopic: GetEnv("KAFKA_CONSUMER_TOPIC", "account.events"),
			ConsumerGroup: GetEnv("KAFKA_CONSUMER_GROUP", "user-service"),
		},
		Redis: Redis{
			Host:     GetEnv("REDIS_HOST", "localhost"),
			Port:     GetEnv("REDIS_PORT", "6379"),
			Password: GetEnv("REDIS_PASSWORD", ""),
			DB:       getEnvInt("REDIS_DB", 0),
		},
		JWT: JWT{
			Secret:    GetEnv("JWT_SECRET", "change-me-in-production"),
			ExpiresIn: time.Duration(getEnvInt("JWT_EXPIRES_IN_MIN", 60)) * time.Minute,
		},
	}
}

func GetEnv(key string, defaultValue string) string {
	result := os.Getenv(key)
	if result != "" {
		return result
	}
	return defaultValue
}

func getEnvInt(key string, defaultValue int) int {
	val := os.Getenv(key)
	if val == "" {
		return defaultValue
	}
	n, err := strconv.Atoi(val)
	if err != nil {
		return defaultValue
	}
	return n
}
