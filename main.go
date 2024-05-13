package main

import (
	"context"
	"fmt"
	"github.com/dungnh3/trustwallet-assignment/internal/parser"
	"github.com/dungnh3/trustwallet-assignment/internal/repositories"
)

func main() {
	ctx := context.Background()
	host := "https://cloudflare-eth.com"
	repo := repositories.New()
	invoker := parser.New(ctx, host, repo)

	address := "0x12ebe0a"

	currentBlock := invoker.GetCurrentBlock()
	fmt.Printf("Current block is: %d\n", currentBlock)

	transactions := invoker.GetTransactions(address)
	fmt.Println(transactions)

	isSubscribed := invoker.Subscribe(address)
	fmt.Println(isSubscribed)
}
