package repositories

import (
	"context"
	"errors"
	"github.com/dungnh3/trustwallet-assignment/models"
	"sync"
)

var ErrNotFound = errors.New("record not found")

type Repository interface {
	GetBlockInfo(ctx context.Context, blockAddress string) (*models.BlockInfo, error)
	UpsertBlockInfo(ctx context.Context, blockInfo *models.BlockInfo) error
	CreateBlockTransactions(ctx context.Context, blockTransactions []*models.BlockTransaction) error
}

type InMemory struct {
	mapBlockInfo      *sync.Map
	blockTransactions []*models.BlockTransaction
}

func New() *InMemory {
	return &InMemory{
		mapBlockInfo:      &sync.Map{},
		blockTransactions: nil,
	}
}

func (s *InMemory) GetBlockInfo(ctx context.Context, blockAddress string) (*models.BlockInfo, error) {
	value, ok := s.mapBlockInfo.Load(blockAddress)
	if !ok {
		return nil, ErrNotFound
	}
	return value.(*models.BlockInfo), nil
}

func (s *InMemory) UpsertBlockInfo(ctx context.Context, blockInfo *models.BlockInfo) error {
	s.mapBlockInfo.Store(blockInfo.BlockAddress, blockInfo)
	return nil
}

func (s *InMemory) CreateBlockTransactions(ctx context.Context, blockTransactions []*models.BlockTransaction) error {
	s.blockTransactions = append(s.blockTransactions, blockTransactions...)
	return nil
}
