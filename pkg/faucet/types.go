package faucet

import (
	"database/sql"
	"encoding/json"
	"time"
)

// FaucetConfig holds the configuration for a single faucet instance (database representation).
type FaucetConfig struct {
	FaucetName                 string         `db:"name" json:"name"`
	Slug                       string         `db:"slug" json:"slug"`
	InstanceMasterPublicKeyHex string         `db:"instance_master_pubkey_hex" json:"instance_master_pubkey_hex"`
	DerivationCpPubKeyHex      string         `db:"counterparty_pubkey" json:"counterparty_pubkey"`
	DerivationInvoiceNum       string         `db:"derivation_invoice_num" json:"derivation_invoice_num"`
	NextChangeInvoiceIdx       int64          `db:"next_change_invoice_idx" json:"next_change_invoice_idx"`
	NextDepositInvoiceIdx      int64          `db:"next_deposit_invoice_idx" json:"next_deposit_invoice_idx"`
	FixedDropSats              sql.NullInt64  `db:"drop_sats" json:"fixed_drop_sats"`
	APIKeyHash                 sql.NullString `db:"api_key_hash" json:"api_key_hash"`
	TargetBuckets              sql.NullInt64  `db:"target_buckets" json:"target_buckets"`
	CurrentBucketIdx           sql.NullInt64  `db:"current_bucket_idx" json:"current_bucket_idx"`
	MaxConsolidationInputs     sql.NullInt64  `db:"max_consolidation_inputs" json:"max_consolidation_inputs"`
	CreatedAt                  time.Time      `db:"created_at" json:"created_at"`
	UpdatedAt                  time.Time      `db:"updated_at" json:"updated_at"`
	OwnerUserPublicKeyHex      sql.NullString `db:"owner_pubkey" json:"owner_pubkey"`
	BalanceSats                int64          `json:"balance_sats"`
}

// Droplit is the representation of FaucetConfig for API responses with clean JSON.
type Droplit struct {
	Name                   string         `json:"name"`
	Slug                   string         `json:"slug"`
	InstancePubkey         string         `json:"instance_pubkey"`
	CounterpartyPubkey     string         `json:"counterparty_pubkey"`
	DerivationInvoiceNum   string         `json:"derivation_invoice_num"`
	NextChangeInvoiceIdx   int64          `json:"next_change_invoice_idx"`
	NextDepositInvoiceIdx  int64          `json:"next_deposit_invoice_idx"`
	DropSats               NullableInt64  `json:"drop_sats,omitempty"`
	APIKeyHash             NullableString `json:"api_key_hash,omitempty"`
	TargetBuckets          NullableInt64  `json:"target_buckets,omitempty"`
	CurrentBucketIdx       NullableInt64  `json:"current_bucket_idx,omitempty"`
	MaxConsolidationInputs NullableInt64  `json:"max_consolidation_inputs,omitempty"`
	CreatedAt              time.Time      `json:"created_at"`
	UpdatedAt              time.Time      `json:"updated_at"`
	OwnerPubkey            NullableString `json:"owner_pubkey,omitempty"`
	BalanceSats            int64          `json:"balance_sats"`
}

// NullableString is a wrapper for sql.NullString to marshal as string or null.
type NullableString struct {
	sql.NullString
}

func (ns NullableString) MarshalJSON() ([]byte, error) {
	if !ns.Valid {
		return []byte("null"), nil
	}
	return json.Marshal(ns.String)
}

func (ns *NullableString) UnmarshalJSON(data []byte) error {
	if string(data) == "null" {
		ns.Valid = false
		return nil
	}
	ns.Valid = true
	return json.Unmarshal(data, &ns.String)
}

// NullableInt64 is a wrapper for sql.NullInt64 to marshal as int64 or null.
type NullableInt64 struct {
	sql.NullInt64
}

func (ni NullableInt64) MarshalJSON() ([]byte, error) {
	if !ni.Valid {
		return []byte("null"), nil
	}
	return json.Marshal(ni.Int64)
}

func (ni *NullableInt64) UnmarshalJSON(data []byte) error {
	if string(data) == "null" {
		ni.Valid = false
		return nil
	}
	ni.Valid = true
	return json.Unmarshal(data, &ni.Int64)
}

// --- Request/Response types ---

type FaucetActionKind string

const (
	FaucetActionTap  FaucetActionKind = "tap"
	FaucetActionPush FaucetActionKind = "push"
	FaucetActionMint FaucetActionKind = "mint"
	FaucetActionFund FaucetActionKind = "fund"
)

type FaucetActionRequest struct {
	Kind             FaucetActionKind `json:"kind"`
	RecipientAddress string           `json:"recipientAddress,omitempty"`
	AmountSat        *uint64          `json:"amountSat,omitempty"`
	Data             []string         `json:"data,omitempty"`
	Encoding         string           `json:"encoding,omitempty"`
	ContentType      string           `json:"contentType,omitempty"`
	Inscription      string           `json:"inscription,omitempty"`
	BSV21            *BSV21Metadata   `json:"bsv21,omitempty"`
	RawTx            string           `json:"rawtx,omitempty"`
	Broadcast        *bool            `json:"broadcast,omitempty"`
}

type FaucetActionResponse struct {
	Success       bool   `json:"success"`
	Kind          string `json:"kind"`
	TxID          string `json:"txid"`
	RawTx         string `json:"rawtx,omitempty"`
	Message       string `json:"message,omitempty"`
	InscriptionID string `json:"inscriptionId,omitempty"`
}

type FaucetStatusResponse struct {
	FaucetName                   string `json:"faucet_name"`
	Slug                         string `json:"faucet_slug"`
	BalanceSatoshis              int64  `json:"balance_satoshis"`
	UnspentUTXOCount             int64  `json:"unspent_utxo_count"`
	FixedDropSatoshis            int64  `json:"fixed_drop_sats"`
	SpendableUTXOCount           int64  `json:"spendable_utxo_count"`
	ConsolidatingBalanceSatoshis int64  `json:"consolidating_balance_satoshis"`
	ConsolidatingUTXOCount       int64  `json:"consolidating_utxo_count"`
}

type FaucetDiagnosticData struct {
	FaucetName              string `json:"faucet_name"`
	NextDepositInvoiceIdx   int64  `json:"next_deposit_invoice_idx"`
	CurrentUTXOCount        int64  `json:"current_utxo_count"`
	CurrentUTXOAddressCount int64  `json:"current_utxo_address_count"`
}

type CreateUserFaucetRequest struct {
	Name                   string `json:"name"`
	FixedDropSats          int64  `json:"fixed_drop_sats,omitempty"`
	MaxConsolidationInputs int64  `json:"max_consolidation_inputs,omitempty"`
}

type CreateUserFaucetResponse struct {
	FaucetName      string `json:"faucet_name"`
	Slug            string `json:"slug"`
	InstancePubKey  string `json:"instance_pubkey"`
	OwnerPubKey     string `json:"owner_pubkey"`
	GeneratedAPIKey string `json:"generated_api_key"`
	Message         string `json:"message"`
}

type ProvisionFaucetRequest struct {
	FaucetIdentifierName       string `json:"faucet_identifier_name"`
	OwnerCounterpartyPubKeyHex string `json:"owner_counterparty_pubkey_hex"`
	FixedDropSats              *int64 `json:"fixed_drop_sats,omitempty"`
	MaxConsolidationInputs     *int64 `json:"max_consolidation_inputs,omitempty"`
	TargetBuckets              *int64 `json:"target_buckets,omitempty"`
}

type ProvisionFaucetResponse struct {
	Message                       string `json:"message"`
	FaucetIdentifierName          string `json:"faucet_identifier_name"`
	Slug                          string `json:"slug"`
	FaucetInstanceMasterPubKeyHex string `json:"faucet_instance_pubkey"`
	FixedDropSats                 int64  `json:"fixed_drop_sats"`
	RawAPIKey                     string `json:"raw_api_key"`
	MaxConsolidationInputs        int64  `json:"max_consolidation_inputs"`
	TargetBuckets                 int64  `json:"target_buckets"`
}

type ListFaucetInstancesResponse struct {
	Droplits []Droplit `json:"droplits"`
}

type DepositAddressResponse struct {
	DepositAddress       string `json:"deposit_address"`
	DepositInvoiceNumber string `json:"deposit_invoice_number"`
	FaucetName           string `json:"faucet_name"`
}

type TapRequestPayload struct {
	RecipientAddress string  `json:"recipient_address"`
	Satoshis         *uint64 `json:"satoshis,omitempty"`
}

type TapResponse struct {
	TxID string `json:"txid"`
}

type PushDataRequest struct {
	Data       []string `json:"data"`
	Encoding   string   `json:"encoding"`
	Broadcast  *bool    `json:"broadcast"`
	FillInputs *bool    `json:"fillInputs"`
}

type PushDataResponse struct {
	TxID    string `json:"txid"`
	Message string `json:"message"`
	RawTx   string `json:"rawtx,omitempty"`
}

type FundTransactionRequest struct {
	RawTx     string `json:"rawtx"`
	Broadcast *bool  `json:"broadcast"`
}

type FundTransactionResponse struct {
	TxID    string `json:"txid"`
	Message string `json:"message"`
	RawTx   string `json:"rawtx,omitempty"`
}

type MintInscriptionRequest struct {
	RecipientAddress string         `json:"recipientAddress"`
	ContentType      string         `json:"contentType"`
	Data             string         `json:"data"`
	Encoding         string         `json:"encoding"`
	FeeRate          *float64       `json:"feeRate"`
	Broadcast        *bool          `json:"broadcast"`
	BSV21            *BSV21Metadata `json:"bsv21,omitempty"`
}

type BSV21Metadata struct {
	Ticker *string `json:"ticker,omitempty"`
	Max    *string `json:"max,omitempty"`
	Limit  *string `json:"limit,omitempty"`
	Dec    *int    `json:"dec,omitempty"`
}

type MintInscriptionSuccessResponse struct {
	Success         bool    `json:"success"`
	TxID            string  `json:"txid"`
	InscriptionID   string  `json:"inscriptionId"`
	OutputIndex     uint32  `json:"outputIndex"`
	Fee             int64   `json:"fee"`
	Size            int     `json:"size"`
	FeeRate         float64 `json:"feeRate"`
	Broadcasted     bool    `json:"broadcasted"`
	ContentType     string  `json:"contentType"`
	InscriptionSize int     `json:"inscriptionSize"`
}

type MintInscriptionErrorDetails struct {
	AvailableSatoshis   *int64 `json:"availableSatoshis,omitempty"`
	RequiredSatoshis    *int64 `json:"requiredSatoshis,omitempty"`
	InscriptionTooLarge *bool  `json:"inscriptionTooLarge,omitempty"`
	MaxSize             *int   `json:"maxSize,omitempty"`
	InvalidContentType  *bool  `json:"invalidContentType,omitempty"`
}

type MintInscriptionErrorResponse struct {
	Success bool                         `json:"success"`
	Error   string                       `json:"error"`
	Message string                       `json:"message"`
	Details *MintInscriptionErrorDetails `json:"details,omitempty"`
}

type APIKeyEntry struct {
	FaucetName       string    `json:"faucet_name"`
	UserPublicKeyHex string    `json:"user_public_key_hex"`
	Label            *string   `json:"label,omitempty"`
	CreatedAt        time.Time `json:"created_at"`
}

type ListAPIKeysResponse struct {
	Keys []APIKeyEntry `json:"keys"`
}

type AddAPIKeyRequest struct {
	PublicKey string  `json:"public_key"`
	Label     *string `json:"label,omitempty"`
}

type AddAPIKeyResponse struct {
	Success bool   `json:"success"`
	Message string `json:"message"`
}

type DeleteAPIKeyResponse struct {
	Success bool   `json:"success"`
	Message string `json:"message"`
}

type ScanDepositsResponse struct {
	FaucetName            string `json:"faucet_name"`
	AddressesScanned      int    `json:"addresses_scanned"`
	NewUTXOsIngested      int    `json:"new_utxos_ingested"`
	TotalSatoshisIngested int64  `json:"total_satoshis_ingested"`
	Message               string `json:"message"`
}

type SyncFaucetAddressesRequest struct {
	Addresses            string `json:"addresses,omitempty"`
	StartInvoiceNumber   string `json:"start_invoice_number,omitempty"`
	EndInvoiceNumber     string `json:"end_invoice_number,omitempty"`
	ScanAllKnownInvoices string `json:"scan_all_known_invoices,omitempty"`
}

type GetFaucetActivityParams struct {
	Limit  int `json:"limit,omitempty"`
	Offset int `json:"offset,omitempty"`
}

type GetFaucetActivityResponse struct {
	Activities []FaucetActivityItem `json:"activities"`
}

type UpdateFaucetConfigAdminRequest struct {
	FaucetIdentifierName   string `json:"faucet_identifier_name"`
	FixedDropSats          *int64 `json:"fixed_drop_sats,omitempty"`
	TargetBuckets          *int64 `json:"target_buckets,omitempty"`
	MaxConsolidationInputs *int64 `json:"max_consolidation_inputs,omitempty"`
	CurrentBucketIdx       *int64 `json:"current_bucket_idx,omitempty"`
}

type MigrateOwnerRequest struct {
	OldOwnerPubkey string `json:"old_owner_pubkey"`
	NewOwnerPubkey string `json:"new_owner_pubkey"`
}

type MigrateOwnerResponse struct {
	UpdatedCount int      `json:"updated_count"`
	FaucetNames  []string `json:"faucet_names"`
	Message      string   `json:"message"`
}

// FaucetActivityType defines the type of activity.
type FaucetActivityType string

const (
	ActivityTypeTap       FaucetActivityType = "tap"
	ActivityTypePush      FaucetActivityType = "push"
	ActivityTypeMint      FaucetActivityType = "mint"
	ActivityTypeDeposit   FaucetActivityType = "deposit"
	ActivityTypeScanStart FaucetActivityType = "scan_start"
	ActivityTypeScanEnd   FaucetActivityType = "scan_end"
)

// FaucetActivityItem represents a single activity event for a faucet.
type FaucetActivityItem struct {
	Timestamp     time.Time          `json:"timestamp"`
	Type          FaucetActivityType `json:"type"`
	TxID          string             `json:"txid"`
	AmountSat     int64              `json:"amount_sat"`
	Description   string             `json:"description"`
	Details       json.RawMessage    `json:"details,omitempty"`
	FaucetName    string             `json:"faucet_name"`
	PrettyTimeAgo string             `json:"pretty_time_ago"`
}
