package service

import (
	"encoding/json"
	"fmt"

	"github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/domain"
	"github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/pkg/apperror"
	"github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/pkg/crypto"
)

// encryptOzonCredentials serializes the Ozon credential set to JSON and
// encrypts it with AES-256-GCM (same key as WB tokens). The result goes into
// seller_cabinets.encrypted_credentials.
func encryptOzonCredentials(creds domain.OzonCredentials, key []byte) (string, error) {
	payload, err := json.Marshal(creds)
	if err != nil {
		return "", fmt.Errorf("marshal ozon credentials: %w", err)
	}
	encrypted, err := crypto.Encrypt(string(payload), key)
	if err != nil {
		return "", fmt.Errorf("encrypt ozon credentials: %w", err)
	}
	return encrypted, nil
}

// decryptOzonCredentialsBlob is the inverse of encryptOzonCredentials.
func decryptOzonCredentialsBlob(encrypted string, key []byte) (domain.OzonCredentials, error) {
	var creds domain.OzonCredentials
	if encrypted == "" {
		return creds, fmt.Errorf("cabinet has no encrypted ozon credentials")
	}
	plaintext, err := crypto.Decrypt(encrypted, key)
	if err != nil {
		return creds, fmt.Errorf("decrypt ozon credentials: %w", err)
	}
	if err := json.Unmarshal([]byte(plaintext), &creds); err != nil {
		return creds, fmt.Errorf("unmarshal ozon credentials: %w", err)
	}
	return creds, nil
}

// decryptOzonCredentials returns the Ozon credential set of a cabinet.
// It fails for non-Ozon cabinets so WB cabinets can never be misused as Ozon.
func (s *SellerCabinetService) decryptOzonCredentials(cabinet domain.SellerCabinet) (domain.OzonCredentials, error) {
	if cabinet.Marketplace != domain.MarketplaceOzon {
		return domain.OzonCredentials{}, apperror.New(apperror.ErrValidation, "seller cabinet is not an Ozon cabinet")
	}
	creds, err := decryptOzonCredentialsBlob(cabinet.EncryptedCredentials, s.encryptionKey)
	if err != nil {
		return domain.OzonCredentials{}, apperror.New(apperror.ErrInternal, "failed to decrypt ozon credentials")
	}
	return creds, nil
}
