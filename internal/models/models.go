package models

import "time"

type BlockInfo struct {
	BlockAddress             string `json:"block_address,omitempty"`
	Count                    int    `json:"count,omitempty"`
	LatestTransactionAddress string `json:"latest_transaction_address,omitempty"`
}

type BlockTransaction struct {
	ID                 int       `json:"id"`
	BlockAddress       string    `json:"block_address,omitempty"`
	TransactionAddress string    `json:"transaction_address,omitempty"`
	CreatedAt          time.Time `json:"created_at"`
}
