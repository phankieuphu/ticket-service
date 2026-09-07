package main

import (
	"context"
	"ticket-service/internal/application"
)

func main() {
	ctx := context.Background()
	application.AccountApplication(ctx)

}
