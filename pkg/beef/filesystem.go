package beef

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/bsv-blockchain/go-sdk/chainhash"
)

type FilesystemBeefStorage struct {
	basePath string
}

func NewFilesystemBeefStorage(basePath string) (*FilesystemBeefStorage, error) {
	if err := os.MkdirAll(basePath, 0755); err != nil {
		return nil, fmt.Errorf("failed to create base directory: %w", err)
	}

	return &FilesystemBeefStorage{
		basePath: basePath,
	}, nil
}

func (f *FilesystemBeefStorage) getFilePath(txid *chainhash.Hash) string {
	txidStr := txid.String()
	subDir := txidStr[:2]
	fileName := txidStr + ".beef"
	return filepath.Join(f.basePath, subDir, fileName)
}

func (f *FilesystemBeefStorage) Get(ctx context.Context, txid *chainhash.Hash) ([]byte, error) {
	filePath := f.getFilePath(txid)
	beefBytes, err := os.ReadFile(filePath)
	if os.IsNotExist(err) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("failed to read beef file: %w", err)
	}
	return beefBytes, nil
}

func (f *FilesystemBeefStorage) Put(ctx context.Context, txid *chainhash.Hash, beefBytes []byte) error {
	filePath := f.getFilePath(txid)

	dir := filepath.Dir(filePath)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("failed to create directory: %w", err)
	}

	// Fixed .tmp path races under concurrent Put of the same txid (e.g. shared
	// BEEF ancestors from parallel crawl workers). Another writer may rename
	// the shared tmp away before we do; if the final file is present, treat as success.
	tempFile := filePath + ".tmp"
	if err := os.WriteFile(tempFile, beefBytes, 0644); err != nil {
		return fmt.Errorf("failed to write temp file: %w", err)
	}

	if err := os.Rename(tempFile, filePath); err != nil {
		_ = os.Remove(tempFile)
		if _, statErr := os.Stat(filePath); statErr == nil {
			return nil
		}
		return fmt.Errorf("failed to rename file: %w", err)
	}

	return nil
}

func (f *FilesystemBeefStorage) UpdateMerklePath(ctx context.Context, txid *chainhash.Hash) ([]byte, error) {
	return nil, nil
}

func (f *FilesystemBeefStorage) GetRawTx(ctx context.Context, txid *chainhash.Hash) ([]byte, error) {
	beefBytes, err := f.Get(ctx, txid)
	if err != nil {
		return nil, err
	}
	rawTx, _, err := splitBEEF(beefBytes)
	return rawTx, err
}

func (f *FilesystemBeefStorage) GetProof(ctx context.Context, txid *chainhash.Hash) ([]byte, error) {
	beefBytes, err := f.Get(ctx, txid)
	if err != nil {
		return nil, err
	}
	_, proof, err := splitBEEF(beefBytes)
	if err != nil {
		return nil, err
	}
	if len(proof) == 0 {
		return nil, ErrNotFound
	}
	return proof, nil
}

func (f *FilesystemBeefStorage) Close() error {
	return nil
}
