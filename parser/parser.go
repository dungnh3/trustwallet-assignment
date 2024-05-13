package parser

import (
	"context"
	"errors"
	"fmt"
	"github.com/dungnh3/trustwallet-assignment/models"
	"github.com/dungnh3/trustwallet-assignment/repositories"
	"github.com/dungnh3/trustwallet-assignment/rest"
	"github.com/dungnh3/trustwallet-assignment/utils"
	"github.com/google/uuid"
	"go.uber.org/zap"
	"time"
)

type Parser interface {
	GetCurrentBlock() int
	Subscribe(address string) bool
	GetTransactions(address string) []Transaction
}

type Invoker struct {
	ctx      context.Context
	host     string
	jsonrpc  string
	cli      *rest.Rest
	logger   *zap.Logger
	repo     repositories.Repository
	interval time.Duration
}

func New(ctx context.Context, host string, repo repositories.Repository) *Invoker {
	cli := rest.New().Base(host)
	logger, _ := zap.NewProduction()
	return &Invoker{
		jsonrpc:  "2.0",
		ctx:      ctx,
		host:     host,
		repo:     repo,
		cli:      cli,
		logger:   logger,
		interval: 5 * time.Second,
	}
}

func (s *Invoker) GetCurrentBlock() int {
	request := map[string]interface{}{
		"jsonrpc": s.jsonrpc,
		"method":  "eth_blockNumber",
		"params":  nil,
		"id":      uuid.New().ID(),
	}
	var failureRaw rest.Raw
	var out BlockNumber
	_, err := s.cli.SetContext(s.ctx).Post("").
		SetHeader("Content-Type", "application/json").
		BodyJSON(&request).Receive(&out, &failureRaw)
	if err != nil {
		s.logger.Error("failed to execute request", zap.Error(err))
		return 0
	}
	if failureRaw != nil {
		s.logger.Error("failed to fetch current block", zap.ByteString("raw", failureRaw))
		return 0
	}
	return utils.ConvertHexToDec(out.Result)
}

func (s *Invoker) Subscribe(address string) bool {
	go func() {
		ticker := time.NewTicker(time.Millisecond)
		defer func() {
			ticker.Stop()
		}()
		for {
			select {
			case <-s.ctx.Done():
				return
			case <-ticker.C:
				ticker.Stop()
				if err := s.subscribe(address); err != nil {
					s.logger.Error("failed to subscribe", zap.Error(err))
				}
				ticker.Reset(s.interval)
			}
		}
	}()
	return true
}

func (s *Invoker) GetTransactions(address string) []Transaction {
	block := s.GetBlock(address)
	if block == nil {
		return nil
	}
	var transactions []Transaction
	for _, value := range block.Result.Transactions {
		request := map[string]interface{}{
			"jsonrpc": s.jsonrpc,
			"method":  "eth_getTransactionByHash",
			"params":  []string{value},
			"id":      uuid.New().ID(),
		}
		var failureRaw rest.Raw
		var out TransactionResult
		_, err := s.cli.SetContext(s.ctx).Post("").
			SetHeader("Content-Type", "application/json").
			BodyJSON(&request).Receive(&out, &failureRaw)
		if err != nil {
			s.logger.Error("failed to execute request", zap.Error(err))
			return nil
		}
		if failureRaw != nil {
			s.logger.Error("failed to fetch current block", zap.ByteString("raw", failureRaw))
			return nil
		}
		transactions = append(transactions, out.Result)
	}
	return transactions
}

func (s *Invoker) subscribe(address string) error {
	blockInfo, err := s.repo.GetBlockInfo(s.ctx, address)
	if err != nil && !errors.Is(err, repositories.ErrNotFound) {
		return err
	}

	hexCount := s.CountBlockTransaction(address)
	if hexCount == "" {
		return errors.New("failed to fetch block count")
	}
	count := utils.ConvertHexToDec(hexCount)

	if blockInfo != nil && blockInfo.Count == count {
		return nil
	}

	var nexIndex int
	if blockInfo != nil {
		nexIndex = blockInfo.Count
	}

	var blockTransactions []*models.BlockTransaction
	var latest string
	for idx := nexIndex; idx < count; idx++ {
		hexIndex := fmt.Sprintf("%#x", idx)
		trans := s.GetTransactionByIndex(address, hexIndex)
		blockTransactions = append(blockTransactions, &models.BlockTransaction{
			BlockAddress:       address,
			TransactionAddress: trans.Hash,
			CreatedAt:          time.Now().UTC(),
		})
		latest = trans.Hash
	}
	_ = s.repo.CreateBlockTransactions(s.ctx, blockTransactions)
	_ = s.repo.UpsertBlockInfo(s.ctx, &models.BlockInfo{
		BlockAddress:             address,
		Count:                    count,
		LatestTransactionAddress: latest,
	})
	return nil
}

func (s *Invoker) GetBlock(address string) *BlockResult {
	request := map[string]interface{}{
		"jsonrpc": s.jsonrpc,
		"method":  "eth_getBlockByHash",
		"params":  []interface{}{address, false},
		"id":      uuid.New().ID(),
	}
	var failureRaw rest.Raw
	var out BlockResult
	_, err := s.cli.SetContext(s.ctx).Post("").
		SetHeader("Content-Type", "application/json").
		BodyJSON(&request).Receive(&out, &failureRaw)
	if err != nil {
		s.logger.Error("failed to execute request", zap.Error(err))
		return nil
	}
	if failureRaw != nil {
		s.logger.Error("failed to fetch block", zap.ByteString("raw", failureRaw))
		return nil
	}
	return &out
}

func (s *Invoker) GetTransactionByIndex(address, index string) *Transaction {
	request := map[string]interface{}{
		"jsonrpc": s.jsonrpc,
		"method":  "eth_getTransactionByBlockHashAndIndex",
		"params":  []string{address, index},
		"id":      uuid.New().ID(),
	}
	var failureRaw rest.Raw
	var out TransactionResult
	_, err := s.cli.SetContext(s.ctx).Post("").
		SetHeader("Content-Type", "application/json").
		BodyJSON(&request).Receive(&out, &failureRaw)
	if err != nil {
		s.logger.Error("failed to execute request", zap.Error(err))
		return nil
	}
	if failureRaw != nil {
		s.logger.Error("failed to fetch current block", zap.ByteString("raw", failureRaw))
		return nil
	}
	return &out.Result
}

func (s *Invoker) CountBlockTransaction(address string) string {
	request := map[string]interface{}{
		"jsonrpc": s.jsonrpc,
		"method":  "eth_getBlockTransactionCountByHash",
		"params":  []string{address},
		"id":      uuid.New().ID(),
	}
	var failureRaw rest.Raw
	var out CountBlockTransaction
	_, err := s.cli.SetContext(s.ctx).Post("").
		SetHeader("Content-Type", "application/json").
		BodyJSON(&request).Receive(&out, &failureRaw)
	if err != nil {
		s.logger.Error("failed to execute request", zap.Error(err))
		return ""
	}
	if failureRaw != nil {
		s.logger.Error("failed to fetch block count", zap.ByteString("raw", failureRaw))
		return ""
	}
	return out.Result
}
