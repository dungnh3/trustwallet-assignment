package parser

type BlockNumber struct {
	JsonRPC string `json:"jsonrpc"`
	Result  string `json:"result"`
	ID      int    `json:"id"`
}

type CountBlockTransaction struct {
	JsonRPC string `json:"jsonrpc"`
	Result  string `json:"result"`
	ID      int    `json:"id"`
}

type Transaction struct {
	Type             string `json:"type"`
	BlockHash        string `json:"blockHash"`
	BlockNumber      string `json:"blockNumber"`
	From             string `json:"from"`
	To               string `json:"to"`
	Gas              string `json:"gas"`
	Hash             string `json:"hash"`
	Input            string `json:"input"`
	Nonce            string `json:"nonce"`
	TransactionIndex string `json:"transactionIndex"`
	Value            string `json:"value"`
	V                string `json:"v"`
	R                string `json:"r"`
	S                string `json:"s"`
	GasPrice         string `json:"gasPrice"`
	ChainID          string `json:"chainId"`
}

type TransactionResult struct {
	JsonRPC string      `json:"jsonrpc"`
	Result  Transaction `json:"result"`
	ID      int         `json:"id"`
}

type Block struct {
	Difficulty       string   `json:"difficulty"`
	ExtraData        string   `json:"extraData"`
	GasLimit         string   `json:"gasLimit"`
	GasUsed          string   `json:"gasUsed"`
	Hash             string   `json:"hash"`
	LogsBloom        string   `json:"logsBloom"`
	Miner            string   `json:"miner"`
	MixHash          string   `json:"mixHash"`
	Nonce            string   `json:"nonce"`
	Number           string   `json:"number"`
	ParentHash       string   `json:"parentHash"`
	ReceiptsRoot     string   `json:"receiptsRoot"`
	Sha3Uncles       string   `json:"sha3Uncles"`
	Size             string   `json:"size"`
	StateRoot        string   `json:"stateRoot"`
	Timestamp        string   `json:"timestamp"`
	TotalDifficulty  string   `json:"totalDifficulty"`
	Transactions     []string `json:"transactions"`
	TransactionsRoot string   `json:"transactionsRoot"`
	Uncles           []string `json:"uncles"`
}

type BlockResult struct {
	JsonRPC string `json:"jsonrpc"`
	Result  Block  `json:"result"`
	ID      int    `json:"id"`
}
