package faucet

import (
	"context"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"strings"

	"github.com/bsv-blockchain/go-sdk/script"
	"github.com/bsv-blockchain/go-sdk/transaction"
	p2pkh "github.com/bsv-blockchain/go-sdk/transaction/template/p2pkh"
	sdk "github.com/bsv-blockchain/go-sdk/wallet"
	walletcore "github.com/bsv-blockchain/go-wallet-toolbox/pkg/wallet"
)

func (s *Service) ExecuteWalletAction(ctx context.Context, faucetName string, req *FaucetActionRequest, originator string) (*FaucetActionResponse, error) {
	if strings.TrimSpace(originator) == "" {
		return nil, fmt.Errorf("originator cannot be empty")
	}

	wallet, config, err := s.GetFaucetWallet(ctx, faucetName)
	if err != nil {
		return nil, err
	}

	actionArgs, postProcess, err := s.buildCreateActionArgs(ctx, wallet, config, req)
	if err != nil {
		return nil, err
	}

	noSend := req.Broadcast != nil && !*req.Broadcast

	signAndProcess := false
	actionArgs.Options = &sdk.CreateActionOptions{
		SignAndProcess:         &signAndProcess,
		AcceptDelayedBroadcast: ptrBool(false),
		RandomizeOutputs:       ptrBool(false),
	}

	createRes, err := wallet.CreateAction(ctx, actionArgs, originator)
	if err != nil {
		return nil, fmt.Errorf("create action failed: %w", err)
	}
	if createRes.SignableTransaction == nil {
		return nil, fmt.Errorf("create action did not return signable transaction")
	}

	signArgs := sdk.SignActionArgs{
		Reference: createRes.SignableTransaction.Reference,
	}
	if noSend {
		signArgs.Options = &sdk.SignActionOptions{
			NoSend: ptrBool(true),
		}
	}

	signRes, err := wallet.SignAction(ctx, signArgs, originator)
	if err != nil {
		return nil, fmt.Errorf("sign action failed: %w", err)
	}

	response := &FaucetActionResponse{
		Success: true,
		Kind:    string(req.Kind),
		TxID:    signRes.Txid.String(),
		RawTx:   hex.EncodeToString(signRes.Tx),
	}
	if noSend {
		response.Message = "transaction signed (not broadcast)"
	} else {
		response.Message = "transaction broadcast"
	}

	if postProcess != nil {
		postProcess(response, signRes)
	}

	return response, nil
}

func (s *Service) buildCreateActionArgs(
	ctx context.Context,
	w *walletcore.Wallet,
	config *FaucetConfig,
	req *FaucetActionRequest,
) (sdk.CreateActionArgs, func(*FaucetActionResponse, *sdk.SignActionResult), error) {
	switch req.Kind {
	case FaucetActionTap:
		amount := uint64(0)
		if req.AmountSat != nil {
			amount = *req.AmountSat
		} else if config.FixedDropSats.Valid && config.FixedDropSats.Int64 > 0 {
			amount = uint64(config.FixedDropSats.Int64)
		}

		// No explicit amount and no fixed drop — send all funds minus fees
		if amount == 0 {
			limit := uint32(10000)
			result, err := w.ListOutputs(ctx, sdk.ListOutputsArgs{
				Basket: "default",
				Limit:  &limit,
			}, "")
			if err != nil {
				return sdk.CreateActionArgs{}, nil, fmt.Errorf("failed to list outputs for send-all: %w", err)
			}
			var total uint64
			var inputCount int
			for _, out := range result.Outputs {
				if out.Spendable {
					total += out.Satoshis
					inputCount++
				}
			}
			if inputCount == 0 {
				return sdk.CreateActionArgs{}, nil, fmt.Errorf("no spendable outputs")
			}
			// P2PKH input ~148 bytes, 1 output ~34 bytes, overhead ~10 bytes, 100 sat/KB
			txSize := uint64(inputCount*148 + 34 + 10)
			fee := (txSize * 100) / 1000
			if fee == 0 {
				fee = 1
			}
			if total <= fee {
				return sdk.CreateActionArgs{}, nil, fmt.Errorf("balance (%d sats) is insufficient to cover fees (%d sats)", total, fee)
			}
			amount = total - fee
		}

		if amount == 0 {
			return sdk.CreateActionArgs{}, nil, fmt.Errorf("tap amount is zero or not configured")
		}
		if req.RecipientAddress == "" {
			return sdk.CreateActionArgs{}, nil, fmt.Errorf("recipientAddress is required")
		}

		// Try as base58 address first, fall back to pubkey hex
		addr, err := script.NewAddressFromString(req.RecipientAddress)
		if err != nil {
			addr, err = script.NewAddressFromPublicKeyString(req.RecipientAddress, true)
			if err != nil {
				return sdk.CreateActionArgs{}, nil, fmt.Errorf("invalid recipientAddress (not a valid address or pubkey): %w", err)
			}
		}
		lockingScript, err := p2pkh.Lock(addr)
		if err != nil {
			return sdk.CreateActionArgs{}, nil, fmt.Errorf("failed to create recipient locking script: %w", err)
		}

		return sdk.CreateActionArgs{
			Description: "Droplit tap payout",
			Outputs: []sdk.CreateActionOutput{
				{
					LockingScript:     lockingScript.Bytes(),
					Satoshis:          amount,
					OutputDescription: fmt.Sprintf("Tap payout to %s", req.RecipientAddress),
					Tags:              []string{"droplit", "tap"},
				},
			},
			Labels: []string{"droplit:tapped"},
		}, nil, nil

	case FaucetActionPush:
		if len(req.Data) == 0 {
			return sdk.CreateActionArgs{}, nil, fmt.Errorf("data cannot be empty")
		}
		template, err := BuildOpReturnTemplate(req.Data, req.Encoding)
		if err != nil {
			return sdk.CreateActionArgs{}, nil, err
		}
		if len(template.Outputs) == 0 {
			return sdk.CreateActionArgs{}, nil, fmt.Errorf("OP_RETURN template has no outputs")
		}

		outputs := make([]sdk.CreateActionOutput, 0, len(template.Outputs))
		for i, out := range template.Outputs {
			outputs = append(outputs, sdk.CreateActionOutput{
				LockingScript:     out.LockingScript.Bytes(),
				Satoshis:          out.Satoshis,
				OutputDescription: fmt.Sprintf("Push output %d", i),
				Tags:              []string{"droplit", "push"},
			})
		}

		return sdk.CreateActionArgs{
			Description: "Droplit push record",
			Outputs:     outputs,
			Labels:      []string{"droplit:pushed"},
		}, nil, nil

	case FaucetActionMint:
		if req.ContentType == "" {
			return sdk.CreateActionArgs{}, nil, fmt.Errorf("contentType is required")
		}
		if req.Inscription == "" {
			return sdk.CreateActionArgs{}, nil, fmt.Errorf("inscription is required")
		}

		encoding := req.Encoding
		if encoding == "" {
			encoding = "utf8"
		}

		var inscriptionBytes []byte
		switch encoding {
		case "utf8":
			inscriptionBytes = []byte(req.Inscription)
		case "hex":
			decoded, err := hex.DecodeString(req.Inscription)
			if err != nil {
				return sdk.CreateActionArgs{}, nil, fmt.Errorf("invalid hex inscription: %w", err)
			}
			inscriptionBytes = decoded
		case "base64":
			decoded, err := base64.StdEncoding.DecodeString(req.Inscription)
			if err != nil {
				return sdk.CreateActionArgs{}, nil, fmt.Errorf("invalid base64 inscription: %w", err)
			}
			inscriptionBytes = decoded
		default:
			return sdk.CreateActionArgs{}, nil, fmt.Errorf("unsupported inscription encoding: %s", encoding)
		}

		inscriptionScript, err := CreateInscriptionScript(req.ContentType, inscriptionBytes, req.BSV21)
		if err != nil {
			return sdk.CreateActionArgs{}, nil, err
		}

		amount := uint64(546)
		if req.AmountSat != nil && *req.AmountSat > 0 {
			amount = *req.AmountSat
		}

		postProcess := func(res *FaucetActionResponse, signRes *sdk.SignActionResult) {
			res.InscriptionID = fmt.Sprintf("%s_%d", signRes.Txid.String(), 0)
		}

		return sdk.CreateActionArgs{
			Description: "Droplit mint inscription",
			Outputs: []sdk.CreateActionOutput{
				{
					LockingScript:     inscriptionScript.Bytes(),
					Satoshis:          amount,
					OutputDescription: fmt.Sprintf("Mint inscription (%s)", req.ContentType),
					Tags:              []string{"droplit", "mint"},
				},
			},
			Labels: []string{"droplit:minted"},
		}, postProcess, nil

	case FaucetActionFund:
		if req.RawTx == "" {
			return sdk.CreateActionArgs{}, nil, fmt.Errorf("rawtx is required")
		}
		txBytes, err := hex.DecodeString(req.RawTx)
		if err != nil {
			return sdk.CreateActionArgs{}, nil, fmt.Errorf("invalid rawtx hex: %w", err)
		}
		template, err := transaction.NewTransactionFromBytes(txBytes)
		if err != nil {
			return sdk.CreateActionArgs{}, nil, fmt.Errorf("invalid rawtx transaction: %w", err)
		}
		if len(template.Outputs) == 0 {
			return sdk.CreateActionArgs{}, nil, fmt.Errorf("template transaction has no outputs")
		}

		outputs := make([]sdk.CreateActionOutput, 0, len(template.Outputs))
		for i, out := range template.Outputs {
			outputs = append(outputs, sdk.CreateActionOutput{
				LockingScript:     out.LockingScript.Bytes(),
				Satoshis:          out.Satoshis,
				OutputDescription: fmt.Sprintf("Fund template output %d", i),
				Tags:              []string{"droplit", "fund"},
			})
		}

		return sdk.CreateActionArgs{
			Description: "Droplit fund template transaction",
			Outputs:     outputs,
			Labels:      []string{"droplit:funded"},
		}, nil, nil

	default:
		return sdk.CreateActionArgs{}, nil, fmt.Errorf("unsupported action kind %q", req.Kind)
	}
}

func ptrBool(v bool) *bool {
	return &v
}
