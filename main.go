package main

import (
	"context"
	"fmt"
	"github.com/dungnh3/trustwallet-assignment/parser"
	"github.com/dungnh3/trustwallet-assignment/repositories"
)

func main() {
	ctx := context.Background()
	host := "https://cloudflare-eth.com"
	repo := repositories.New()
	invoker := parser.New(ctx, host, repo)

	//currentBlock := invoker.GetCurrentBlock()
	//fmt.Printf("Current block is: %d\n", currentBlock)

	address := "0x12ebe0a"
	transactions := invoker.Subscribe(address)
	fmt.Println(transactions)
}
